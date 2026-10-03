package grip

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreBinaryAndSwitchLink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(dir, "store")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "tool"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := storeBinary(src, store, "tool", noop); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(store, "tool"))
	if err != nil || string(got) != "new" {
		t.Fatalf("stored binary = %q, %v; want new", got, err)
	}
	if fi, _ := os.Stat(filepath.Join(store, "tool")); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(store); len(entries) != 1 {
		t.Errorf("store has %d entries, want 1 (no temp leftovers)", len(entries))
	}

	link := filepath.Join(dir, "tool")
	if err := os.WriteFile(link, []byte("v1 regular file"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := switchLink("store/tool", link); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != "store/tool" {
		t.Fatalf("Readlink = %q, %v; want store/tool", target, err)
	}
	if got, _ := os.ReadFile(link); string(got) != "new" {
		t.Errorf("through link = %q, want new", got)
	}

	if err := storeBinary(filepath.Join(dir, "missing"), store, "x", noop); err == nil {
		t.Error("storeBinary of missing source succeeded")
	}
}

func noop() error { return nil }
