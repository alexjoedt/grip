package grip

import (
	"context"

	"github.com/google/go-github/v56/github"
)

// GitHubClientImpl implements GitHubClient using real GitHub API
type GitHubClientImpl struct {
	client *github.Client
}

// NewGitHubClient creates a new GitHub client
func NewGitHubClient() *GitHubClientImpl {
	return &GitHubClientImpl{
		client: github.NewClient(nil),
	}
}

// GetLatestRelease fetches the latest release
func (g *GitHubClientImpl) GetLatestRelease(ctx context.Context, owner, repo string) (*github.RepositoryRelease, error) {
	release, _, err := g.client.Repositories.GetLatestRelease(ctx, owner, repo)
	return release, err
}

// GetReleaseByTag fetches a specific release by tag
func (g *GitHubClientImpl) GetReleaseByTag(ctx context.Context, owner, repo, tag string) (*github.RepositoryRelease, error) {
	release, _, err := g.client.Repositories.GetReleaseByTag(ctx, owner, repo, tag)
	return release, err
}
