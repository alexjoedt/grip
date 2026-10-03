package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyCommandsCreateNoFiles(t *testing.T) {
	for _, args := range [][]string{{"grip", "version"}, {"grip", "--help"}, {"grip", "--version"}} {
		t.Run(args[1], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			if err := newApp().Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("%v created %d entries in HOME, want 0", args, len(entries))
			}
		})
	}
}

func TestListOnEmptyHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := newApp().Run(context.Background(), []string{"grip", "ls"}); err != nil {
		t.Fatal(err)
	}
}

func TestShortBuild(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "abc", "0123456789abcdef": "01234567"} {
		if got := shortBuild(in); got != want {
			t.Errorf("shortBuild(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVerifyExitCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRIP_HOME", home)
	run := func(args ...string) error {
		return newApp().Run(context.Background(), append([]string{"grip", "verify"}, args...))
	}
	if err := run(); err != nil {
		t.Fatalf("verify on empty home: %v", err)
	}
	state := `{"version":2,"packages":{"gone":{"repo":"github.com/o/gone","tag":"v1","sha256":"abc"},"nohash":{"repo":"github.com/o/nohash","tag":"v1"}}}`
	if err := os.WriteFile(filepath.Join(home, "grip.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run("nohash"); err != nil {
		t.Errorf("verify nohash: %v, want success", err)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("verify all: %v, want 1 of 2 failed", err)
	}
	if err := run("unknown"); err == nil {
		t.Error("verify unknown succeeded")
	}
}
