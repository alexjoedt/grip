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
	"github.com/k0kubun/go-ansi"
	"github.com/minio/selfupdate"
	"github.com/schollz/progressbar/v3"
)

const (
	repository = "github.com/alexjoedt/grip"
)

var (
	currentOS   = runtime.GOOS
	currentArch = runtime.GOARCH

	// osAliases common aliases used in release packages
	osAliases = map[string][]string{
		"darwin": {"macos"},
		"linux":  {"musl"},
	}

	// archAliases common aliases used in release packages
	archAliases = map[string][]string{
		"amd64": {"x86_64"},
		"arm64": {"aarch64", "universal"},
	}
)

func SelfUpdate(ctx context.Context, version string) error {
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
