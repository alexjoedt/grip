package grip

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
)

type fakeSource struct {
	release *Release
	err     error
}

func (f fakeSource) LatestRelease(context.Context, Repo) (*Release, error) {
	return f.release, f.err
}

func (f fakeSource) ReleaseByTag(context.Context, Repo, string) (*Release, error) {
	return f.release, f.err
}

// countingSource counts forge requests.
type countingSource struct {
	Source
	calls atomic.Int64
}

func (c *countingSource) LatestRelease(ctx context.Context, r Repo) (*Release, error) {
	c.calls.Add(1)
	return c.Source.LatestRelease(ctx, r)
}

func (c *countingSource) ReleaseByTag(ctx context.Context, r Repo, tag string) (*Release, error) {
	c.calls.Add(1)
	return c.Source.ReleaseByTag(ctx, r, tag)
}

func TestInstallRejectsMalformedRelease(t *testing.T) {
	tests := map[string]*Release{
		"nil release":        nil,
		"missing tag":        {Assets: []ReleaseAsset{{Name: "a.tar.gz", URL: "https://x/a.tar.gz"}}},
		"asset without name": {Tag: "v1", Assets: []ReleaseAsset{{URL: "https://x/a.tar.gz"}}},
		"asset without URL":  {Tag: "v1", Assets: []ReleaseAsset{{Name: "a.tar.gz"}}},
	}
	for name, rel := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := &Config{HomeDir: dir, BinDir: filepath.Join(dir, "bin")}
			storage, err := NewStorage(filepath.Join(dir, "grip.json"), cfg)
			if err != nil {
				t.Fatal(err)
			}
			inst := NewInstaller(cfg, storage, fakeSource{release: rel}, nil)
			err = inst.Install(context.Background(), InstallOptions{Repo: "owner/grip-test-nonexistent"})
			if !errors.Is(err, ErrInvalidAsset) {
				t.Fatalf("Install err = %v, want ErrInvalidAsset", err)
			}
		})
	}
}
