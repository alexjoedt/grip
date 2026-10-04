package grip

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const githubAPI = "https://api.github.com"

// GitHubSource implements Source against the GitHub REST API.
type GitHubSource struct {
	baseURL string
	client  *http.Client
	token   string
}

// NewGitHubSource creates a GitHub source. A non-empty token is sent as a
// bearer token with every API request.
func NewGitHubSource(token string) *GitHubSource {
	return &GitHubSource{baseURL: githubAPI, client: &http.Client{Timeout: 30 * time.Second}, token: token}
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
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

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
		switch {
		case resp.StatusCode == http.StatusUnauthorized && g.token != "":
			return nil, fmt.Errorf("GET %s: %s: GITHUB_TOKEN was rejected, fix or unset it", u, resp.Status)
		case (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
			resp.Header.Get("X-RateLimit-Remaining") == "0":
			hint := ""
			if g.token == "" {
				hint = ", set GITHUB_TOKEN to raise the limit"
			}
			return nil, fmt.Errorf("GET %s: %s: %w until %s%s", u, resp.Status, ErrRateLimited, rateLimitReset(resp.Header.Get("X-RateLimit-Reset")), hint)
		}
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

// rateLimitReset formats the X-RateLimit-Reset epoch seconds as local time.
func rateLimitReset(v string) string {
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return "an unknown time"
	}
	return time.Unix(sec, 0).Format("15:04:05 MST")
}
