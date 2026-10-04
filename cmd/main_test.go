package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	grip "github.com/alexjoedt/grip/internal"
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

func TestJSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRIP_HOME", home)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		app := newApp()
		app.Writer = &out
		err := app.Run(context.Background(), append([]string{"grip"}, args...))
		return out.String(), err
	}

	for _, cmd := range []string{"ls", "outdated", "verify"} {
		if out, err := run(cmd, "--json"); err != nil || out != "[]\n" {
			t.Errorf("%s --json on empty home = %q, %v; want []", cmd, out, err)
		}
	}

	state := `{"version":2,"packages":{
		"a":{"repo":"github.com/o/a","tag":"v2","asset":"a.tar.gz","sha256":"bb","pinned":true,"previous":{"tag":"v1"}},
		"b":{"repo":"github.com/o/b","tag":"v1"}}}`
	if err := os.WriteFile(filepath.Join(home, "grip.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	keys := []string{"name", "path", "repo", "tag", "asset", "assetDigest", "digestSource", "sha256", "installedAt", "pinned", "assetOverride", "binOverride"}
	check := func(what string, obj map[string]any, extra ...string) {
		t.Helper()
		for _, k := range append(keys, extra...) {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s: missing field %q in %v", what, k, obj)
			}
		}
	}

	out, err := run("info", "--json", "a")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("info --json: %v: %q", err, out)
	}
	check("info", obj, "previous")
	if obj["name"] != "a" || obj["path"] != filepath.Join(home, "bin", "a") {
		t.Errorf("info --json name, path = %v, %v", obj["name"], obj["path"])
	}

	var arr []map[string]any
	out, err = run("ls", "--json", "--filter", "name=^b$")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 1 || arr[0]["name"] != "b" {
		t.Fatalf("ls --json --filter = %q, %v", out, err)
	}
	check("ls", arr[0])

	out, err = run("verify", "--json")
	if err == nil {
		t.Error("verify --json with a missing binary exited zero")
	}
	arr = nil
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("verify --json = %q, %v", out, err)
	}
	for _, r := range arr {
		check("verify", r, "status")
	}
}

func TestCompletion(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("GRIP_HOME", home)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out bytes.Buffer
		app := newApp()
		app.Writer = &out
		if err := app.Run(context.Background(), []string{"grip", "completion", shell}); err != nil || out.Len() == 0 {
			t.Errorf("completion %s: %d bytes, %v", shell, out.Len(), err)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("completion created the grip home: %v", err)
	}

	var help bytes.Buffer
	app := newApp()
	app.Writer = &help
	if err := app.Run(context.Background(), []string{"grip", "--help"}); err != nil || !strings.Contains(help.String(), "completion") {
		t.Errorf("--help does not list completion: %v\n%s", err, help.String())
	}
}

// exitSource serves latest tag per repo name; a missing name fails, "limited"
// hits the rate limit.
type exitSource map[string]string

func (s exitSource) LatestRelease(_ context.Context, r grip.Repo) (*grip.Release, error) {
	if r.Name == "limited" {
		return nil, fmt.Errorf("GET x: 403 Forbidden: %w", grip.ErrRateLimited)
	}
	tag, ok := s[r.Name]
	if !ok {
		return nil, errors.New("no such repo")
	}
	return &grip.Release{Tag: tag}, nil
}

func (s exitSource) ReleaseByTag(ctx context.Context, r grip.Repo, _ string) (*grip.Release, error) {
	return s.LatestRelease(ctx, r)
}

func TestExitCodes(t *testing.T) {
	source = exitSource{"cur": "v1", "old": "v2", "held": "v2"}
	t.Cleanup(func() { source = nil })
	stdin := os.Stdin
	t.Cleanup(func() { os.Stdin = stdin })

	const (
		cur    = `"cur":{"repo":"github.com/o/cur","tag":"v1"}`
		old    = `"old":{"repo":"github.com/o/old","tag":"v1"}`
		held   = `"held":{"repo":"github.com/o/held","tag":"v1","pinned":true}`
		gone   = `"gone":{"repo":"github.com/o/gone","tag":"v1"}`
		lim    = `"limited":{"repo":"github.com/o/limited","tag":"v1"}`
		hashed = `"cur":{"repo":"github.com/o/cur","tag":"v1","sha256":"abc"}`
	)
	tests := []struct {
		name  string
		state []string
		args  []string
		fail  bool
	}{
		{"verify modified or missing", []string{hashed}, []string{"verify"}, true},
		{"verify without hash", []string{cur}, []string{"verify"}, false},
		{"update several, one failed", []string{cur, gone}, []string{"update", "cur", "gone"}, true},
		{"update pinned", []string{held}, []string{"update", "held"}, true},
		{"rate limit", []string{lim, cur}, []string{"outdated"}, true},
		{"outdated failed lookup", []string{cur, gone}, []string{"outdated"}, true},
		{"outdated with newer release", []string{cur, old}, []string{"outdated"}, false},
		{"update current", []string{cur}, []string{"update", "cur"}, false},
		{"update --all only pinned", []string{held}, []string{"update", "--all"}, false},
		{"install installed", []string{cur}, []string{"install", "o/cur"}, false},
		{"info unknown", nil, []string{"info", "nope"}, true},
		{"update unknown", nil, []string{"update", "nope"}, true},
		{"rollback unknown", nil, []string{"rollback", "nope"}, true},
		{"pin unknown", nil, []string{"pin", "nope"}, true},
		{"remove unknown", nil, []string{"remove", "--force", "nope"}, true},
		{"unknown command", nil, []string{"nope"}, true},
		{"unknown flag", nil, []string{"ls", "--nope"}, true},
		{"missing argument", nil, []string{"rollback"}, true},
		{"conflicting flags", nil, []string{"--quiet", "--verbose", "ls"}, true},
		{"remove on empty stdin", []string{cur}, []string{"remove", "cur"}, true},
		{"remove --all on empty stdin", []string{cur}, []string{"remove", "--all"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("GRIP_HOME", home)
			state := fmt.Sprintf(`{"version":2,"packages":{%s}}`, strings.Join(tt.state, ","))
			statePath := filepath.Join(home, "grip.json")
			if err := os.WriteFile(statePath, []byte(state), 0o644); err != nil {
				t.Fatal(err)
			}
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = devNull.Close() }()
			os.Stdin = devNull

			app := newApp()
			app.Writer = io.Discard
			app.ErrWriter = io.Discard
			err = app.Run(context.Background(), append([]string{"grip"}, tt.args...))
			if (err != nil) != tt.fail {
				t.Errorf("grip %s: err = %v, want failure %v", strings.Join(tt.args, " "), err, tt.fail)
			}
			if strings.HasPrefix(tt.name, "remove") && tt.state != nil {
				if b, _ := os.ReadFile(statePath); string(b) != state {
					t.Error("aborted remove changed the state")
				}
			}
		})
	}
}

func TestRemoveDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRIP_HOME", home)
	state := `{"version":2,"packages":{"a":{"repo":"github.com/o/a","tag":"v1"},"b":{"repo":"github.com/o/b","tag":"v1"}}}`
	statePath := filepath.Join(home, "grip.json")
	if err := os.WriteFile(statePath, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(home, "pkgs", name, "v1"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "pkgs", name, "v1", name), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "pkgs", name, "v1", name), filepath.Join(home, "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	listing := func() []string {
		t.Helper()
		var paths []string
		if err := filepath.WalkDir(home, func(p string, _ fs.DirEntry, err error) error {
			paths = append(paths, p)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return paths
	}
	before := listing()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		app := newApp()
		app.Writer = &out
		err := app.Run(context.Background(), append([]string{"grip", "remove", "--dry-run"}, args...))
		return out.String(), err
	}

	out, err := run("a")
	if err != nil {
		t.Fatal(err)
	}
	if want := "a: would remove link " + filepath.Join(home, "bin", "a") + ", store " + filepath.Join(home, "pkgs", "a") + " and the state entry\n"; out != want {
		t.Errorf("dry run a = %q, want %q", out, want)
	}
	if out, err = run("--all"); err != nil || strings.Count(out, "would remove") != 2 || !strings.HasPrefix(out, "a:") {
		t.Errorf("dry run --all = %q, %v", out, err)
	}
	if _, err := run("nope"); err == nil {
		t.Error("dry run of unknown package succeeded")
	}
	if b, err := os.ReadFile(statePath); err != nil || string(b) != state || !slices.Equal(before, listing()) {
		t.Errorf("dry run changed the home: %v", err)
	}
}

func TestExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRIP_HOME", home)
	export := func() (string, error) {
		var out bytes.Buffer
		app := newApp()
		app.Writer = &out
		err := app.Run(context.Background(), []string{"grip", "export"})
		return out.String(), err
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH

	out, err := export()
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("{\n  \"version\": 1,\n  \"platform\": %q,\n  \"packages\": {}\n}\n", platform); out != want {
		t.Errorf("export on empty home = %q, want %q", out, want)
	}

	state := `{"version":2,"packages":{
		"pinned":{"repo":"github.com/o/pinned","tag":"v2","asset":"pinned.tar.gz","assetDigest":"sha256:aa","digestSource":"api-digest",
			"sha256":"bb","installedAt":"2026-01-02T03:04:05Z","pinned":true,"previous":{"tag":"v1"}},
		"rg":{"repo":"github.com/o/ripgrep","tag":"v1","asset":"rg.zip","assetDigest":"sha256:cc","digestSource":"api-digest"},
		"over":{"repo":"github.com/o/over","tag":"v3","asset":"over-v3-x.tar.gz","assetDigest":"sha256:dd","digestSource":"api-digest",
			"assetOverride":"over-*-x.tar.gz","binOverride":"overd"},
		"nodigest":{"repo":"github.com/o/nodigest","tag":"v1","asset":"nodigest","digestSource":"none","installPath":"/opt/bin"}}}`
	statePath := filepath.Join(home, "grip.json")
	if err := os.WriteFile(statePath, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = export()
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "version": 1,
  "platform": "` + platform + `",
  "packages": {
    "nodigest": {
      "repo": "github.com/o/nodigest",
      "tag": "v1",
      "asset": "nodigest",
      "pinned": false
    },
    "over": {
      "repo": "github.com/o/over",
      "tag": "v3",
      "asset": "over-v3-x.tar.gz",
      "assetDigest": "sha256:dd",
      "pinned": false,
      "assetOverride": "over-*-x.tar.gz",
      "binOverride": "overd"
    },
    "pinned": {
      "repo": "github.com/o/pinned",
      "tag": "v2",
      "asset": "pinned.tar.gz",
      "assetDigest": "sha256:aa",
      "pinned": true
    },
    "rg": {
      "repo": "github.com/o/ripgrep",
      "tag": "v1",
      "asset": "rg.zip",
      "assetDigest": "sha256:cc",
      "pinned": false
    }
  }
}
`
	if out != want {
		t.Errorf("export =\n%s\nwant\n%s", out, want)
	}
	if b, err := os.ReadFile(statePath); err != nil || string(b) != state {
		t.Errorf("export changed the state file: %v", err)
	}
	if _, err := os.Stat(statePath + ".lock"); err == nil {
		t.Error("export took the lock")
	}
}

func TestSyncInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRIP_HOME", home)
	sync := func(stdin string, args ...string) error {
		app := newApp()
		app.Reader = strings.NewReader(stdin)
		app.Writer = io.Discard
		return app.Run(context.Background(), append([]string{"grip", "sync"}, args...))
	}

	if err := sync(""); err == nil {
		t.Error("sync without a file succeeded")
	}
	if err := sync("{", "-"); err == nil || !strings.Contains(err.Error(), "read manifest -") {
		t.Errorf("sync of malformed stdin: %v", err)
	}
	if err := sync("", filepath.Join(home, "missing.json")); err == nil {
		t.Error("sync of a missing file succeeded")
	}
	if err := sync(`{"version":1,"packages":{}}`, "-"); err != nil {
		t.Errorf("sync of an empty manifest: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "tools")
	if err := os.WriteFile(manifest, []byte(`{"version":7,"packages":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sync("", manifest); err == nil || !strings.Contains(err.Error(), "unsupported manifest version 7") {
		t.Errorf("sync of version 7: %v", err)
	}
}
