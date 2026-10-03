package grip

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alexjoedt/grip/internal/logger"
)

// Installer coordinates installation operations
type Installer struct {
	config     *Config
	storage    *Storage
	source     Source
	httpClient *http.Client
	faultHook  func(stage string) error
}

// NewInstaller creates a new installer
func NewInstaller(cfg *Config, storage *Storage, source Source, httpClient *http.Client) *Installer {
	return &Installer{
		config:     cfg,
		storage:    storage,
		source:     source,
		httpClient: httpClient,
	}
}

// InstallOptions holds installation parameters
type InstallOptions struct {
	Repo  string
	Tag   string
	Force bool
	Alias string
	Asset string // asset name or path.Match pattern, remembered for updates
	Bin   string // base name of the executable in the archive, remembered
}

// Install installs a package from GitHub
func (i *Installer) Install(ctx context.Context, opts InstallOptions) error {
	unlock, err := i.storage.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	cmd := "grip install " + shellArg(opts.Repo)
	if opts.Tag != "" {
		cmd += " --tag " + shellArg(opts.Tag)
	}
	if opts.Alias != "" {
		cmd += " --alias " + shellArg(opts.Alias)
	}
	if opts.Force {
		cmd += " --force"
	}
	return withRetry(i.install(ctx, opts), cmd, opts)
}

// withRetry turns a choiceError into one paste-ready command per candidate.
func withRetry(err error, cmd string, opts InstallOptions) error {
	var ce *choiceError
	if !errors.As(err, &ce) {
		return err
	}
	if ce.flag != "--asset" && opts.Asset != "" {
		cmd += " --asset " + shellArg(opts.Asset)
	}
	if ce.flag != "--bin" && opts.Bin != "" {
		cmd += " --bin " + shellArg(opts.Bin)
	}
	var b strings.Builder
	for _, c := range ce.cands {
		fmt.Fprintf(&b, "\n  %s %s %s", cmd, ce.flag, shellArg(c))
	}
	return fmt.Errorf("%w, choose one:%s", ce.err, b.String())
}

var plainArg = regexp.MustCompile(`^[A-Za-z0-9._+/:=@-]+$`)

// shellArg single-quotes s unless it is safe unquoted in a POSIX shell.
func shellArg(s string) string {
	if plainArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (i *Installer) install(ctx context.Context, opts InstallOptions) error {
	repo, err := ParseRepo(opts.Repo)
	if err != nil {
		return err
	}
	owner, name := repo.Owner, repo.Name

	// Use alias as name if provided
	installName := name
	if opts.Alias != "" {
		installName = opts.Alias
	}

	if err := validName(installName); err != nil {
		return err
	}

	existing, err := i.storage.GetByRepo(repo)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if existing != nil {
		if existing.Name != installName {
			return fmt.Errorf("%s is already installed as %s, remove it first", repo, existing.Name)
		}
		if !opts.Force {
			return fmt.Errorf("%s version %s is already installed", existing.Name, existing.Tag)
		}
	}
	if other, err := i.storage.Get(installName); err == nil && existing == nil {
		return fmt.Errorf("name %s is already used by %s, choose another with --alias", installName, other.Repo)
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	// A leftover in grip's own bin dir without a state entry is overwritten.
	if p, err := exec.LookPath(installName); err == nil && existing == nil && filepath.Dir(p) != i.config.BinDir {
		return fmt.Errorf("%s is already installed from another source: %s", installName, p)
	}

	// Fetch release
	logger.Info("Fetching release %s for %s", tagOrLatest(opts.Tag), repo)
	release, err := fetchRelease(ctx, i.source, repo, opts.Tag)
	if err != nil {
		return err
	}

	dir, err := tagDir(release.Tag)
	if err != nil {
		return err
	}

	assetOverride, binOverride := opts.Asset, opts.Bin
	if existing != nil {
		assetOverride = cmp.Or(assetOverride, existing.AssetOverride)
		binOverride = cmp.Or(binOverride, existing.BinOverride)
	}

	// Parse asset for current platform
	asset, err := parseAsset(release.Assets, i.config, owner, name, assetOverride)
	if err != nil {
		return err
	}

	asset.Tag = release.Tag
	asset.Alias = opts.Alias
	asset.BinOverride = binOverride
	if opts.Asset == asset.Name {
		assetOverride = versionGlob(asset.Name, release.Tag)
	}

	sha256Hash, err := i.installAsset(ctx, asset, filepath.Join(i.pkgDir(installName), dir), installName)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if err := i.stage("switched"); err != nil {
		return err
	}

	inst := &Installation{
		Name: installName,
		Repo: repo.String(),
		Version: Version{
			Tag:         asset.Tag,
			Asset:       asset.Name,
			SHA256:      sha256Hash,
			InstalledAt: time.Now(),
		},
		AssetOverride: assetOverride,
		BinOverride:   binOverride,
	}
	if existing != nil {
		inst.Pinned = existing.Pinned
		inst.Previous = existing.Previous
		if existing.Tag != inst.Tag {
			prev := existing.Version
			inst.Previous = &prev
		}
	}

	if err := i.storage.Save(inst); err != nil {
		return fmt.Errorf("save installation: %w", err)
	}

	if existing != nil && existing.InstallPath != "" {
		old := filepath.Join(existing.InstallPath, installName)
		if old != filepath.Join(i.config.BinDir, installName) {
			if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
				logger.Warn("Could not remove old binary %s: %v", old, err)
			}
		}
	}
	i.cleanStore(inst)

	if !i.config.CheckPathEnv() {
		logger.Warn("The grip path '%s' isn't in PATH", i.config.BinDir)
	}

	logger.Success("%s@%s installed successfully", installName, asset.Tag)
	return nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// validName rejects install names that are not a safe single path segment.
func validName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid package name %q: must match %s", name, namePattern)
	}
	return nil
}

// tagDir returns the directory name for a release tag.
func tagDir(tag string) (string, error) {
	if tag == "" || tag == "." || tag == ".." {
		return "", fmt.Errorf("invalid release tag %q", tag)
	}
	return url.PathEscape(tag), nil
}

func tagOrLatest(tag string) string {
	if tag == "" {
		return "latest"
	}
	return tag
}

// Update updates an installed package. A non-empty asset or bin replaces the
// stored override.
func (i *Installer) Update(ctx context.Context, name, asset, bin string) error {
	unlock, err := i.storage.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	inst, err := i.storage.Get(name)
	if err != nil {
		return fmt.Errorf("package not found: %s", name)
	}

	opts := InstallOptions{Repo: inst.Repo, Force: true, Asset: asset, Bin: bin}
	if repo, err := ParseRepo(inst.Repo); err == nil && repo.Name != name {
		opts.Alias = name
	}

	return withRetry(i.install(ctx, opts), "grip update "+shellArg(name), opts)
}

// downloadAndUnpack downloads an asset archive and unpacks it.
// Returns the path to the extracted executable and a cleanup function.
// The caller is responsible for calling cleanup when done.
func (i *Installer) downloadAndUnpack(ctx context.Context, asset *Asset) (string, func(), error) {
	ws, err := NewWorkspace(i.config.TempDir, asset.Name)
	if err != nil {
		return "", nil, fmt.Errorf("create workspace: %w", err)
	}
	cleanup := func() {
		if cleanupErr := ws.Cleanup(); cleanupErr != nil {
			logger.Error("Failed to cleanup workspace: %v", cleanupErr)
		}
	}

	if err := Download(ctx, i.httpClient, asset.DownloadURL, ws.DownloadDir(), asset.Name); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("download: %w", err)
	}
	if err := i.stage("downloaded"); err != nil {
		cleanup()
		return "", nil, err
	}

	archivePath := filepath.Join(ws.DownloadDir(), asset.Name)
	binPath, err := Unpack(archivePath, ws.UnpackDir(), binaryQuery{
		OS:       asset.OS,
		Arch:     asset.Arch,
		Override: asset.BinOverride,
		Names:    []string{asset.BinaryName(), asset.RepoName},
		AnyArch:  asset.AnyArch,
	})
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("unpack: %w", err)
	}
	if err := i.stage("unpacked"); err != nil {
		cleanup()
		return "", nil, err
	}

	return binPath, cleanup, nil
}

// stage runs the test fault hook at a named step; nil in production.
func (i *Installer) stage(name string) error {
	if i.faultHook == nil {
		return nil
	}
	return i.faultHook(name)
}

func (i *Installer) pkgDir(name string) string {
	return filepath.Join(i.config.HomeDir, "pkgs", name)
}

// installAsset downloads the asset, writes its binary to storeDir/name and
// switches bin/name to it. It returns the SHA256 of the stored binary.
func (i *Installer) installAsset(ctx context.Context, asset *Asset, storeDir, name string) (string, error) {
	binPath, cleanup, err := i.downloadAndUnpack(ctx, asset)
	if err != nil {
		return "", err
	}
	defer cleanup()

	_, statErr := os.Stat(storeDir)
	created := errors.Is(statErr, os.ErrNotExist)
	sum, err := i.storeAndSwitch(binPath, storeDir, name)
	if err != nil && created {
		_ = os.RemoveAll(storeDir)
	}
	return sum, err
}

func (i *Installer) storeAndSwitch(binPath, storeDir, name string) (string, error) {
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return "", fmt.Errorf("create store dir: %w", err)
	}
	if err := storeBinary(binPath, storeDir, name, func() error { return i.stage("copying") }); err != nil {
		return "", err
	}
	if err := i.stage("stored"); err != nil {
		return "", err
	}
	storePath := filepath.Join(storeDir, name)
	sum, err := calculateFileSHA256(storePath)
	if err != nil {
		return "", fmt.Errorf("hash binary: %w", err)
	}

	if err := os.MkdirAll(i.config.BinDir, 0o755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}
	target, err := filepath.Rel(i.config.BinDir, storePath)
	if err != nil {
		return "", err
	}
	if err := switchLink(target, filepath.Join(i.config.BinDir, name)); err != nil {
		return "", err
	}
	return sum, nil
}

// cleanStore deletes everything in the package's store dir except the
// current and previous tag directories.
func (i *Installer) cleanStore(inst *Installation) {
	keep := map[string]bool{}
	for _, v := range []*Version{&inst.Version, inst.Previous} {
		if v == nil {
			continue
		}
		if d, err := tagDir(v.Tag); err == nil {
			keep[d] = true
		}
	}

	pkgDir := i.pkgDir(inst.Name)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		logger.Warn("Could not clean %s: %v", pkgDir, err)
		return
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(pkgDir, e.Name())); err != nil {
			logger.Warn("Could not clean %s: %v", e.Name(), err)
		}
	}
}

// Remove removes an installed package
func (i *Installer) Remove(ctx context.Context, name string) error {
	unlock, err := i.storage.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	inst, err := i.storage.Get(name)
	if err != nil {
		return fmt.Errorf("package not found: %s", name)
	}

	// Delete binary
	binPath := filepath.Join(i.storage.InstallDir(inst), name)
	if err := os.Remove(binPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove binary: %w", err)
	}
	if err := i.stage("unlinked"); err != nil {
		return err
	}

	// Remove from storage
	if err := i.storage.Delete(name); err != nil {
		return fmt.Errorf("remove from storage: %w", err)
	}
	if err := i.stage("deleted"); err != nil {
		return err
	}

	if err := os.RemoveAll(i.pkgDir(name)); err != nil {
		return fmt.Errorf("remove store: %w", err)
	}

	logger.Success("%s removed successfully", name)
	return nil
}
