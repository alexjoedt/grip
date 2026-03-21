package grip

import (
	"context"
	"fmt"
	"os"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/alexjoedt/grip/internal/semver"
	"github.com/k0kubun/go-ansi"
	"github.com/minio/selfupdate"
	"github.com/schollz/progressbar/v3"
)

const (
	repository = "github.com/alexjoedt/grip"
)

func SelfUpdate(ctx context.Context, version string, installer *Installer) error {
	if installer == nil {
		return fmt.Errorf("installer is required")
	}
	if installer.ghClient == nil {
		return fmt.Errorf("installer: GitHub client is required")
	}
	if installer.config == nil {
		return fmt.Errorf("installer: config is required")
	}
	if installer.httpClient == nil {
		return fmt.Errorf("installer: HTTP client is required")
	}

	owner, name, err := ParseRepoPath(repository)
	if err != nil {
		return err
	}

	// Fetch latest release
	release, err := installer.ghClient.GetLatestRelease(ctx, owner, name)
	if err != nil {
		return err
	}

	// Check if update is needed
	latestTag := *release.TagName
	latestVersion, err := semver.Parse(latestTag)
	if err != nil {
		return err
	}

	// Only check for newer version if current version is defined
	if version != "" && version != "undefined" {
		currentVersion, err := semver.Parse(version)
		if err != nil {
			return err
		}

		if semver.Compare(currentVersion, latestVersion) >= 0 {
			logger.Info("Newest version already installed")
			return nil
		}
	}

	// Parse asset for current platform
	asset, err := parseAsset(release.Assets, installer.config, owner, name)
	if err != nil {
		return err
	}
	asset.Tag = latestTag

	// Download and unpack using installer
	binPath, cleanup, err := installer.downloadAndUnpack(ctx, asset)
	if err != nil {
		return err
	}
	defer cleanup()

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
