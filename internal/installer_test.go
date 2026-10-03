package grip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureRepo = "owner/grip-fixture-zz"

type installerEnv struct {
	cfg     *Config
	storage *Storage
	srv     *httptest.Server
}

func newInstallerEnv(t *testing.T) *installerEnv {
	t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg.HomeDir = dir
	cfg.BinDir = filepath.Join(dir, "bin")
	cfg.TempDir = dir
	storage, err := NewStorage(filepath.Join(dir, "grip.json"), cfg)
	if err != nil {
		t.Fatal(err)
	}

	archive := createTestTarGz(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	return &installerEnv{cfg: cfg, storage: storage, srv: srv}
}

func (e *installerEnv) release(tag, path string) *Release {
	name := fmt.Sprintf("tool_%s_%s.tar.gz", e.cfg.OS, e.cfg.Arch)
	return &Release{Tag: tag, Assets: []ReleaseAsset{{Name: name, URL: e.srv.URL + path}}}
}

func (e *installerEnv) installer(src Source) *Installer {
	return NewInstaller(e.cfg, e.storage, src, e.srv.Client())
}

func (e *installerEnv) assertInstalled(t *testing.T, name, tag string) {
	t.Helper()
	link := filepath.Join(e.cfg.BinDir, name)
	want := filepath.Join("..", "pkgs", name, tag, name)
	if target, err := os.Readlink(link); err != nil || target != want {
		t.Fatalf("bin/%s -> %q, %v; want symlink to %s", name, target, err, want)
	}
	fi, err := os.Stat(link)
	if err != nil {
		t.Fatalf("binary %s: %v", name, err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("binary %s not executable: %v", name, fi.Mode())
	}
	inst, err := e.storage.Get(name)
	if err != nil {
		t.Fatalf("state entry %s: %v", name, err)
	}
	if inst.Repo != "github.com/owner/grip-fixture-zz" || inst.Tag != tag {
		t.Errorf("state entry = %s@%s, want github.com/owner/grip-fixture-zz@%s", inst.Repo, inst.Tag, tag)
	}
	if sum, err := calculateFileSHA256(filepath.Join(e.cfg.HomeDir, "pkgs", name, tag, name)); err != nil || inst.SHA256 != sum {
		t.Errorf("state sha256 = %s, store file %s, %v", inst.SHA256, sum, err)
	}
	if inst.InstallPath != "" {
		t.Errorf("installPath = %q, want empty", inst.InstallPath)
	}
}

// assertStore checks that pkgs/<name> holds exactly the given tag dirs and
// that state records previous.
func (e *installerEnv) assertStore(t *testing.T, name, previous string, tags ...string) {
	t.Helper()
	got := dirNames(t, filepath.Join(e.cfg.HomeDir, "pkgs", name))
	slices.Sort(tags)
	if !slices.Equal(got, tags) {
		t.Errorf("pkgs/%s = %v, want %v", name, got, tags)
	}
	inst, err := e.storage.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	var gotPrev string
	if inst.Previous != nil {
		gotPrev = inst.Previous.Tag
	}
	if gotPrev != previous {
		t.Errorf("previous = %q, want %q", gotPrev, previous)
	}
}

func (e *installerEnv) assertEmptyState(t *testing.T) {
	t.Helper()
	all, err := e.storage.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("state has %d entries, want 0", len(all))
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	slices.Sort(names)
	return names
}

func TestInstallNew(t *testing.T) {
	e := newInstallerEnv(t)
	inst := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")})

	if err := inst.Install(context.Background(), InstallOptions{Repo: "https://github.com/" + fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, "grip-fixture-zz", "v1.0.0")
}

func TestInstallForce(t *testing.T) {
	e := newInstallerEnv(t)
	inst := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")})
	ctx := context.Background()

	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo}); err == nil {
		t.Fatal("second install without Force succeeded")
	}
	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo, Force: true}); err != nil {
		t.Fatalf("install with Force: %v", err)
	}
	e.assertInstalled(t, "grip-fixture-zz", "v1.0.0")
}

func TestInstallAlias(t *testing.T) {
	e := newInstallerEnv(t)
	inst := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")})

	if err := inst.Install(context.Background(), InstallOptions{Repo: fixtureRepo, Alias: "gfz-alias"}); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, "gfz-alias", "v1.0.0")
	if _, err := os.Stat(filepath.Join(e.cfg.BinDir, "grip-fixture-zz")); !os.IsNotExist(err) {
		t.Errorf("binary under repo name exists: %v", err)
	}

	inst = e.installer(fakeSource{release: e.release("v1.1.0", "/ok")})
	if err := inst.Update(context.Background(), "gfz-alias"); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, "gfz-alias", "v1.1.0")
	if _, err := e.storage.Get("grip-fixture-zz"); err == nil {
		t.Error("update of alias created an entry under the repo name")
	}
}

func TestUpdate(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	if err := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}

	inst := e.installer(fakeSource{release: e.release("v1.1.0", "/ok")})
	if err := inst.Update(ctx, "grip-fixture-zz"); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, "grip-fixture-zz", "v1.1.0")

	if err := inst.Update(ctx, "unknown-zz"); err == nil {
		t.Error("update of unknown package succeeded")
	}
}

func TestRemove(t *testing.T) {
	e := newInstallerEnv(t)
	inst := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")})
	if err := inst.Install(context.Background(), InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}

	if err := inst.Remove(context.Background(), "grip-fixture-zz"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(e.cfg.BinDir, "grip-fixture-zz")); !os.IsNotExist(err) {
		t.Errorf("binary still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.HomeDir, "pkgs", "grip-fixture-zz")); !os.IsNotExist(err) {
		t.Errorf("store dir still exists: %v", err)
	}
	e.assertEmptyState(t)

	if err := inst.Remove(context.Background(), "unknown-zz"); err == nil {
		t.Error("remove of unknown package succeeded")
	}
}

func TestUpdateKeepsCurrentAndPrevious(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	const name = "grip-fixture-zz"
	install := func(tag string, force bool) {
		t.Helper()
		if err := e.installer(fakeSource{release: e.release(tag, "/ok")}).Install(ctx, InstallOptions{Repo: fixtureRepo, Force: force}); err != nil {
			t.Fatalf("install %s: %v", tag, err)
		}
		e.assertInstalled(t, name, tag)
	}

	install("v1.0.0", false)
	e.assertStore(t, name, "", "v1.0.0")

	pkgDir := filepath.Join(e.cfg.HomeDir, "pkgs", name)
	if err := os.MkdirAll(filepath.Join(pkgDir, "v0.1.0-crashed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, ".tmp-leftover"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	install("v1.1.0", true)
	e.assertStore(t, name, "v1.0.0", "v1.0.0", "v1.1.0")

	install("v1.1.0", true)
	e.assertStore(t, name, "v1.0.0", "v1.0.0", "v1.1.0")

	install("v1.2.0", true)
	e.assertStore(t, name, "v1.1.0", "v1.1.0", "v1.2.0")

	install("v1.0.0", true)
	e.assertStore(t, name, "v1.2.0", "v1.0.0", "v1.2.0")
}

func TestUpdateConvertsV1Install(t *testing.T) {
	const name = "grip-fixture-zz"
	tests := map[string]func(e *installerEnv) string{
		"regular file in bin":  func(e *installerEnv) string { return e.cfg.BinDir },
		"foreign install path": func(e *installerEnv) string { return filepath.Join(e.cfg.HomeDir, "elsewhere") },
	}
	for tname, dir := range tests {
		t.Run(tname, func(t *testing.T) {
			e := newInstallerEnv(t)
			installPath := dir(e)
			old := filepath.Join(installPath, name)
			if err := os.MkdirAll(installPath, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(old, []byte("v1 binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			v1 := &Installation{Name: name, Repo: "github.com/" + fixtureRepo, Version: Version{Tag: "v0.9.0"}, InstallPath: installPath}
			if err := e.storage.Save(v1); err != nil {
				t.Fatal(err)
			}

			if err := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Update(context.Background(), name); err != nil {
				t.Fatal(err)
			}
			e.assertInstalled(t, name, "v1.0.0")
			e.assertStore(t, name, "v0.9.0", "v1.0.0")
			if installPath != e.cfg.BinDir {
				if _, err := os.Lstat(old); !os.IsNotExist(err) {
					t.Errorf("old binary still exists: %v", err)
				}
			}
		})
	}
}

func TestRemoveForeignInstallPathFailsLoudly(t *testing.T) {
	e := newInstallerEnv(t)
	const name = "grip-fixture-zz"
	dir := filepath.Join(e.cfg.HomeDir, "readonly")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := e.storage.Save(&Installation{Name: name, Repo: "github.com/" + fixtureRepo, InstallPath: dir}); err != nil {
		t.Fatal(err)
	}

	if err := e.installer(fakeSource{}).Remove(context.Background(), name); err == nil {
		t.Fatal("remove succeeded on a read-only install path")
	}
	if _, err := e.storage.Get(name); err != nil {
		t.Errorf("state entry dropped after failed remove: %v", err)
	}
}

func TestInstallPathConflict(t *testing.T) {
	const name = "grip-fixture-zz"
	e := newInstallerEnv(t)
	outside := filepath.Join(e.cfg.HomeDir, "usr-bin")
	for _, d := range []string{outside, e.cfg.BinDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inst := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")})

	t.Setenv("PATH", outside)
	if err := inst.Install(context.Background(), InstallOptions{Repo: fixtureRepo}); err == nil || !strings.Contains(err.Error(), "another source") {
		t.Fatalf("install with %s in PATH err = %v, want conflict", outside, err)
	}

	t.Setenv("PATH", e.cfg.BinDir)
	if err := inst.Install(context.Background(), InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatalf("install over leftover in grip bin: %v", err)
	}
	e.assertInstalled(t, name, "v1.0.0")
}

var errFault = errors.New("injected fault")

// installV1 installs fixture v1.0.0 and replaces its store file with known
// bytes, so a later fault can be checked against the previous binary.
func (e *installerEnv) installV1(t *testing.T) []byte {
	t.Helper()
	if err := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Install(context.Background(), InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	prev := []byte("previous binary")
	if err := os.WriteFile(filepath.Join(e.cfg.HomeDir, "pkgs", "grip-fixture-zz", "v1.0.0", "grip-fixture-zz"), prev, 0o755); err != nil {
		t.Fatal(err)
	}
	return prev
}

func TestUpdateFaultKeepsPrevious(t *testing.T) {
	const name = "grip-fixture-zz"
	for _, stage := range []string{"downloaded", "unpacked", "copying", "stored"} {
		t.Run(stage, func(t *testing.T) {
			e := newInstallerEnv(t)
			prev := e.installV1(t)
			assertPrevious := func() {
				t.Helper()
				if got, err := os.ReadFile(filepath.Join(e.cfg.BinDir, name)); err != nil || !bytes.Equal(got, prev) {
					t.Errorf("bin/%s = %q, %v; want previous bytes", name, got, err)
				}
				if inst, err := e.storage.Get(name); err != nil || inst.Tag != "v1.0.0" {
					t.Errorf("state = %+v, %v; want tag v1.0.0", inst, err)
				}
			}

			inst := e.installer(fakeSource{release: e.release("v1.1.0", "/ok")})
			hit := false
			inst.faultHook = func(s string) error {
				if s != stage {
					return nil
				}
				hit = true
				assertPrevious()
				return errFault
			}
			if err := inst.Update(context.Background(), name); !errors.Is(err, errFault) {
				t.Fatalf("Update err = %v, want injected fault", err)
			}
			if !hit {
				t.Fatalf("stage %s not reached", stage)
			}

			assertPrevious()
			pkgDir := filepath.Join(e.cfg.HomeDir, "pkgs", name)
			for dir, want := range map[string][]string{
				e.cfg.BinDir:                    {name},
				pkgDir:                          {"v1.0.0"},
				filepath.Join(pkgDir, "v1.0.0"): {name},
			} {
				if got := dirNames(t, dir); !slices.Equal(got, want) {
					t.Errorf("%s = %v, want %v", dir, got, want)
				}
			}
		})
	}
}

func TestUpdateFaultAfterSwitch(t *testing.T) {
	const name = "grip-fixture-zz"
	e := newInstallerEnv(t)
	e.installV1(t)
	ctx := context.Background()

	inst := e.installer(fakeSource{release: e.release("v1.1.0", "/ok")})
	inst.faultHook = func(s string) error {
		if s == "switched" {
			return errFault
		}
		return nil
	}
	if err := inst.Update(ctx, name); !errors.Is(err, errFault) {
		t.Fatalf("Update err = %v, want injected fault", err)
	}
	if got, err := e.storage.Get(name); err != nil || got.Tag != "v1.0.0" {
		t.Fatalf("state = %+v, %v; want tag v1.0.0", got, err)
	}

	inst.faultHook = nil
	if err := inst.Update(ctx, name); err != nil {
		t.Fatalf("repairing update: %v", err)
	}
	e.assertInstalled(t, name, "v1.1.0")
	e.assertStore(t, name, "v1.0.0", "v1.0.0", "v1.1.0")
}

func TestRemoveFaultResumes(t *testing.T) {
	const name = "grip-fixture-zz"
	tests := map[string]bool{"unlinked": false, "deleted": true} // stage: needs reinstall
	for stage, reinstall := range tests {
		t.Run(stage, func(t *testing.T) {
			e := newInstallerEnv(t)
			e.installV1(t)
			ctx := context.Background()
			inst := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")})
			inst.faultHook = func(s string) error {
				if s == stage {
					return errFault
				}
				return nil
			}
			if err := inst.Remove(ctx, name); !errors.Is(err, errFault) {
				t.Fatalf("Remove err = %v, want injected fault", err)
			}

			inst.faultHook = nil
			if reinstall {
				if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
					t.Fatalf("reinstall: %v", err)
				}
			}
			if err := inst.Remove(ctx, name); err != nil {
				t.Fatalf("resumed remove: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(e.cfg.BinDir, name)); !os.IsNotExist(err) {
				t.Errorf("bin entry still exists: %v", err)
			}
			if _, err := os.Stat(filepath.Join(e.cfg.HomeDir, "pkgs", name)); !os.IsNotExist(err) {
				t.Errorf("store dir still exists: %v", err)
			}
			e.assertEmptyState(t)
		})
	}
}

func TestInstallFailureLeavesNoState(t *testing.T) {
	e := newInstallerEnv(t)
	sources := map[string]Source{
		"source error":   fakeSource{err: errors.New("api down")},
		"download error": fakeSource{release: e.release("v1.0.0", "/fail")},
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			if err := e.installer(src).Install(context.Background(), InstallOptions{Repo: fixtureRepo}); err == nil {
				t.Fatal("install succeeded")
			}
			e.assertEmptyState(t)
		})
	}
}

func TestValidName(t *testing.T) {
	tests := map[string]bool{
		"rg": true, "gh-dash": true, "yq_4": true, "go1.22": true, "c++": true, "A": true,
		"": false, ".": false, "..": false, "../x": false, "a/b": false, `a\b`: false, "a\x00b": false,
		"-rf": false, ".hidden": false, "a b": false, " a": false, "a\n": false, "_x": false,
	}
	for name, ok := range tests {
		if err := validName(name); (err == nil) != ok {
			t.Errorf("validName(%q) = %v, want ok=%v", name, err, ok)
		}
	}
}

func TestTagDir(t *testing.T) {
	tests := []struct {
		tag, want string
		ok        bool
	}{
		{"v1.2.3", "v1.2.3", true},
		{"cli/v1.2.3", "cli%2Fv1.2.3", true},
		{"14.1.0", "14.1.0", true},
		{"", "", false},
		{".", "", false},
		{"..", "", false},
	}
	for _, tt := range tests {
		got, err := tagDir(tt.tag)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("tagDir(%q) = %q, %v; want %q, ok=%v", tt.tag, got, err, tt.want, tt.ok)
		}
	}
}

func TestInstallRejectsBeforeFetch(t *testing.T) {
	errFetch := errors.New("fetch called")
	tests := map[string]struct {
		existing *Installation
		opts     InstallOptions
		want     string
	}{
		"alias escapes bin dir": {opts: InstallOptions{Repo: fixtureRepo, Alias: "../x"}, want: "invalid package name"},
		"repo name invalid":     {opts: InstallOptions{Repo: "owner/.hidden"}, want: "invalid package name"},
		"name used by other repo": {
			existing: &Installation{Name: "grip-fixture-zz", Repo: "github.com/other/grip-fixture-zz"},
			opts:     InstallOptions{Repo: fixtureRepo},
			want:     "--alias",
		},
		"repo installed under other name": {
			existing: &Installation{Name: "gfz", Repo: "github.com/owner/grip-fixture-zz"},
			opts:     InstallOptions{Repo: fixtureRepo, Force: true},
			want:     "remove it first",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newInstallerEnv(t)
			if tt.existing != nil {
				if err := e.storage.Save(tt.existing); err != nil {
					t.Fatal(err)
				}
			}
			err := e.installer(fakeSource{err: errFetch}).Install(context.Background(), tt.opts)
			if err == nil || errors.Is(err, errFetch) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Install err = %v, want %q before fetch", err, tt.want)
			}
		})
	}
}

func TestInstallRejectsUnsafeTag(t *testing.T) {
	e := newInstallerEnv(t)
	err := e.installer(fakeSource{release: e.release("..", "/fail")}).Install(context.Background(), InstallOptions{Repo: fixtureRepo})
	if err == nil || !strings.Contains(err.Error(), "invalid release tag") {
		t.Fatalf("Install err = %v, want invalid release tag", err)
	}
}

func TestInstallConcurrent(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()

	const n = 8
	errs := make(chan error, n+2)
	var wg sync.WaitGroup
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Install(ctx, InstallOptions{Repo: fmt.Sprintf("owner/gfz-par-%d", k)})
		}()
	}
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Install(ctx, InstallOptions{Repo: fixtureRepo, Force: true})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	all, err := e.storage.List()
	if err != nil || len(all) != n+1 {
		t.Fatalf("List() = %d entries, %v; want %d", len(all), err, n+1)
	}
	e.assertInstalled(t, "grip-fixture-zz", "v1.0.0")
}

func TestLockWaitsAndHonorsContext(t *testing.T) {
	e := newInstallerEnv(t)
	unlock, err := e.storage.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := e.storage.Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock while held err = %v, want DeadlineExceeded", err)
	}

	acquired := make(chan error, 1)
	go func() {
		u, err := e.storage.Lock(context.Background())
		if err == nil {
			u()
		}
		acquired <- err
	}()
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-acquired:
		t.Fatalf("Lock returned while held: %v", err)
	default:
	}
	unlock()
	if err := <-acquired; err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
}
