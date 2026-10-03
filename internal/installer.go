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
	"slices"
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
	Repo  string // package reference, optionally with @tag
	Tag   string // explicit tag, pins the package; split from Repo by Install
	Force bool
	Alias string
	Asset string // asset name or path.Match pattern, remembered for updates
	Bin   string // base name of the executable in the archive, remembered

	requireDigest bool
	release       *Release // already fetched, skips the forge request
}

// Install installs a package from GitHub
func (i *Installer) Install(ctx context.Context, opts InstallOptions) error {
	unlock, err := i.storage.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	if ref, tag, ok := strings.Cut(opts.Repo, "@"); ok {
		if tag == "" {
			return fmt.Errorf("empty tag in %q", opts.Repo)
		}
		opts.Repo, opts.Tag = ref, tag
	}

	ref := opts.Repo
	if opts.Tag != "" {
		ref += "@" + opts.Tag
	}
	cmd := "grip install " + shellArg(ref)
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
		switch {
		case opts.Force:
		case opts.Tag == "":
			logger.Println("%s %s is already installed, update it with: grip update %s", existing.Name, existing.Tag, existing.Name)
			return nil
		case opts.Tag == existing.Tag:
			logger.Println("%s %s is already installed", existing.Name, existing.Tag)
			if existing.Pinned {
				return nil
			}
			existing.Pinned = true
			if err := i.storage.Save(existing); err != nil {
				return fmt.Errorf("save installation: %w", err)
			}
			logPinned(existing)
			return nil
		}
		if existing.Pinned && opts.Tag == "" {
			opts.Tag = existing.Tag
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

	release := opts.release
	if release == nil {
		logger.Info("Fetching release %s for %s", tagOrLatest(opts.Tag), repo)
		if release, err = fetchRelease(ctx, i.source, repo, opts.Tag); err != nil {
			return err
		}
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
	asset.requireDigest = opts.requireDigest
	if opts.Asset == asset.Name {
		assetOverride = versionGlob(asset.Name, release.Tag)
	}

	version, err := i.installAsset(ctx, asset, pinnedDigest(existing, asset.Tag, asset.Name), filepath.Join(i.pkgDir(installName), dir), installName)
	if errors.Is(err, ErrDigestChanged) {
		return fmt.Errorf("install: %w; to accept the new asset run grip remove %s, then install it again", err, installName)
	}
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if err := i.stage("switched"); err != nil {
		return err
	}

	version.InstalledAt = time.Now()
	inst := &Installation{
		Name:          installName,
		Repo:          repo.String(),
		Version:       version,
		AssetOverride: assetOverride,
		BinOverride:   binOverride,
	}
	inst.Pinned = opts.Tag != ""
	if existing != nil {
		inst.Pinned = inst.Pinned || existing.Pinned
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

	if existing != nil && existing.Tag != inst.Tag {
		logger.Success("%s updated from %s to %s", installName, existing.Tag, inst.Tag)
	} else {
		logger.Success("%s@%s installed successfully", installName, asset.Tag)
	}
	if opts.Tag != "" {
		logPinned(inst)
	}
	return nil
}

func logPinned(inst *Installation) {
	logger.Println("%s is pinned at %s, run grip unpin %s to follow the latest release", inst.Name, inst.Tag, inst.Name)
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
	return i.update(ctx, name, asset, bin, false)
}

func (i *Installer) update(ctx context.Context, name, asset, bin string, requireDigest bool) error {
	unlock, err := i.storage.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	inst, err := i.storage.Get(name)
	if err != nil {
		return fmt.Errorf("package not found: %s", name)
	}
	if inst.Pinned {
		return fmt.Errorf("%s is pinned at %s, run grip unpin %s", name, inst.Tag, name)
	}

	repo, err := ParseRepo(inst.Repo)
	if err != nil {
		return err
	}
	// "Newer" is tag inequality with the latest release, not semver order.
	logger.Info("Fetching release latest for %s", repo)
	release, err := fetchRelease(ctx, i.source, repo, "")
	if err != nil {
		return err
	}
	if release.Tag == inst.Tag && asset == "" && bin == "" {
		logger.Println("%s is already at %s", name, inst.Tag)
		return nil
	}

	opts := InstallOptions{Repo: inst.Repo, Force: true, Asset: asset, Bin: bin, requireDigest: requireDigest, release: release}
	if repo.Name != name {
		opts.Alias = name
	}

	return withRetry(i.install(ctx, opts), "grip update "+shellArg(name), opts)
}

// OutdatedPackage is an installed package whose tag differs from the latest
// release.
type OutdatedPackage struct {
	Name, Tag, Latest string
	Pinned            bool
}

// Outdated looks up the latest release of every installed package, sorted by
// name. A failed lookup is logged and the rest are still checked. It takes no
// lock and changes nothing.
func (i *Installer) Outdated(ctx context.Context) ([]OutdatedPackage, error) {
	insts, err := i.storage.List()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(insts, func(a, b *Installation) int { return strings.Compare(a.Name, b.Name) })

	var out []OutdatedPackage
	failed := 0
	for _, inst := range insts {
		release, err := func() (*Release, error) {
			repo, err := ParseRepo(inst.Repo)
			if err != nil {
				return nil, err
			}
			return fetchRelease(ctx, i.source, repo, "")
		}()
		if err != nil {
			logger.Error("%s: %v", inst.Name, err)
			failed++
			continue
		}
		if release.Tag != inst.Tag {
			out = append(out, OutdatedPackage{Name: inst.Name, Tag: inst.Tag, Latest: release.Tag, Pinned: inst.Pinned})
		}
	}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d lookups failed", failed, len(insts))
	}
	return out, nil
}

// pinnedDigest returns the digest recorded for tag and asset in the current
// or previous version, empty when none is recorded.
func pinnedDigest(existing *Installation, tag, asset string) string {
	if existing == nil {
		return ""
	}
	for _, v := range []*Version{&existing.Version, existing.Previous} {
		if v != nil && v.Tag == tag && v.Asset == asset {
			return v.AssetDigest
		}
	}
	return ""
}

// checkDigest verifies the downloaded hex sum against the published digest
// and a pinned digest recorded earlier. It returns the digest source to record
// and a warning when no sha256 digest was published, an error instead when
// the asset requires one.
func checkDigest(asset *Asset, sum, pinned string) (source, warning string, err error) {
	got := "sha256:" + sum
	algo, want, _ := strings.Cut(asset.Digest, ":")
	switch {
	case asset.Digest == "":
		source, warning = DigestSourceNone, fmt.Sprintf("%s has no published digest, recording %s", asset.Name, got)
	case !strings.EqualFold(algo, "sha256"):
		source, warning = DigestSourceNone, fmt.Sprintf("%s has an unsupported %s digest, recording %s", asset.Name, algo, got)
	case !strings.EqualFold(want, sum):
		return "", "", fmt.Errorf("%w for %s: expected %s, got %s", ErrDigestMismatch, asset.Name, asset.Digest, got)
	default:
		source = DigestSourceAPI
	}
	if source == DigestSourceNone && asset.requireDigest {
		return "", "", fmt.Errorf("%w for %s", ErrDigestMissing, asset.Name)
	}
	if pinned != "" && !strings.EqualFold(pinned, got) {
		return "", "", fmt.Errorf("%w for %s %s: recorded %s, downloaded %s", ErrDigestChanged, asset.Name, asset.Tag, pinned, got)
	}
	return source, warning, nil
}

// downloadAndUnpack downloads an asset, verifies its digest and unpacks it.
// It returns the path to the extracted executable, the version with tag,
// asset and digest filled, and a cleanup function the caller must call.
func (i *Installer) downloadAndUnpack(ctx context.Context, asset *Asset, pinned string) (string, Version, func(), error) {
	ws, err := NewWorkspace(i.config.TempDir, asset.Name)
	if err != nil {
		return "", Version{}, nil, fmt.Errorf("create workspace: %w", err)
	}
	cleanup := func() {
		if cleanupErr := ws.Cleanup(); cleanupErr != nil {
			logger.Error("Failed to cleanup workspace: %v", cleanupErr)
		}
	}

	sum, err := Download(ctx, i.httpClient, asset.DownloadURL, ws.DownloadDir(), asset.Name, asset.Size)
	if err != nil {
		cleanup()
		return "", Version{}, nil, fmt.Errorf("download: %w", err)
	}
	source, warning, err := checkDigest(asset, sum, pinned)
	if err != nil {
		cleanup()
		return "", Version{}, nil, err
	}
	if warning != "" {
		logger.Warn("%s", warning)
	}
	version := Version{Tag: asset.Tag, Asset: asset.Name, AssetDigest: "sha256:" + sum, DigestSource: source}
	if err := i.stage("downloaded"); err != nil {
		cleanup()
		return "", Version{}, nil, err
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
		return "", Version{}, nil, fmt.Errorf("unpack: %w", err)
	}
	if err := i.stage("unpacked"); err != nil {
		cleanup()
		return "", Version{}, nil, err
	}

	return binPath, version, cleanup, nil
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
// switches bin/name to it. It returns the installed version without
// InstalledAt.
func (i *Installer) installAsset(ctx context.Context, asset *Asset, pinned, storeDir, name string) (Version, error) {
	binPath, version, cleanup, err := i.downloadAndUnpack(ctx, asset, pinned)
	if err != nil {
		return Version{}, err
	}
	defer cleanup()

	_, statErr := os.Stat(storeDir)
	created := errors.Is(statErr, os.ErrNotExist)
	version.SHA256, err = i.storeAndSwitch(binPath, storeDir, name)
	if err != nil {
		if created {
			_ = os.RemoveAll(storeDir)
		}
		return Version{}, err
	}
	return version, nil
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
