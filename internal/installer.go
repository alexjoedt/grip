package grip

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/google/go-github/v56/github"
)

// GitHubClient interface for testing
type GitHubClient interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (*github.RepositoryRelease, error)
	GetReleaseByTag(ctx context.Context, owner, repo, tag string) (*github.RepositoryRelease, error)
}

// Installer coordinates installation operations
type Installer struct {
	config     *Config
	storage    *Storage
	ghClient   GitHubClient
	httpClient *http.Client
}

// NewInstaller creates a new installer
func NewInstaller(cfg *Config, storage *Storage, ghClient GitHubClient, httpClient *http.Client) *Installer {
	return &Installer{
		config:     cfg,
		storage:    storage,
		ghClient:   ghClient,
		httpClient: httpClient,
	}
}

// Config returns the installer's config (for lock file operations)
func (i *Installer) Config() *Config {
	return i.config
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
	owner, name, err := ParseRepoPath(opts.Repo)
	if err != nil {
		return err
	}

	// Use alias as name if provided
	installName := name
	if opts.Alias != "" {
		installName = opts.Alias
	}

	// Check if already installed
	existing, err := i.storage.GetByRepo(opts.Repo)
	if err == nil && !opts.Force {
		return fmt.Errorf("%s version %s is already installed", existing.Name, existing.Tag)
	}

	// Check if name conflicts with another source
	if _, err := exec.LookPath(installName); err == nil && existing == nil {
		return fmt.Errorf("%s is already installed from another source", installName)
	}

	// Fetch release
	var release *github.RepositoryRelease
	if opts.Tag == "" {
		logger.Info("Fetching latest release for %s/%s", owner, name)
		release, err = i.ghClient.GetLatestRelease(ctx, owner, name)
	} else {
		logger.Info("Fetching release %s for %s/%s", opts.Tag, owner, name)
		release, err = i.ghClient.GetReleaseByTag(ctx, owner, name, opts.Tag)
	}
	if err != nil {
		return fmt.Errorf("fetch release: %w", err)
	}

	// Parse asset for current platform
	asset, err := parseAsset(release.Assets, i.config, owner, name)
	if err != nil {
		return err
	}

	asset.Tag = *release.TagName
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
	now := time.Now()
	inst := &Installation{
		Name:        installName,
		Alias:       opts.Alias,
		Repo:        opts.Repo,
		Tag:         asset.Tag,
		SHA256:      sha256Hash,
		InstalledAt: now,
		UpdatedAt:   now,
		InstallPath: i.config.BinDir,
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

// Update updates an installed package
func (i *Installer) Update(ctx context.Context, name string) error {
	// Get current installation
	inst, err := i.storage.Get(name)
	if err != nil {
		return fmt.Errorf("package not found: %s", name)
	}

	// Install with force flag
	opts := InstallOptions{
		Repo:  inst.Repo,
		Tag:   "", // Get latest
		Force: true,
		Alias: inst.Alias,
	}

	return i.Install(ctx, opts)
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
func (i *Installer) Remove(name string) error {
	inst, err := i.storage.Get(name)
	if err != nil {
		return fmt.Errorf("package not found: %s", name)
	}

	// Delete binary
	binPath := filepath.Join(inst.InstallPath, name)
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
