package grip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/alexjoedt/grip/internal/logger"
)

const stateVersion = 2

// Digest sources recorded in Version.DigestSource.
const (
	DigestSourceAPI  = "api-digest"
	DigestSourceNone = "none"
)

// Version records one installed release of a package.
type Version struct {
	Tag          string    `json:"tag"`
	Asset        string    `json:"asset"`
	AssetDigest  string    `json:"assetDigest"`
	DigestSource string    `json:"digestSource"`
	SHA256       string    `json:"sha256"`
	InstalledAt  time.Time `json:"installedAt"`
}

// Installation represents an installed package. Name is the state key.
type Installation struct {
	Name string `json:"-"`
	Repo string `json:"repo"`
	Version
	Previous      *Version `json:"previous,omitempty"`
	Pinned        bool     `json:"pinned"`
	AssetOverride string   `json:"assetOverride"`
	BinOverride   string   `json:"binOverride"`
	// InstallPath is set only on entries migrated from v1, whose binaries
	// may live outside grip's bin directory.
	InstallPath string `json:"installPath,omitempty"`
}

type stateFile struct {
	Version  int                      `json:"version"`
	Packages map[string]*Installation `json:"packages"`
}

// v1Installation is an entry of the unversioned v1 grip.json.
type v1Installation struct {
	Repo        string    `json:"repo"`
	Tag         string    `json:"tag"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	InstallPath string    `json:"installPath"`
}

// state is the in-memory state; v1 holds the raw v1 file until it is backed up.
type state struct {
	packages map[string]*Installation
	v1       []byte
}

// Storage manages installed packages
type Storage struct {
	filepath string
	binDir   string
}

// NewStorage opens the state file. A missing file is empty state; it is
// created by the first save.
func NewStorage(path string, cfg *Config) (*Storage, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(cfg.HomeDir, "grip.lock")); err == nil {
			return nil, errors.New("found legacy grip.lock: run grip v1.1+ once to migrate")
		}
	}
	return &Storage{filepath: path, binDir: cfg.BinDir}, nil
}

// InstallDir returns the directory holding the binary of inst.
func (s *Storage) InstallDir(inst *Installation) string {
	if inst.InstallPath != "" {
		return inst.InstallPath
	}
	return s.binDir
}

// Lock takes the exclusive operation lock <state>.lock, waiting until it is
// free or ctx is done. Reads need no lock since saves replace the file by rename.
func (s *Storage) Lock(ctx context.Context) (unlock func(), err error) {
	f, err := os.OpenFile(s.filepath+".lock", os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}

	waiting := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
		}
		if !waiting {
			logger.Warn("waiting for another grip process")
			waiting = true
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Get retrieves installation by name
func (s *Storage) Get(name string) (*Installation, error) {
	st, err := s.load()
	if err != nil {
		return nil, err
	}

	inst, ok := st.packages[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	return inst, nil
}

// GetByRepo retrieves installation by repository identity.
func (s *Storage) GetByRepo(repo Repo) (*Installation, error) {
	st, err := s.load()
	if err != nil {
		return nil, err
	}

	for _, inst := range st.packages {
		if r, err := ParseRepo(inst.Repo); err == nil && r == repo {
			return inst, nil
		}
	}

	return nil, fmt.Errorf("%w: repo %s", ErrNotFound, repo)
}

// List returns all installations
func (s *Storage) List() ([]*Installation, error) {
	st, err := s.load()
	if err != nil {
		return nil, err
	}

	result := make([]*Installation, 0, len(st.packages))
	for _, inst := range st.packages {
		result = append(result, inst)
	}

	return result, nil
}

// Save stores or updates an installation
func (s *Storage) Save(inst *Installation) error {
	st, err := s.load()
	if err != nil {
		return err
	}

	st.packages[inst.Name] = inst
	return s.save(st)
}

// SetPinned sets or clears the pinned flag of the named packages under the
// lock. An unknown name fails before anything changes.
func (s *Storage) SetPinned(ctx context.Context, pinned bool, names ...string) error {
	unlock, err := s.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	st, err := s.load()
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, ok := st.packages[name]; !ok {
			return fmt.Errorf("package %w: %s", ErrNotFound, name)
		}
	}

	verb := map[bool]string{true: "pinned", false: "unpinned"}[pinned]
	changed := false
	for _, name := range names {
		inst := st.packages[name]
		if inst.Pinned == pinned {
			logger.Println("%s is already %s at %s", name, verb, inst.Tag)
			continue
		}
		inst.Pinned = pinned
		changed = true
		logger.Println("%s %s at %s", name, verb, inst.Tag)
	}
	if !changed {
		return nil
	}
	return s.save(st)
}

// Delete removes an installation by name
func (s *Storage) Delete(name string) error {
	st, err := s.load()
	if err != nil {
		return err
	}

	if _, ok := st.packages[name]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	delete(st.packages, name)
	return s.save(st)
}

// load reads the state file, converting a v1 file in memory.
func (s *Storage) load() (*state, error) {
	raw, err := os.ReadFile(s.filepath)
	if errors.Is(err, os.ErrNotExist) {
		return &state{packages: map[string]*Installation{}}, nil
	}
	if err != nil {
		return nil, err
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("decode %s: %w", s.filepath, err)
	}

	// A v1 package named "version" holds an object, never a number.
	var version int
	if v, ok := top["version"]; !ok || json.Unmarshal(v, &version) != nil {
		packages, err := convertV1(raw)
		if err != nil {
			return nil, fmt.Errorf("decode v1 %s: %w", s.filepath, err)
		}
		return &state{packages: packages, v1: raw}, nil
	}

	if version != stateVersion {
		return nil, fmt.Errorf("%s has unsupported state version %d, this grip supports %d", s.filepath, version, stateVersion)
	}

	var sf stateFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, fmt.Errorf("decode %s: %w", s.filepath, err)
	}
	if sf.Packages == nil {
		sf.Packages = map[string]*Installation{}
	}
	for name, inst := range sf.Packages {
		inst.Name = name
	}
	return &state{packages: sf.Packages}, nil
}

func convertV1(raw []byte) (map[string]*Installation, error) {
	var v1 map[string]*v1Installation
	if err := json.Unmarshal(raw, &v1); err != nil {
		return nil, err
	}

	packages := make(map[string]*Installation, len(v1))
	for name, e := range v1 {
		repo := e.Repo
		if r, err := ParseRepo(repo); err == nil {
			repo = r.String()
		}
		installedAt := e.UpdatedAt
		if installedAt.IsZero() {
			installedAt = e.InstalledAt
		}
		packages[name] = &Installation{
			Name:        name,
			Repo:        repo,
			Version:     Version{Tag: e.Tag, SHA256: e.SHA256, InstalledAt: installedAt},
			InstallPath: e.InstallPath,
		}
	}
	return packages, nil
}

// save writes the state as version 2 atomically. Converting a v1 file keeps
// a one-time copy at <path>.v1.
func (s *Storage) save(st *state) error {
	if st.v1 != nil {
		if err := writeFileExcl(s.filepath+".v1", st.v1); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("back up v1 state: %w", err)
		}
	}

	tmpPath := s.filepath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(stateFile{Version: stateVersion, Packages: st.packages}); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.filepath)
}

func writeFileExcl(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// calculateFileSHA256 computes the SHA256 hash of a file
func calculateFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// Results of Storage.Verify.
const (
	VerifyOK       = "ok"
	VerifyModified = "modified"
	VerifyMissing  = "missing"
	VerifyNoHash   = "no recorded hash"
)

// VerifyResult is the outcome of checking one installed package.
type VerifyResult struct {
	Name, Tag, Result, DigestSource string
}

// Failed reports whether the installed binary is missing or differs from
// the recorded hash.
func (r VerifyResult) Failed() bool {
	return r.Result == VerifyModified || r.Result == VerifyMissing
}

// Verify compares the binary each named package's link resolves to with the
// sha256 recorded for its current version; no names means all packages. It
// takes no lock and changes nothing.
func (s *Storage) Verify(names ...string) ([]VerifyResult, error) {
	var insts []*Installation
	if len(names) == 0 {
		all, err := s.List()
		if err != nil {
			return nil, err
		}
		insts = all
		slices.SortFunc(insts, func(a, b *Installation) int { return strings.Compare(a.Name, b.Name) })
	}
	for _, name := range names {
		inst, err := s.Get(name)
		if err != nil {
			return nil, err
		}
		insts = append(insts, inst)
	}

	results := make([]VerifyResult, 0, len(insts))
	for _, inst := range insts {
		r := VerifyResult{Name: inst.Name, Tag: inst.Tag, DigestSource: inst.DigestSource, Result: VerifyNoHash}
		if inst.SHA256 != "" {
			sum, err := calculateFileSHA256(filepath.Join(s.InstallDir(inst), inst.Name))
			switch {
			case errors.Is(err, os.ErrNotExist):
				r.Result = VerifyMissing
			case err != nil:
				return nil, fmt.Errorf("hash %s: %w", inst.Name, err)
			case strings.EqualFold(sum, inst.SHA256):
				r.Result = VerifyOK
			default:
				r.Result = VerifyModified
			}
		}
		results = append(results, r)
	}
	return results, nil
}

// PackageJSON is the --json form of an installation: the state encoding plus
// name and link path.
type PackageJSON struct {
	Name string `json:"name"`
	Path string `json:"path"`
	*Installation
}

// PackageJSON returns the --json form of inst.
func (s *Storage) PackageJSON(inst *Installation) PackageJSON {
	return PackageJSON{Name: inst.Name, Path: filepath.Join(s.InstallDir(inst), inst.Name), Installation: inst}
}
