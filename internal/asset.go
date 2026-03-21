package grip

import (
	"fmt"
	"strings"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/google/go-github/v56/github"
)

// Asset describes a release asset (pure data structure)
type Asset struct {
	Name        string
	Alias       string
	OS          string
	Arch        string
	DownloadURL string
	Tag         string
	RepoName    string
	RepoOwner   string
}

// BinaryName returns the name for the installed binary
func (a *Asset) BinaryName() string {
	if a.Alias != "" {
		return a.Alias
	}
	if a.RepoName != "" {
		return a.RepoName
	}
	// Fallback: extract name from asset filename
	name := strings.ToLower(a.Name)
	for ext := range map[string]bool{
		".tar.gz": true, ".tar.bz2": true, ".tbz": true,
		".zip": true, ".tar.xz": true, ".bz2": true,
	} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// parseAsset selects the appropriate asset for the platform
func parseAsset(assets []*github.ReleaseAsset, cfg *Config, repoOwner, repoName string) (*Asset, error) {
	logger.Info("Parsing %d release assets for %s_%s", len(assets), cfg.OS, cfg.Arch)

	for _, a := range assets {
		name := strings.ToLower(*a.Name)
		logger.Info("Evaluating asset: %s", name)

		if MatchesPlatform(name, cfg.OS, cfg.Arch, cfg.OSAliases, cfg.ArchAliases) && IsSupportedFormat(name) {
			logger.Info("Found compatible asset: %s", name)
			return &Asset{
				Name:        name,
				OS:          cfg.OS,
				Arch:        cfg.Arch,
				DownloadURL: *a.BrowserDownloadURL,
				RepoOwner:   repoOwner,
				RepoName:    repoName,
			}, nil
		}
	}

	return nil, fmt.Errorf("no asset found for %s_%s", cfg.OS, cfg.Arch)
}


