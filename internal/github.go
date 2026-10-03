package grip

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const githubAPI = "https://api.github.com"

// GitHubSource implements Source against the GitHub REST API.
type GitHubSource struct {
	baseURL string
	client  *http.Client
}

// NewGitHubSource creates a GitHub source.
func NewGitHubSource() *GitHubSource {
	return &GitHubSource{baseURL: githubAPI, client: &http.Client{Timeout: 30 * time.Second}}
}

// LatestRelease fetches the latest release.
func (g *GitHubSource) LatestRelease(ctx context.Context, repo Repo) (*Release, error) {
	return g.release(ctx, "repos", repo.Owner, repo.Name, "releases", "latest")
}

// ReleaseByTag fetches a specific release by tag.
func (g *GitHubSource) ReleaseByTag(ctx context.Context, repo Repo, tag string) (*Release, error) {
	return g.release(ctx, "repos", repo.Owner, repo.Name, "releases", "tags", tag)
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
		Digest             string `json:"digest"`
	} `json:"assets"`
}

func (g *GitHubSource) release(ctx context.Context, path ...string) (*Release, error) {
	u, err := url.JoinPath(g.baseURL, path...)
	if err != nil {
		return nil, fmt.Errorf("build release URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return nil, fmt.Errorf("GET %s: %s: %s", u, resp.Status, apiErr.Message)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release from %s: %w", u, err)
	}
	r := &Release{Tag: rel.TagName}
	for _, a := range rel.Assets {
		r.Assets = append(r.Assets, ReleaseAsset{
			Name:   a.Name,
			URL:    a.BrowserDownloadURL,
			Size:   a.Size,
			Digest: a.Digest,
		})
	}
	return r, nil
}
