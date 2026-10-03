package grip

import (
	"context"
	"fmt"
)

// Release is a forge release in grip's own terms.
type Release struct {
	Tag    string
	Assets []ReleaseAsset
}

// ReleaseAsset is a downloadable file attached to a release.
type ReleaseAsset struct {
	Name   string
	URL    string
	Size   int64
	Digest string // "<algo>:<hex>", empty when the forge publishes none
}

// Source fetches releases from a forge.
type Source interface {
	LatestRelease(ctx context.Context, repo Repo) (*Release, error)
	ReleaseByTag(ctx context.Context, repo Repo, tag string) (*Release, error)
}

func (r *Release) validate() error {
	if r == nil || r.Tag == "" {
		return fmt.Errorf("%w: release without tag", ErrInvalidAsset)
	}
	for i, a := range r.Assets {
		if a.Name == "" || a.URL == "" {
			return fmt.Errorf("%w: asset %d of %s without name or URL", ErrInvalidAsset, i, r.Tag)
		}
	}
	return nil
}

func fetchRelease(ctx context.Context, src Source, repo Repo, tag string) (*Release, error) {
	var (
		release *Release
		err     error
	)
	if tag == "" {
		release, err = src.LatestRelease(ctx, repo)
	} else {
		release, err = src.ReleaseByTag(ctx, repo, tag)
	}
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	if err := release.validate(); err != nil {
		return nil, err
	}
	return release, nil
}
