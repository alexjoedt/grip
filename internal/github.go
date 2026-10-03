package grip

import (
	"context"

	"github.com/google/go-github/v56/github"
)

// GitHubSource implements Source against the GitHub API.
type GitHubSource struct {
	client *github.Client
}

// NewGitHubSource creates a GitHub source.
func NewGitHubSource() *GitHubSource {
	return &GitHubSource{client: github.NewClient(nil)}
}

// LatestRelease fetches the latest release.
func (g *GitHubSource) LatestRelease(ctx context.Context, repo Repo) (*Release, error) {
	rel, _, err := g.client.Repositories.GetLatestRelease(ctx, repo.Owner, repo.Name)
	if err != nil {
		return nil, err
	}
	return toRelease(rel), nil
}

// ReleaseByTag fetches a specific release by tag.
func (g *GitHubSource) ReleaseByTag(ctx context.Context, repo Repo, tag string) (*Release, error) {
	rel, _, err := g.client.Repositories.GetReleaseByTag(ctx, repo.Owner, repo.Name, tag)
	if err != nil {
		return nil, err
	}
	return toRelease(rel), nil
}

func toRelease(rel *github.RepositoryRelease) *Release {
	r := &Release{Tag: rel.GetTagName()}
	for _, a := range rel.Assets {
		r.Assets = append(r.Assets, ReleaseAsset{
			Name: a.GetName(),
			URL:  a.GetBrowserDownloadURL(),
			Size: int64(a.GetSize()),
		})
	}
	return r
}
