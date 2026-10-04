package grip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexjoedt/grip/internal/logger"
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
	const token = "secret-token-zz"
	reset := time.Unix(1_900_000_000, 0).Format("15:04:05 MST")
	limited := map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1900000000"}
	tests := map[string]struct {
		status      int
		header      map[string]string
		token       string
		rateLimited bool
		want        []string
		notWant     []string
	}{
		"not found":         {status: 404, want: []string{"404", "Not Found"}},
		"forbidden":         {status: 403, header: map[string]string{"X-RateLimit-Remaining": "12"}, want: []string{"403", "Not Found"}},
		"rejected token":    {status: 401, token: token, want: []string{"401", "GITHUB_TOKEN was rejected"}},
		"rate limit":        {status: 403, header: limited, rateLimited: true, want: []string{"403", reset, "set GITHUB_TOKEN"}},
		"secondary limit":   {status: 429, header: limited, token: token, rateLimited: true, want: []string{"429", reset}, notWant: []string{"set GITHUB_TOKEN"}},
		"limit, bad header": {status: 403, header: map[string]string{"X-RateLimit-Remaining": "0"}, rateLimited: true, want: []string{"unknown time"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			src := newGitHubTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				for k, v := range tt.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			})
			src.token = tt.token
			_, err := src.LatestRelease(context.Background(), Repo{Owner: "o", Name: "r"})
			if err == nil {
				t.Fatal("expected error")
			}
			if errors.Is(err, ErrRateLimited) != tt.rateLimited {
				t.Errorf("errors.Is(%q, ErrRateLimited) = %v, want %v", err, !tt.rateLimited, tt.rateLimited)
			}
			for _, s := range tt.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not contain %q", err, s)
				}
			}
			for _, s := range append(tt.notWant, token) {
				if strings.Contains(err.Error(), s) {
					t.Errorf("error %q contains %q", err, s)
				}
			}
			if calls.Load() != 1 {
				t.Errorf("sent %d requests, want 1", calls.Load())
			}
		})
	}
}

func TestGitHubSourceToken(t *testing.T) {
	for _, token := range []string{"", "secret-token-zz"} {
		src := newGitHubTestSource(t, func(w http.ResponseWriter, r *http.Request) {
			want := ""
			if token != "" {
				want = "Bearer " + token
			}
			if got := r.Header.Get("Authorization"); got != want {
				t.Errorf("token %q: Authorization = %q, want %q", token, got, want)
			}
			_, _ = w.Write([]byte(`{"tag_name":"v1"}`))
		})
		src.token = token
		if _, err := src.LatestRelease(context.Background(), Repo{Owner: "o", Name: "r"}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGitHubTokenStaysOnAPI installs through the API with a token and verbose
// output, and checks that neither the asset download nor the output carry it.
// It swaps os.Stdout and os.Stderr, so it must not run in parallel.
func TestGitHubTokenStaysOnAPI(t *testing.T) {
	const token = "secret-token-zz"
	e := newInstallerEnv(t)
	var assetAuth atomic.Value
	assetAuth.Store("")
	assets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write(createTestTarGz(t))
	}))
	t.Cleanup(assets.Close)
	src := newGitHubTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[{"name":"tool_%s_%s.tar.gz","browser_download_url":%q}]}`,
			e.cfg.OS, e.cfg.Arch, assets.URL+"/tool.tar.gz")
	})
	src.token = token

	logger.SetVerbose(true)
	t.Cleanup(func() { logger.SetVerbose(false) })
	var err error
	out := captureOutput(t, func() {
		err = NewInstaller(e.cfg, e.storage, src, assets.Client()).Install(context.Background(), InstallOptions{Repo: fixtureRepo})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := assetAuth.Load(); got != "" {
		t.Errorf("asset download sent Authorization %q", got)
	}
	if !strings.Contains(out, "[INFO]") {
		t.Errorf("verbose output missing: %q", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("output contains the token: %q", out)
	}
}

// captureOutput returns everything f writes to os.Stdout and os.Stderr.
func captureOutput(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	f()
	_ = w.Close()
	return string(<-done)
}
