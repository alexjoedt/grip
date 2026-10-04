package main

import (
	"bytes"
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

func TestQuietAndVerboseConflict(t *testing.T) {
	t.Setenv("GRIP_HOME", t.TempDir())
	err := newApp().Run(context.Background(), []string{"grip", "--quiet", "--verbose", "ls"})
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Errorf("--quiet --verbose err = %v", err)
	}
}

func TestInfo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRIP_HOME", home)
	state := `{"version":2,"packages":{
		"full":{"repo":"github.com/o/full","tag":"v2","asset":"full.tar.gz","assetDigest":"sha256:aa","digestSource":"api-digest",
			"sha256":"bb","installedAt":"2026-01-02T03:04:05Z","pinned":true,"assetOverride":"full*.tar.gz",
			"previous":{"tag":"v1","installedAt":"2025-01-02T03:04:05Z"}},
		"old":{"repo":"github.com/o/old","tag":"v1"}}}`
	statePath := filepath.Join(home, "grip.json")
	if err := os.WriteFile(statePath, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "pkgs", "full", "v2", "full"), filepath.Join(home, "bin", "full")); err != nil {
		t.Fatal(err)
	}
	info := func(name string) (string, error) {
		var out bytes.Buffer
		app := newApp()
		app.Writer = &out
		err := app.Run(context.Background(), []string{"grip", "info", name})
		return out.String(), err
	}

	out, err := info("full")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"github.com/o/full", "full.tar.gz", "sha256:aa (api-digest)", "bb", "pinned:          yes",
		"full*.tar.gz", "bin override:    none", filepath.Join(home, "bin", "full"),
		"store:           " + filepath.Join(home, "pkgs", "full", "v2", "full"), "previous:        v1, installed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("info full does not contain %q:\n%s", want, out)
		}
	}

	out, err = info("old")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"asset:           unknown", "unknown (unknown)", "installed:       unknown", "store:           missing"} {
		if !strings.Contains(out, want) {
			t.Errorf("info old does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "previous") {
		t.Errorf("info old shows a previous version:\n%s", out)
	}

	if _, err := info("unknown"); err == nil {
		t.Error("info unknown succeeded")
	}
	if b, err := os.ReadFile(statePath); err != nil || string(b) != state {
		t.Errorf("info changed the state file: %v", err)
	}
	if _, err := os.Stat(statePath + ".lock"); err == nil {
		t.Error("info took the lock")
	}
}
