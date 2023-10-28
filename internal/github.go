package grip

import (
	"context"
	"log"
)

func GetLatest(owner, repo string) (*Asset, error) {
	release, _, err := ghClient.Repositories.GetLatestRelease(context.Background(), owner, repo)
	if err != nil {
		log.Fatal(err)
	}
	asset, err := parseAsset(release.Assets)
	if err != nil {
		return nil, err
	}

	asset.repoOwner = owner
	asset.repoName = repo
	asset.Tag = *release.TagName

	return asset, nil
}

func GetByTag(owner, repo, tag string) (*Asset, error) {
	release, _, err := ghClient.Repositories.GetReleaseByTag(context.Background(), owner, repo, tag)
	if err != nil {
		log.Fatal(err)
	}
	asset, err := parseAsset(release.Assets)
	if err != nil {
		return nil, err
	}

	asset.repoOwner = owner
	asset.repoName = repo
	asset.Tag = *release.TagName

	return asset, nil
}
