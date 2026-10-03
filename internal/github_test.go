package grip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newGitHubTestSource(t *testing.T, h http.HandlerFunc) *GitHubSource {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &GitHubSource{baseURL: srv.URL, client: srv.Client()}
}

func TestGitHubSourceRelease(t *testing.T) {
	const body = `{"tag_name":"v1.2.0","assets":[
		{"name":"a.tar.gz","browser_download_url":"https://dl/a.tar.gz","size":42,"digest":"sha256:abc"},
		{"name":"b.tar.gz","browser_download_url":"https://dl/b.tar.gz","size":7,"digest":null},
		{"name":"c.tar.gz","browser_download_url":"https://dl/c.tar.gz","size":1}]}`
	var paths []string
	src := newGitHubTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") == "" {
			t.Errorf("missing API headers: %v", r.Header)
		}
		_, _ = w.Write([]byte(body))
	})
	repo := Repo{Host: "github.com", Owner: "o", Name: "r"}

	latest, err := src.LatestRelease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	byTag, err := src.ReleaseByTag(context.Background(), repo, "kustomize/v5.8.2")
	if err != nil {
		t.Fatal(err)
	}

	want := []ReleaseAsset{
		{Name: "a.tar.gz", URL: "https://dl/a.tar.gz", Size: 42, Digest: "sha256:abc"},
		{Name: "b.tar.gz", URL: "https://dl/b.tar.gz", Size: 7},
		{Name: "c.tar.gz", URL: "https://dl/c.tar.gz", Size: 1},
	}
	for _, rel := range []*Release{latest, byTag} {
		if rel.Tag != "v1.2.0" || len(rel.Assets) != len(want) {
			t.Fatalf("release = %+v", rel)
		}
		for i, a := range rel.Assets {
			if a != want[i] {
				t.Errorf("asset %d = %+v, want %+v", i, a, want[i])
			}
		}
	}
	wantPaths := []string{"/repos/o/r/releases/latest", "/repos/o/r/releases/tags/kustomize/v5.8.2"}
	if strings.Join(paths, " ") != strings.Join(wantPaths, " ") {
		t.Errorf("paths = %v, want %v", paths, wantPaths)
	}
}

func TestGitHubSourceErrors(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
		want   []string
	}{
		"not found":  {404, `{"message":"Not Found"}`, []string{"404", "Not Found"}},
		"rate limit": {403, `{"message":"API rate limit exceeded for 1.2.3.4."}`, []string{"403", "API rate limit exceeded"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			src := newGitHubTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := src.LatestRelease(context.Background(), Repo{Owner: "o", Name: "r"})
			if err == nil {
				t.Fatal("expected error")
			}
			for _, s := range tt.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not contain %q", err, s)
				}
			}
		})
	}
}
