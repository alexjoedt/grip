package grip

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRepo(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{in: "owner/repo"},
		{in: "github.com/owner/repo"},
		{in: "https://github.com/owner/repo"},
		{in: "https://github.com/owner/repo.git"},
		{in: "https://github.com/owner/repo/"},
		{in: "github.com/owner/repo/"},
		{in: "  owner/repo  "},
		{in: "", wantErr: true},
		{in: "repo", wantErr: true},
		{in: "/repo", wantErr: true},
		{in: "owner/", wantErr: true},
		{in: "github.com/owner", wantErr: true},
		{in: "https://github.com/owner", wantErr: true},
		{in: "github.com/owner/repo/extra", wantErr: true},
		{in: "https://github.com/owner/repo/releases", wantErr: true},
		{in: "gitlab.com/owner/repo", wantErr: true},
		{in: "https://gitlab.com/owner/repo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRepo(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidRepo) {
					t.Fatalf("ParseRepo(%q) err = %v, want ErrInvalidRepo", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRepo(%q) err = %v", tt.in, err)
			}
			if got.String() != "github.com/owner/repo" {
				t.Fatalf("ParseRepo(%q) = %q, want github.com/owner/repo", tt.in, got)
			}
		})
	}
}

func TestInstallAlreadyInstalledAnySpelling(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{HomeDir: dir, BinDir: filepath.Join(dir, "bin")}
	storage, err := NewStorage(filepath.Join(dir, "grip.json"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// v1 recorded the raw user string.
	if err := storage.Save(&Installation{Name: "repo", Repo: "https://github.com/owner/repo.git", Version: Version{Tag: "v1.0.0"}}); err != nil {
		t.Fatal(err)
	}

	// A nil source panics on any forge request.
	inst := NewInstaller(cfg, storage, nil, nil)
	for _, ref := range []string{"owner/repo", "github.com/owner/repo", "https://github.com/owner/repo/"} {
		if err := inst.Install(context.Background(), InstallOptions{Repo: ref}); err != nil {
			t.Errorf("Install(%q) err = %v, want no-op", ref, err)
		}
	}
	if err := inst.Install(context.Background(), InstallOptions{Repo: "owner/repo", Tag: "v0.9.0"}); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Errorf("Install at another tag err = %v, want already installed", err)
	}
}
