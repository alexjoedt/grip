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

	inst := NewInstaller(cfg, storage, nil, nil)
	for _, ref := range []string{"owner/repo", "github.com/owner/repo", "https://github.com/owner/repo/"} {
		err := inst.Install(context.Background(), InstallOptions{Repo: ref})
		if err == nil || !strings.Contains(err.Error(), "already installed") {
			t.Errorf("Install(%q) err = %v, want already installed", ref, err)
		}
	}
}

func TestMatchesPlatform(t *testing.T) {
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		file, os, arch string
		want           bool
	}{
		{"tool_linux_amd64.tar.gz", "linux", "amd64", true},
		{"tool_darwin_arm64.tar.gz", "darwin", "arm64", true},
		{"Tool-Linux-AMD64.tar.gz", "linux", "amd64", true},
		{"tool_macos_arm64.tar.gz", "darwin", "arm64", true},
		{"tool_mac_amd64.zip", "darwin", "amd64", true},
		{"tool_x86_64-unknown-linux-musl.tar.gz", "linux", "amd64", true},
		{"tool_linux_x86_64.tar.gz", "linux", "amd64", true},
		{"tool_linux_aarch64.tar.gz", "linux", "arm64", true},
		{"tool_darwin_universal.tar.gz", "darwin", "arm64", true},
		{"tool_windows_amd64.zip", "linux", "amd64", false},
		{"tool_linux_amd64.tar.gz", "darwin", "amd64", false},
		{"tool_linux_amd64.tar.gz", "linux", "arm64", false},
		// Substring matching lets "arm" match "arm64"; scoring is epic 03.
	}
	for _, tt := range tests {
		got := MatchesPlatform(tt.file, tt.os, tt.arch, cfg.OSAliases, cfg.ArchAliases)
		if got != tt.want {
			t.Errorf("MatchesPlatform(%q, %s, %s) = %v, want %v", tt.file, tt.os, tt.arch, got, tt.want)
		}
	}
}
