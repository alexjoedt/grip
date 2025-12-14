package grip

import (
	"context"

	"github.com/alexjoedt/grip/internal/logger"
)

func GetLatest(owner, repo string) (*Asset, error) {
	logger.Info("Fetching latest release for %s/%s", owner, repo)
	release, _, err := ghClient.Repositories.GetLatestRelease(context.Background(), owner, repo)
	if err != nil {
		logger.Fatal("Failed to fetch latest release for %s/%s: %v", owner, repo, err)
	}
	asset, err := parseAsset(release.Assets)
	if err != nil {
		return nil, err
	}

	asset.repoOwner = owner
	asset.repoName = repo
	asset.Tag = *release.TagName

	logger.Info("Found latest release: %s", asset.Tag)
	return asset, nil
}

func GetByTag(owner, repo, tag string) (*Asset, error) {
	logger.Info("Fetching release %s for %s/%s", tag, owner, repo)
	release, _, err := ghClient.Repositories.GetReleaseByTag(context.Background(), owner, repo, tag)
	if err != nil {
		logger.Fatal("Failed to fetch release %s for %s/%s: %v", tag, owner, repo, err)
	}
	asset, err := parseAsset(release.Assets)
	if err != nil {
		return nil, err
	}

	asset.repoOwner = owner
	asset.repoName = repo
	asset.Tag = *release.TagName

	logger.Info("Found release: %s", asset.Tag)
	return asset, nil
}
