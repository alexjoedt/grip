package grip

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/alexjoedt/grip/internal/logger"
)

// Installer coordinates installation operations
type Installer struct {
	config     *Config
	storage    *Storage
	source     Source
	httpClient *http.Client
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
}

// Install installs a package from GitHub
func (i *Installer) Install(ctx context.Context, opts InstallOptions) error {
	unlock, err := i.storage.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return i.install(ctx, opts)
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

	// Check if name conflicts with another source
	if _, err := exec.LookPath(installName); err == nil && existing == nil {
		return fmt.Errorf("%s is already installed from another source", installName)
	}

	// Fetch release
	logger.Info("Fetching release %s for %s", tagOrLatest(opts.Tag), repo)
	release, err := fetchRelease(ctx, i.source, repo, opts.Tag)
	if err != nil {
		return err
	}

	if _, err := tagDir(release.Tag); err != nil {
		return err
	}

	// Parse asset for current platform
	asset, err := parseAsset(release.Assets, i.config, owner, name)
	if err != nil {
		return err
	}

	asset.Tag = release.Tag
	asset.Alias = opts.Alias

	// Install asset
	if err := i.installAsset(ctx, asset); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	// Calculate SHA256 of installed binary
	binPath := filepath.Join(i.config.BinDir, installName)
	sha256Hash, err := calculateFileSHA256(binPath)
	if err != nil {
		logger.Warn("Could not calculate SHA256: %v", err)
	}

	// Save to storage
	inst := &Installation{
		Name: installName,
		Repo: repo.String(),
		Version: Version{
			Tag:         asset.Tag,
			Asset:       asset.Name,
			SHA256:      sha256Hash,
			InstalledAt: time.Now(),
		},
	}

	if err := i.storage.Save(inst); err != nil {
		return fmt.Errorf("save installation: %w", err)
	}

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

// Update updates an installed package
func (i *Installer) Update(ctx context.Context, name string) error {
	unlock, err := i.storage.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	inst, err := i.storage.Get(name)
	if err != nil {
		return fmt.Errorf("package not found: %s", name)
	}

	opts := InstallOptions{Repo: inst.Repo, Force: true}
	if repo, err := ParseRepo(inst.Repo); err == nil && repo.Name != name {
		opts.Alias = name
	}

	return i.install(ctx, opts)
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

	archivePath := filepath.Join(ws.DownloadDir(), asset.Name)
	binPath, err := Unpack(archivePath, ws.UnpackDir())
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("unpack: %w", err)
	}

	return binPath, cleanup, nil
}

// installAsset orchestrates the complete installation workflow for an asset.
func (i *Installer) installAsset(ctx context.Context, asset *Asset) error {
	binPath, cleanup, err := i.downloadAndUnpack(ctx, asset)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := InstallBinary(binPath, i.config.BinDir, asset.BinaryName()); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	return nil
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

	// Remove from storage
	if err := i.storage.Delete(name); err != nil {
		return fmt.Errorf("remove from storage: %w", err)
	}

	logger.Success("%s removed successfully", name)
	return nil
}
