package grip

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/alexjoedt/grip/internal/semver"
	"github.com/google/go-github/v56/github"
	"github.com/k0kubun/go-ansi"
	"github.com/minio/selfupdate"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/exp/slices"
)

const (
	repository string = "github.com/alexjoedt/grip"
)

var (
	// homePath, default: ~/.grip
	homePath string = ""

	// InstallPath is the path where the executables will be installed.
	// Must be in PATH
	InstallPath string = ""

	// lockFilepath holds the path to the lock file, where all installed executables
	// are indexed. The path will be determined in the init function.
	lockFilepath string = ""

	currentOS   string = runtime.GOOS
	currentArch string = runtime.GOARCH

	// osAliases common aliases used in release packages
	osAliases map[string][]string = map[string][]string{
		"darwin": {"macos"},
		"linux":  {"musl"},
	}

	// archAliases common aliases used in release packages
	archAliases map[string][]string = map[string][]string{
		"amd64": {"x86_64"},
		"arm64": {"aarch64", "universal"},
	}

	// unpacker unpack functions for common package types
	unpacker map[string]unpackFn = map[string]unpackFn{
		".tar.gz":  unpackTarGz,
		".tar.bz2": unpackTarBz2,
		".tbz":     unpackTarBz2,
		".zip":     unpackZip,
		".tar.xz":  unpackTarXz,
		".bz2":     unpackBz2,
	}

	// ghClient github api client
	ghClient *github.Client

	// httpClient
	httpClient *http.Client
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Fatal("No user home dir, please provide an install path")
	}

	homePath = filepath.Join(home, ".grip")

	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" && currentOS == "linux" {
		homePath = filepath.Join("/home", sudoUser, ".grip")
	}

	// TODO: read install path from config if config file exists
	InstallPath = filepath.Join(homePath, "bin")
	err = os.MkdirAll(InstallPath, 0755)
	if err != nil {
		logger.Error("Failed to create install path: %v", err)
	}

	lockFilepath = filepath.Join(homePath, "grip.lock")
	_, err = os.Stat(lockFilepath)
	if err != nil {
		_, err = os.Create(lockFilepath)
		if err != nil {
			logger.Error("Failed to create grip.lock: %v", err)
		}
	}

	ghClient = github.NewClient(nil)
	httpClient = &http.Client{
		Timeout: time.Second * 30,
	}
}

func CheckPathEnv() {
	pathEnv := os.Getenv("PATH")
	parts := filepath.SplitList(pathEnv)
	if !slices.Contains(parts, InstallPath) {
		logger.Warn("The grip path '%s' isn't in PATH", InstallPath)
	}
}

func SelfUpdate(version string) error {
	ctx := context.Background()
	
	currentVersion, err := semver.Parse(version)
	if err != nil {
		return err
	}

	owner, name, err := ParseRepoPath(repository)
	if err != nil {
		return err
	}

	// Fetch latest release
	ghClient := NewGitHubClient()
	release, err := ghClient.GetLatestRelease(ctx, owner, name)
	if err != nil {
		return err
	}

	// Check if update is needed
	latestTag := *release.TagName
	latestVersion, err := semver.Parse(latestTag)
	if err != nil {
		return err
	}

	if semver.Compare(currentVersion, latestVersion) >= 0 {
		logger.Info("Newest version already installed")
		return nil
	}

	// Parse asset for current platform
	cfg, err := DefaultConfig()
	if err != nil {
		return err
	}

	asset, err := parseAsset(release.Assets, cfg, owner, name)
	if err != nil {
		return err
	}
	asset.Tag = latestTag

	// Create workspace for download and unpack
	ws, err := NewWorkspace(cfg.TempDir, "grip-selfupdate")
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	defer ws.Cleanup()

	// Download
	downloader := NewDownloader(&http.Client{Timeout: 30 * time.Second})
	if err := downloader.Download(ctx, asset.DownloadURL, ws.DownloadDir(), asset.Name); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// Unpack
	unpacker := NewUnpacker()
	archivePath := filepath.Join(ws.DownloadDir(), asset.Name)
	binPath, err := unpacker.Unpack(archivePath, ws.UnpackDir())
	if err != nil {
		return fmt.Errorf("unpack: %w", err)
	}

	// Apply self-update
	reader, err := os.Open(binPath)
	if err != nil {
		return fmt.Errorf("open binary: %w", err)
	}
	defer reader.Close()

	if err := selfupdate.Apply(reader, selfupdate.Options{}); err != nil {
		return fmt.Errorf("apply update: %w", err)
	}

	logger.Success("Grip updated successfully to %s", asset.Tag)
	return nil
}

func NewProgressBar(size int, description string) *progressbar.ProgressBar {
	return progressbar.NewOptions(size,
		progressbar.OptionFullWidth(),
		progressbar.OptionSetWriter(ansi.NewAnsiStdout()),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))
}
