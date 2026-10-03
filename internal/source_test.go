package grip

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type fakeSource struct {
	release *Release
}

func (f fakeSource) LatestRelease(context.Context, Repo) (*Release, error) {
	return f.release, nil
}

func (f fakeSource) ReleaseByTag(context.Context, Repo, string) (*Release, error) {
	return f.release, nil
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
