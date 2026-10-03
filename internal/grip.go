package grip

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/alexjoedt/grip/internal/semver"
)

const (
	repository = "github.com/alexjoedt/grip"
)

// SelfUpdate replaces the running grip with the latest release. A grip
// installed by grip is updated through its state entry instead.
func SelfUpdate(ctx context.Context, version string, installer *Installer) error {
	if installer == nil {
		return fmt.Errorf("installer is required")
	}
	if installer.source == nil {
		return fmt.Errorf("installer: source is required")
	}
	if installer.config == nil {
		return fmt.Errorf("installer: config is required")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	return installer.selfUpdate(ctx, version, exe)
}

// selfUpdate updates the grip executable at exe, a symlink-resolved path.
func (i *Installer) selfUpdate(ctx context.Context, version, exe string) error {
	if name, ok := i.storeName(exe); ok {
		logger.Info("grip is installed by grip as %s, updating that package", name)
		return i.update(ctx, name, "", "", true)
	}

	repo, err := ParseRepo(repository)
	if err != nil {
		return err
	}

	release, err := fetchRelease(ctx, i.source, repo, "")
	if err != nil {
		return err
	}

	// Only check for newer version if current version is defined
	if version != "" && version != "undefined" {
		latestVersion, err := semver.Parse(release.Tag)
		if err != nil {
			return err
		}
		currentVersion, err := semver.Parse(version)
		if err != nil {
			return err
		}
		if semver.Compare(currentVersion, latestVersion) >= 0 {
			logger.Info("Newest version already installed")
			return nil
		}
	}

	asset, err := parseAsset(release.Assets, i.config, repo.Owner, repo.Name, "")
	if err != nil {
		return err
	}
	asset.Tag = release.Tag
	asset.requireDigest = true

	binPath, _, cleanup, err := i.downloadAndUnpack(ctx, asset, "")
	if err != nil {
		return err
	}
	defer cleanup()

	dir := filepath.Dir(exe)
	if err := storeBinary(binPath, dir, filepath.Base(exe), func() error { return i.stage("copying") }); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("replace %s: %w; rerun with permission to write %s", exe, err, dir)
		}
		return fmt.Errorf("replace %s: %w", exe, err)
	}

	logger.Success("Grip updated successfully to %s", asset.Tag)
	return nil
}

// storeName returns the package name when exe lies in grip's store.
func (i *Installer) storeName(exe string) (string, bool) {
	pkgs := filepath.Join(i.config.HomeDir, "pkgs")
	if resolved, err := filepath.EvalSymlinks(pkgs); err == nil {
		pkgs = resolved
	}
	rel, err := filepath.Rel(pkgs, exe)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	name, _, ok := strings.Cut(rel, string(filepath.Separator))
	return name, ok
}
