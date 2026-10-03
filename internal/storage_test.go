package grip

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestStorage(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStorage(filepath.Join(dir, "grip.json"), &Config{HomeDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestStorageCRUD(t *testing.T) {
	s, _ := newTestStorage(t)

	a := &Installation{Name: "a", Repo: "github.com/o/a", Tag: "v1"}
	b := &Installation{Name: "b", Repo: "https://github.com/o/b", Tag: "v2"}
	for _, inst := range []*Installation{a, b} {
		if err := s.Save(inst); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Get("a")
	if err != nil || got.Tag != "v1" {
		t.Fatalf("Get(a) = %+v, %v", got, err)
	}
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
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

func TestStorageMigratesLegacyLockFile(t *testing.T) {
	dir := t.TempDir()
	lock := "rg 14.1.0 github.com/BurntSushi/ripgrep /usr/local/bin\n" +
		"fzf v0.54.0 https://github.com/junegunn/fzf " + dir + "\n" +
		"malformed line\n"
	if err := os.WriteFile(filepath.Join(dir, "grip.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fzf"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	s, err := NewStorage(filepath.Join(dir, "grip.json"), &Config{HomeDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	all, err := s.List()
	if err != nil || len(all) != 2 {
		t.Fatalf("List() = %d entries, %v; want 2", len(all), err)
	}
	rg, err := s.Get("rg")
	if err != nil || rg.Tag != "14.1.0" || rg.Repo != "github.com/BurntSushi/ripgrep" || rg.InstallPath != "/usr/local/bin" {
		t.Errorf("Get(rg) = %+v, %v", rg, err)
	}
	fzf, err := s.Get("fzf")
	if err != nil || fzf.SHA256 == "" {
		t.Errorf("Get(fzf) = %+v, %v; want SHA256 of existing binary", fzf, err)
	}
	if _, err := s.GetByRepo(Repo{Host: "github.com", Owner: "junegunn", Name: "fzf"}); err != nil {
		t.Errorf("GetByRepo(junegunn/fzf) after migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "grip.lock.backup")); err != nil {
		t.Errorf("legacy lock not backed up: %v", err)
	}
}
