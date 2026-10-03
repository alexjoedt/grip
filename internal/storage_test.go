package grip

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStorage(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStorage(filepath.Join(dir, "grip.json"), &Config{HomeDir: dir, BinDir: filepath.Join(dir, "bin")})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestStorageCRUD(t *testing.T) {
	s, dir := newTestStorage(t)

	if all, err := s.List(); err != nil || len(all) != 0 {
		t.Fatalf("List() on missing file = %d entries, %v; want empty", len(all), err)
	}

	a := &Installation{Name: "a", Repo: "github.com/o/a", Version: Version{Tag: "v1"}}
	b := &Installation{Name: "b", Repo: "https://github.com/o/b", Version: Version{Tag: "v2"}}
	for _, inst := range []*Installation{a, b} {
		if err := s.Save(inst); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Get("a")
	if err != nil || got.Tag != "v1" || got.Name != "a" {
		t.Fatalf("Get(a) = %+v, %v", got, err)
	}
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
	}
	if d := s.InstallDir(got); d != filepath.Join(dir, "bin") {
		t.Errorf("InstallDir(a) = %q, want bin dir", d)
	}

	got, err = s.GetByRepo(Repo{Host: "github.com", Owner: "o", Name: "b"})
	if err != nil || got.Name != "b" {
		t.Fatalf("GetByRepo(o/b) = %+v, %v", got, err)
	}
	if _, err := s.GetByRepo(Repo{Host: "github.com", Owner: "o", Name: "c"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByRepo(o/c) err = %v, want ErrNotFound", err)
	}

	all, err := s.List()
	if err != nil || len(all) != 2 {
		t.Fatalf("List() = %d entries, %v; want 2", len(all), err)
	}

	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(a) after delete err = %v, want ErrNotFound", err)
	}
	if err := s.Delete("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(a) twice err = %v, want ErrNotFound", err)
	}
}

func TestStorageWritesVersion2(t *testing.T) {
	s, dir := newTestStorage(t)
	if err := s.Save(&Installation{Name: "rg", Repo: "github.com/BurntSushi/ripgrep", Version: Version{Tag: "14.1.0"}}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "grip.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version  int                                   `json:"version"`
		Packages map[string]map[string]json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 {
		t.Errorf("version = %d, want 2", f.Version)
	}
	entry := f.Packages["rg"]
	for _, k := range []string{"repo", "tag", "asset", "assetDigest", "digestSource", "sha256", "installedAt", "pinned", "assetOverride", "binOverride"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("entry lacks %q: %s", k, raw)
		}
	}
	for _, k := range []string{"name", "alias", "updatedAt", "installPath", "previous"} {
		if _, ok := entry[k]; ok {
			t.Errorf("entry has %q: %s", k, raw)
		}
	}
}

func TestStorageMigratesV1(t *testing.T) {
	fixture, err := os.ReadFile("testdata/grip.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	s, dir := newTestStorage(t)
	path := filepath.Join(dir, "grip.json")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	all, err := s.List()
	if err != nil || len(all) != 3 {
		t.Fatalf("List() = %d entries, %v; want 3", len(all), err)
	}

	tests := []struct {
		name, repo, tag, sha, installPath string
		installedAt                       string
	}{
		{"rg", "github.com/BurntSushi/ripgrep", "14.1.0", "aaaa", "/home/u/.grip/bin", "2025-12-20T10:00:00Z"},
		{"fzf", "github.com/junegunn/fzf", "v0.54.0", "bbbb", "/usr/local/bin", "2025-12-02T10:00:00Z"},
		{"version", "github.com/o/v", "v1", "cccc", "/home/u/.grip/bin", "2025-12-04T10:00:00Z"},
	}
	for _, tt := range tests {
		got, err := s.Get(tt.name)
		if err != nil {
			t.Errorf("Get(%s): %v", tt.name, err)
			continue
		}
		want, _ := time.Parse(time.RFC3339, tt.installedAt)
		if got.Name != tt.name || got.Repo != tt.repo || got.Tag != tt.tag || got.SHA256 != tt.sha ||
			got.InstallPath != tt.installPath || !got.InstalledAt.Equal(want) {
			t.Errorf("Get(%s) = %+v", tt.name, got)
		}
		if s.InstallDir(got) != tt.installPath {
			t.Errorf("InstallDir(%s) = %q, want %q", tt.name, s.InstallDir(got), tt.installPath)
		}
	}

	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, fixture) {
		t.Error("loading v1 rewrote grip.json")
	}
	if _, err := os.Stat(path + ".v1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("grip.json.v1 exists before first save: %v", err)
	}

	if err := s.Delete("fzf"); err != nil {
		t.Fatal(err)
	}
	if backup, err := os.ReadFile(path + ".v1"); err != nil || !bytes.Equal(backup, fixture) {
		t.Fatalf("grip.json.v1 = %v, want fixture copy", err)
	}

	all, err = s.List()
	if err != nil || len(all) != 2 {
		t.Fatalf("List() after save = %d entries, %v; want 2", len(all), err)
	}
	rg, err := s.Get("rg")
	if err != nil || rg.InstallPath != "/home/u/.grip/bin" || rg.SHA256 != "aaaa" {
		t.Errorf("Get(rg) after save = %+v, %v", rg, err)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), `"version": 2`) {
		t.Errorf("grip.json not written as v2: %s", raw)
	}
}

func TestStorageRefusesUnknownVersion(t *testing.T) {
	s, dir := newTestStorage(t)
	path := filepath.Join(dir, "grip.json")
	content := []byte(`{"version": 3, "packages": {}}`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.List(); err == nil {
		t.Error("List() on version 3 succeeded")
	}
	if err := s.Save(&Installation{Name: "a", Repo: "github.com/o/a"}); err == nil {
		t.Error("Save() on version 3 succeeded")
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, content) {
		t.Errorf("version 3 file overwritten: %s", raw)
	}
}

func TestStorageRefusesLegacyLockFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "grip.lock"), []byte("rg 14.1.0 github.com/BurntSushi/ripgrep /usr/local/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewStorage(filepath.Join(dir, "grip.json"), &Config{HomeDir: dir})
	if err == nil || !strings.Contains(err.Error(), "run grip v1.1+ once to migrate") {
		t.Fatalf("NewStorage err = %v, want migrate hint", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("NewStorage wrote files: %v", entries)
	}
}
