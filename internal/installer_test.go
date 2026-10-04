package grip

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

const fixtureRepo = "owner/grip-fixture-zz"

type installerEnv struct {
	cfg     *Config
	storage *Storage
	srv     *httptest.Server
	files   map[string][]byte // served instead of the tar.gz fixture by path
	hits    *atomic.Int64     // requests to srv
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
	cfg.OS, cfg.Arch = darwinAmd64.OS, darwinAmd64.Arch
	storage, err := NewStorage(filepath.Join(dir, "grip.json"), cfg)
	if err != nil {
		t.Fatal(err)
	}

	archive := createTestTarGz(t)
	files := map[string][]byte{}
	hits := new(atomic.Int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/fail" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if b, ok := files[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)

	return &installerEnv{cfg: cfg, storage: storage, srv: srv, files: files, hits: hits}
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

func TestInstallSingleFileAsset(t *testing.T) {
	bin := append(machOBinary(), make([]byte, 100)...)
	var gz, xzb bytes.Buffer
	gw := gzip.NewWriter(&gz)
	if _, err := gw.Write(bin); err != nil || gw.Close() != nil {
		t.Fatal(err)
	}
	xw, err := xz.NewWriter(&xzb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xw.Write(bin); err != nil || xw.Close() != nil {
		t.Fatal(err)
	}

	for suffix, content := range map[string][]byte{"": bin, ".gz": gz.Bytes(), ".xz": xzb.Bytes()} {
		t.Run("bare"+suffix, func(t *testing.T) {
			e := newInstallerEnv(t)
			name := fmt.Sprintf("tool_%s_%s%s", e.cfg.OS, e.cfg.Arch, suffix)
			e.files["/"+name] = content
			rel := &Release{Tag: "v1.0.0", Assets: []ReleaseAsset{{Name: name, URL: e.srv.URL + "/" + name}}}

			if err := e.installer(fakeSource{release: rel}).Install(context.Background(), InstallOptions{Repo: fixtureRepo}); err != nil {
				t.Fatal(err)
			}
			e.assertInstalled(t, "grip-fixture-zz", "v1.0.0")
			if got, err := os.ReadFile(filepath.Join(e.cfg.BinDir, "grip-fixture-zz")); err != nil || !bytes.Equal(got, bin) {
				t.Errorf("installed binary differs from the asset content: %v", err)
			}
		})
	}

	t.Run("bare non-executable", func(t *testing.T) {
		e := newInstallerEnv(t)
		name := fmt.Sprintf("tool_%s_%s", e.cfg.OS, e.cfg.Arch)
		e.files["/"+name] = []byte("#!/bin/sh\necho hi\n")
		rel := &Release{Tag: "v1.0.0", Assets: []ReleaseAsset{{Name: name, URL: e.srv.URL + "/" + name}}}

		err := e.installer(fakeSource{release: rel}).Install(context.Background(), InstallOptions{Repo: fixtureRepo})
		if err == nil || !strings.Contains(err.Error(), "neither a supported archive nor an executable") {
			t.Fatalf("err = %v, want not-an-executable error", err)
		}
		if _, err := os.Lstat(filepath.Join(e.cfg.BinDir, "grip-fixture-zz")); !os.IsNotExist(err) {
			t.Errorf("bin entry exists after failed install: %v", err)
		}
		if _, err := os.Stat(filepath.Join(e.cfg.HomeDir, "pkgs", "grip-fixture-zz")); !os.IsNotExist(err) {
			t.Errorf("store dir exists after failed install: %v", err)
		}
		e.assertEmptyState(t)
	})
}

func TestInstallForce(t *testing.T) {
	e := newInstallerEnv(t)
	src := &countingSource{Source: fakeSource{release: e.release("v1.0.0", "/ok")}}
	inst := e.installer(src)
	ctx := context.Background()

	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	src.calls.Store(0)
	e.hits.Store(0)
	before := e.snapshot(t)
	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatalf("install of installed package: %v", err)
	}
	if n, h := src.calls.Load(), e.hits.Load(); n != 0 || h != 0 {
		t.Errorf("no-op install sent %d forge and %d asset requests, want 0", n, h)
	}
	if after := e.snapshot(t); !maps.Equal(before, after) {
		t.Errorf("no-op install changed the home:\n%v\n%v", before, after)
	}
	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo, Force: true}); err != nil {
		t.Fatalf("install with Force: %v", err)
	}
	if e.hits.Load() != 1 {
		t.Errorf("install with Force sent %d asset requests, want 1", e.hits.Load())
	}
	e.assertInstalled(t, "grip-fixture-zz", "v1.0.0")
}

// snapshot maps every path under the grip home to its content or link target.
func (e *installerEnv) snapshot(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(e.cfg.HomeDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			m[p] = "-> " + target
			return err
		}
		b, err := os.ReadFile(p)
		m[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpdateCurrentSkipsDownload(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	const name = "grip-fixture-zz"
	src := &countingSource{Source: fakeSource{release: e.release("v1.0.0", "/ok")}}
	inst := e.installer(src)
	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}

	src.calls.Store(0)
	e.hits.Store(0)
	before := e.snapshot(t)
	if err := inst.Update(ctx, name, "", ""); err != nil {
		t.Fatal(err)
	}
	if e.hits.Load() != 0 {
		t.Errorf("update of a current package sent %d asset requests, want 0", e.hits.Load())
	}
	if src.calls.Load() != 1 {
		t.Errorf("update sent %d forge requests, want 1", src.calls.Load())
	}
	if after := e.snapshot(t); !maps.Equal(before, after) {
		t.Errorf("update of a current package changed the home:\n%v\n%v", before, after)
	}

	for _, ov := range []struct{ asset, bin string }{{"tool_darwin_amd64.tar.gz", ""}, {"", "test-executable"}} {
		e.hits.Store(0)
		if err := inst.Update(ctx, name, ov.asset, ov.bin); err != nil {
			t.Fatalf("update with override %+v: %v", ov, err)
		}
		if e.hits.Load() != 1 {
			t.Errorf("update with override %+v sent %d asset requests, want 1", ov, e.hits.Load())
		}
	}
	if got, err := e.storage.Get(name); err != nil || got.BinOverride != "test-executable" || got.AssetOverride == "" {
		t.Errorf("overrides after update = %+v, %v", got, err)
	}

	src = &countingSource{Source: fakeSource{release: e.release("v1.1.0", "/ok")}}
	if err := e.installer(src).Update(ctx, name, "", ""); err != nil {
		t.Fatal(err)
	}
	if src.calls.Load() != 1 {
		t.Errorf("update to a new tag sent %d forge requests, want 1", src.calls.Load())
	}
	e.assertInstalled(t, name, "v1.1.0")
	e.assertStore(t, name, "v1.0.0", "v1.0.0", "v1.1.0")
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
	if err := inst.Update(context.Background(), "gfz-alias", "", ""); err != nil {
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
	if err := inst.Update(ctx, "grip-fixture-zz", "", ""); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, "grip-fixture-zz", "v1.1.0")

	if err := inst.Update(ctx, "unknown-zz", "", ""); err == nil {
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

			if err := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Update(context.Background(), name, "", ""); err != nil {
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
			if err := inst.Update(context.Background(), name, "", ""); !errors.Is(err, errFault) {
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
	if err := inst.Update(ctx, name, "", ""); !errors.Is(err, errFault) {
		t.Fatalf("Update err = %v, want injected fault", err)
	}
	if got, err := e.storage.Get(name); err != nil || got.Tag != "v1.0.0" {
		t.Fatalf("state = %+v, %v; want tag v1.0.0", got, err)
	}

	inst.faultHook = nil
	if err := inst.Update(ctx, name, "", ""); err != nil {
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

// releaseOf returns a release with one asset per name, served from e.files
// when present, else the default fixture archive.
func (e *installerEnv) releaseOf(tag string, names ...string) *Release {
	rel := &Release{Tag: tag}
	for _, n := range names {
		rel.Assets = append(rel.Assets, ReleaseAsset{Name: n, URL: e.srv.URL + "/" + n})
	}
	return rel
}

// tarGzOf builds a tar.gz holding one executable entry per name.
func tarGzOf(t *testing.T, bins map[string][]byte) []byte {
	t.Helper()
	var entries []tarEntry
	for name, content := range bins {
		entries = append(entries, tarEntry{name: name, content: content, mode: 0o755})
	}
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := io.Copy(gw, newTarStream(t, entries)); err != nil || gw.Close() != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAssetOverrideRemembered(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	const name = "grip-fixture-zz"
	names := func(v string) []string {
		return []string{"tool_" + v + "_darwin_amd64.tar.gz", "tool_" + v + "_darwin_amd64_extra.tar.gz"}
	}
	assertEntry := func(asset, override string) {
		t.Helper()
		inst, err := e.storage.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if inst.Asset != asset || inst.AssetOverride != override {
			t.Errorf("asset, override = %q, %q; want %q, %q", inst.Asset, inst.AssetOverride, asset, override)
		}
	}

	err := e.installer(fakeSource{release: e.releaseOf("v1.0.0", names("1.0.0")...)}).
		Install(ctx, InstallOptions{Repo: fixtureRepo, Asset: "tool_1.0.0_darwin_amd64_extra.tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	assertEntry("tool_1.0.0_darwin_amd64_extra.tar.gz", "tool_*_darwin_amd64_extra.tar.gz")

	if err := e.installer(fakeSource{release: e.releaseOf("v1.1.0", names("1.1.0")...)}).Update(ctx, name, "", ""); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, name, "v1.1.0")
	assertEntry("tool_1.1.0_darwin_amd64_extra.tar.gz", "tool_*_darwin_amd64_extra.tar.gz")

	if err := e.installer(fakeSource{release: e.releaseOf("v1.2.0", names("1.2.0")...)}).Update(ctx, name, "*_amd64.tar.gz", ""); err != nil {
		t.Fatal(err)
	}
	assertEntry("tool_1.2.0_darwin_amd64.tar.gz", "*_amd64.tar.gz")

	err = e.installer(fakeSource{release: e.releaseOf("v1.3.0", "tool_1.3.0_darwin_x86_64.tar.gz", "tool_1.3.0_darwin_arm64.tar.gz")}).Update(ctx, name, "", "")
	for _, want := range []string{`asset override "*_amd64.tar.gz" matches no release asset`, "grip update " + name + " --asset tool_1.3.0_darwin_x86_64.tar.gz"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("update with stale override err = %v, want %q", err, want)
		}
	}
	e.assertInstalled(t, name, "v1.2.0")
}

func TestBinOverrideRemembered(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	const name = "grip-fixture-zz"
	e.files["/tool_darwin_amd64.tar.gz"] = tarGzOf(t, map[string][]byte{"a": machOBinary(), "b": machOBinary()})
	rel := e.releaseOf("v1.0.0", "tool_darwin_amd64.tar.gz")

	err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo, Alias: name})
	if !errors.Is(err, ErrAmbiguousBinary) {
		t.Fatalf("err = %v, want ErrAmbiguousBinary", err)
	}
	for _, want := range []string{"grip install " + fixtureRepo + " --alias " + name + " --bin a", "--bin b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}

	if err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo, Bin: "b"}); err != nil {
		t.Fatal(err)
	}
	if inst, err := e.storage.Get(name); err != nil || inst.BinOverride != "b" {
		t.Fatalf("binOverride = %+v, %v; want b", inst, err)
	}

	rel = e.releaseOf("v1.1.0", "tool_darwin_amd64.tar.gz")
	if err := e.installer(fakeSource{release: rel}).Update(ctx, name, "", ""); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, name, "v1.1.0")

	e.files["/tool_darwin_amd64.tar.gz"] = tarGzOf(t, map[string][]byte{"a": machOBinary(), "c": machOBinary()})
	rel = e.releaseOf("v1.2.0", "tool_darwin_amd64.tar.gz")
	err = e.installer(fakeSource{release: rel}).Update(ctx, name, "", "")
	for _, want := range []string{`bin override "b" names no single executable`, "grip update " + name + " --bin c"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("update with stale bin err = %v, want %q", err, want)
		}
	}
	e.assertInstalled(t, name, "v1.1.0")

	if err := e.installer(fakeSource{release: rel}).Update(ctx, name, "", "c"); err != nil {
		t.Fatal(err)
	}
	if inst, err := e.storage.Get(name); err != nil || inst.BinOverride != "c" || inst.Tag != "v1.2.0" {
		t.Fatalf("after update --bin c: %+v, %v", inst, err)
	}
}

func TestExplicitAssetAllowsForeignArch(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	const asset = "tool_darwin_amd64.tar.gz" // holds an arm64 binary
	e.files["/"+asset] = tarGzOf(t, map[string][]byte{"grip-fixture-zz": machOFor(macho.CpuArm64, macho.TypeExec)})
	rel := e.releaseOf("v1.0.0", asset)

	if err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err == nil {
		t.Fatal("arm64 binary installed on amd64 without --asset")
	}
	if err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo, Asset: asset}); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, "grip-fixture-zz", "v1.0.0")
}

func TestAmbiguousAssetRetryCommands(t *testing.T) {
	e := newInstallerEnv(t)
	rel := e.releaseOf("v1.0.0", "a_darwin_amd64.tar.gz", "b_darwin_amd64.tar.gz")
	err := e.installer(fakeSource{release: rel}).Install(context.Background(), InstallOptions{Repo: fixtureRepo + "@v1.0.0", Bin: "x y"})
	if !errors.Is(err, ErrAmbiguousAsset) {
		t.Fatalf("err = %v, want ErrAmbiguousAsset", err)
	}
	for _, want := range []string{
		"\n  grip install " + fixtureRepo + "@v1.0.0 --bin 'x y' --asset a_darwin_amd64.tar.gz",
		"\n  grip install " + fixtureRepo + "@v1.0.0 --bin 'x y' --asset b_darwin_amd64.tar.gz",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	e.assertEmptyState(t)
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// digestRelease serves content under path and publishes digest for it.
func (e *installerEnv) digestRelease(tag, path string, content []byte, digest string) *Release {
	e.files[path] = content
	rel := e.release(tag, path)
	rel.Assets[0].Digest = digest
	return rel
}

func fixtureArchive(t *testing.T, variant byte) []byte {
	t.Helper()
	return tarGzOf(t, map[string][]byte{"grip-fixture-zz": append(machOBinary(), variant)})
}

func TestCheckDigest(t *testing.T) {
	content := []byte("asset bytes")
	good := sha256Digest(content)
	sum := strings.TrimPrefix(good, "sha256:")
	other := sha256Digest([]byte("other"))
	tests := map[string]struct {
		published, pinned string
		source            string
		warn              []string
		err               error
	}{
		"published match":        {published: good, source: DigestSourceAPI},
		"uppercase hex":          {published: "SHA256:" + strings.ToUpper(sum), source: DigestSourceAPI},
		"published mismatch":     {published: other, err: ErrDigestMismatch},
		"unpublished":            {source: DigestSourceNone, warn: []string{"tool.tar.gz", "no published digest"}},
		"other algorithm":        {published: "sha512:abcd", source: DigestSourceNone, warn: []string{"tool.tar.gz", "sha512"}},
		"pinned match":           {published: good, pinned: good, source: DigestSourceAPI},
		"pinned differs":         {published: good, pinned: other, err: ErrDigestChanged},
		"pinned differs, none":   {pinned: other, err: ErrDigestChanged},
		"pinned, unpublished ok": {pinned: good, source: DigestSourceNone, warn: []string{"no published digest"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			asset := &Asset{Name: "tool.tar.gz", Tag: "v1.0.0", Digest: tt.published}
			source, warning, err := checkDigest(asset, sum, tt.pinned)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if err != nil {
				for _, s := range []string{"tool.tar.gz", good} {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q does not name %q", err, s)
					}
				}
				return
			}
			if source != tt.source {
				t.Errorf("source = %q, want %q", source, tt.source)
			}
			if (warning == "") != (len(tt.warn) == 0) {
				t.Errorf("warning = %q, want one containing %q", warning, tt.warn)
			}
			for _, s := range tt.warn {
				if !strings.Contains(warning, s) {
					t.Errorf("warning %q does not contain %q", warning, s)
				}
			}
		})
	}
}

func TestInstallDigestMismatchInstallsNothing(t *testing.T) {
	e := newInstallerEnv(t)
	content := fixtureArchive(t, 1)
	inst := e.installer(fakeSource{release: e.digestRelease("v1.0.0", "/a", content, sha256Digest([]byte("tampered")))})
	var stages []string
	inst.faultHook = func(s string) error { stages = append(stages, s); return nil }

	err := inst.Install(context.Background(), InstallOptions{Repo: fixtureRepo})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Install err = %v, want ErrDigestMismatch", err)
	}
	for _, s := range []string{"tool_darwin_amd64.tar.gz", sha256Digest(content), sha256Digest([]byte("tampered"))} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not name %q", err, s)
		}
	}
	if len(stages) != 0 {
		t.Errorf("stages reached after mismatch: %v", stages)
	}
	for _, p := range []string{e.cfg.BinDir, filepath.Join(e.cfg.HomeDir, "pkgs"), filepath.Join(e.cfg.HomeDir, "grip.json")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after mismatch: %v", p, err)
		}
	}
}

func TestUpdateDigestMismatchKeepsPrevious(t *testing.T) {
	const name = "grip-fixture-zz"
	e := newInstallerEnv(t)
	prev := e.installV1(t)

	rel := e.digestRelease("v1.1.0", "/b", fixtureArchive(t, 2), sha256Digest([]byte("tampered")))
	if err := e.installer(fakeSource{release: rel}).Update(context.Background(), name, "", ""); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Update err = %v, want ErrDigestMismatch", err)
	}
	if got, err := os.ReadFile(filepath.Join(e.cfg.BinDir, name)); err != nil || !bytes.Equal(got, prev) {
		t.Errorf("bin/%s = %q, %v; want previous bytes", name, got, err)
	}
	e.assertStore(t, name, "", "v1.0.0")
	if inst, err := e.storage.Get(name); err != nil || inst.Tag != "v1.0.0" {
		t.Errorf("state = %+v, %v; want tag v1.0.0", inst, err)
	}
}

func TestDigestRecorded(t *testing.T) {
	const name = "grip-fixture-zz"
	e := newInstallerEnv(t)
	ctx := context.Background()
	v1 := fixtureArchive(t, 1)
	if err := e.installer(fakeSource{release: e.digestRelease("v1.0.0", "/a", v1, sha256Digest(v1))}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	inst, err := e.storage.Get(name)
	if err != nil || inst.AssetDigest != sha256Digest(v1) || inst.DigestSource != DigestSourceAPI {
		t.Fatalf("v1 entry = %+v, %v; want digest %s from %s", inst, err, sha256Digest(v1), DigestSourceAPI)
	}

	v2 := fixtureArchive(t, 2)
	if err := e.installer(fakeSource{release: e.digestRelease("v1.1.0", "/b", v2, "")}).Update(ctx, name, "", ""); err != nil {
		t.Fatal(err)
	}
	inst, err = e.storage.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	if inst.AssetDigest != sha256Digest(v2) || inst.DigestSource != DigestSourceNone {
		t.Errorf("current = %s from %s, want %s from %s", inst.AssetDigest, inst.DigestSource, sha256Digest(v2), DigestSourceNone)
	}
	if p := inst.Previous; p == nil || p.AssetDigest != sha256Digest(v1) || p.DigestSource != DigestSourceAPI {
		t.Errorf("previous = %+v, want digest %s from %s", p, sha256Digest(v1), DigestSourceAPI)
	}
}

func TestDigestPin(t *testing.T) {
	const name = "grip-fixture-zz"
	ctx := context.Background()
	v1, changed := fixtureArchive(t, 1), fixtureArchive(t, 9)

	t.Run("current", func(t *testing.T) {
		e := newInstallerEnv(t)
		if err := e.installer(fakeSource{release: e.digestRelease("v1.0.0", "/a", v1, "")}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(filepath.Join(e.cfg.HomeDir, "grip.json"))

		rel := e.digestRelease("v1.0.0", "/a2", changed, sha256Digest(changed))
		err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo, Force: true})
		if !errors.Is(err, ErrDigestChanged) {
			t.Fatalf("Install err = %v, want ErrDigestChanged", err)
		}
		for _, s := range []string{sha256Digest(v1), sha256Digest(changed), "grip remove " + name} {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("error %q does not contain %q", err, s)
			}
		}
		after, _ := os.ReadFile(filepath.Join(e.cfg.HomeDir, "grip.json"))
		if !bytes.Equal(before, after) {
			t.Error("state changed after pin failure")
		}
	})

	t.Run("previous", func(t *testing.T) {
		e := newInstallerEnv(t)
		if err := e.installer(fakeSource{release: e.digestRelease("v1.0.0", "/a", v1, sha256Digest(v1))}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
			t.Fatal(err)
		}
		if err := e.installer(fakeSource{release: e.digestRelease("v1.1.0", "/b", fixtureArchive(t, 2), "")}).Update(ctx, name, "", ""); err != nil {
			t.Fatal(err)
		}
		rel := e.digestRelease("v1.0.0", "/a2", changed, sha256Digest(changed))
		err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo, Tag: "v1.0.0", Force: true})
		if !errors.Is(err, ErrDigestChanged) {
			t.Fatalf("Install err = %v, want ErrDigestChanged", err)
		}
		e.assertInstalled(t, name, "v1.1.0")
	})

	t.Run("no recorded digest", func(t *testing.T) {
		e := newInstallerEnv(t)
		if err := e.installer(fakeSource{release: e.digestRelease("v1.0.0", "/a", v1, "")}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
			t.Fatal(err)
		}
		inst, err := e.storage.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		inst.AssetDigest, inst.DigestSource = "", ""
		if err := e.storage.Save(inst); err != nil {
			t.Fatal(err)
		}
		rel := e.digestRelease("v1.0.0", "/a2", changed, "")
		if err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo, Force: true}); err != nil {
			t.Fatalf("unpinned reinstall: %v", err)
		}
	})

	t.Run("other asset name", func(t *testing.T) {
		e := newInstallerEnv(t)
		if err := e.installer(fakeSource{release: e.digestRelease("v1.0.0", "/a", v1, "")}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
			t.Fatal(err)
		}
		e.files["/a2"] = changed
		rel := &Release{Tag: "v1.0.0", Assets: []ReleaseAsset{{Name: "grip-fixture-zz_darwin_amd64.tar.gz", URL: e.srv.URL + "/a2"}}}
		if err := e.installer(fakeSource{release: rel}).Install(ctx, InstallOptions{Repo: fixtureRepo, Force: true}); err != nil {
			t.Fatalf("install of another asset: %v", err)
		}
	})
}

func TestUpdateArchiveTooLargeKeepsPrevious(t *testing.T) {
	const name = "grip-fixture-zz"
	e := newInstallerEnv(t)
	prev := e.installV1(t)
	lowerExtractLimits(t, 1000, 100)
	homeBefore := dirNames(t, e.cfg.HomeDir)

	big := tarGzOf(t, map[string][]byte{name: append(machOBinary(), make([]byte, 5000)...)})
	err := e.installer(fakeSource{release: e.digestRelease("v1.1.0", "/big", big, "")}).Update(context.Background(), name, "", "")
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("Update err = %v, want ErrArchiveTooLarge", err)
	}
	if got, err := os.ReadFile(filepath.Join(e.cfg.BinDir, name)); err != nil || !bytes.Equal(got, prev) {
		t.Errorf("bin/%s = %q, %v; want previous bytes", name, got, err)
	}
	e.assertStore(t, name, "", "v1.0.0")
	if got := dirNames(t, e.cfg.HomeDir); !slices.Equal(got, homeBefore) {
		t.Errorf("home = %v, want %v (workspace left behind?)", got, homeBefore)
	}
}

func TestVerify(t *testing.T) {
	const name = "grip-fixture-zz"
	e := newInstallerEnv(t)
	ctx := context.Background()
	if err := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	for _, inst := range []*Installation{
		{Name: "nohash-zz", Repo: "github.com/o/nohash-zz", Version: Version{Tag: "v1"}},
		{Name: "gone-zz", Repo: "github.com/o/gone-zz", Version: Version{Tag: "v2", SHA256: "abc", DigestSource: DigestSourceAPI}},
	} {
		if err := e.storage.Save(inst); err != nil {
			t.Fatal(err)
		}
	}
	unlock, err := e.storage.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	stateBefore, _ := os.ReadFile(filepath.Join(e.cfg.HomeDir, "grip.json"))

	check := func(want ...VerifyResult) {
		t.Helper()
		got, err := e.storage.Verify()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("Verify() = %+v\nwant %+v", got, want)
		}
	}
	check(
		VerifyResult{"gone-zz", "v2", VerifyMissing, DigestSourceAPI},
		VerifyResult{name, "v1.0.0", VerifyOK, DigestSourceNone},
		VerifyResult{"nohash-zz", "v1", VerifyNoHash, ""},
	)

	store := filepath.Join(e.cfg.HomeDir, "pkgs", name, "v1.0.0", name)
	b, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 0xff
	if err := os.WriteFile(store, b, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := e.storage.Verify(name)
	if err != nil || len(got) != 1 || got[0].Result != VerifyModified || !got[0].Failed() {
		t.Errorf("Verify(%s) = %+v, %v; want modified", name, got, err)
	}

	if _, err := e.storage.Verify("unknown-zz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Verify(unknown) err = %v, want ErrNotFound", err)
	}
	if stateAfter, _ := os.ReadFile(filepath.Join(e.cfg.HomeDir, "grip.json")); !bytes.Equal(stateBefore, stateAfter) {
		t.Error("Verify changed the state file")
	}
	if got := dirNames(t, filepath.Join(e.cfg.HomeDir, "pkgs", name)); !slices.Equal(got, []string{"v1.0.0"}) {
		t.Errorf("store = %v, want [v1.0.0]", got)
	}
}

// selfExe writes a fake running grip outside the store and returns its
// symlink-resolved path.
func selfExe(t *testing.T) (string, []byte) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := []byte("old grip")
	exe := filepath.Join(dir, "grip")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, old
}

func TestSelfUpdateDirect(t *testing.T) {
	ctx := context.Background()
	archive := fixtureArchive(t, 7)
	newBin := append(machOBinary(), 7)
	tests := map[string]struct {
		digest string
		fault  string
		want   error
	}{
		"verified":  {digest: sha256Digest(archive)},
		"mismatch":  {digest: sha256Digest([]byte("tampered")), want: ErrDigestMismatch},
		"no digest": {want: ErrDigestMissing},
		"other alg": {digest: "sha512:abcd", want: ErrDigestMissing},
		"mid-copy":  {digest: sha256Digest(archive), fault: "copying", want: errFault},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := newInstallerEnv(t)
			exe, old := selfExe(t)
			inst := e.installer(fakeSource{release: e.digestRelease("v9.0.0", "/grip", archive, tt.digest)})
			inst.faultHook = func(s string) error {
				if s == tt.fault {
					return errFault
				}
				return nil
			}

			err := inst.selfUpdate(ctx, "v1.0.0", exe)
			if !errors.Is(err, tt.want) {
				t.Fatalf("selfUpdate err = %v, want %v", err, tt.want)
			}
			want := newBin
			if tt.want != nil {
				want = old
			}
			if got, err := os.ReadFile(exe); err != nil || !bytes.Equal(got, want) {
				t.Errorf("exe = %q, %v; want %q", got, err, want)
			}
			if got := dirNames(t, filepath.Dir(exe)); !slices.Equal(got, []string{"grip"}) {
				t.Errorf("exe dir = %v, want only grip", got)
			}
		})
	}
}

func TestSelfUpdateDelegates(t *testing.T) {
	const name = "grip-fixture-zz"
	ctx := context.Background()
	e := newInstallerEnv(t)
	v1 := fixtureArchive(t, 1)
	if err := e.installer(fakeSource{release: e.digestRelease("v1.0.0", "/a", v1, sha256Digest(v1))}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	exe, err := filepath.EvalSymlinks(filepath.Join(e.cfg.BinDir, name))
	if err != nil {
		t.Fatal(err)
	}

	v2 := fixtureArchive(t, 2)
	unpublished := e.installer(fakeSource{release: e.digestRelease("v1.1.0", "/b", v2, "")})
	if err := unpublished.selfUpdate(ctx, "v1.0.0", exe); !errors.Is(err, ErrDigestMissing) {
		t.Fatalf("delegated selfUpdate without digest err = %v, want ErrDigestMissing", err)
	}
	e.assertInstalled(t, name, "v1.0.0")

	if err := e.installer(fakeSource{release: e.digestRelease("v1.1.0", "/b", v2, sha256Digest(v2))}).selfUpdate(ctx, "v1.0.0", exe); err != nil {
		t.Fatalf("delegated selfUpdate: %v", err)
	}
	e.assertInstalled(t, name, "v1.1.0")
	if inst, err := e.storage.Get(name); err != nil || inst.AssetDigest != sha256Digest(v2) || inst.Repo != "github.com/"+fixtureRepo {
		t.Errorf("state = %+v, %v", inst, err)
	}

	v3 := fixtureArchive(t, 3)
	if err := e.installer(fakeSource{release: e.digestRelease("v1.2.0", "/c", v3, "")}).Update(ctx, name, "", ""); err != nil {
		t.Fatalf("plain update without digest: %v", err)
	}
	e.assertInstalled(t, name, "v1.2.0")
}

func TestPin(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	const name = "grip-fixture-zz"
	if err := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	pinned := func() bool {
		t.Helper()
		inst, err := e.storage.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		return inst.Pinned
	}

	e.hits.Store(0)
	before := e.snapshot(t)
	if err := e.storage.SetPinned(ctx, true, name, "unknown-zz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pin with unknown name err = %v, want ErrNotFound", err)
	}
	if !maps.Equal(before, e.snapshot(t)) {
		t.Error("failed pin changed the home")
	}
	for range 2 {
		if err := e.storage.SetPinned(ctx, true, name); err != nil || !pinned() {
			t.Fatalf("pin: %v, pinned %v", err, pinned())
		}
	}
	state := filepath.Join(e.cfg.HomeDir, "grip.json")
	after := e.snapshot(t)
	delete(before, state)
	delete(after, state)
	if !maps.Equal(before, after) || e.hits.Load() != 0 {
		t.Errorf("pin touched store or links, or sent %d requests", e.hits.Load())
	}

	src := &countingSource{Source: fakeSource{release: e.release("v1.1.0", "/ok")}}
	err := e.installer(src).Update(ctx, name, "", "")
	if err == nil || err.Error() != name+" is pinned at v1.0.0, run grip unpin "+name {
		t.Fatalf("update of pinned package err = %v", err)
	}
	if src.calls.Load() != 0 {
		t.Errorf("update of pinned package sent %d forge requests", src.calls.Load())
	}

	src = &countingSource{Source: fakeSource{release: e.release("v1.0.0", "/ok")}}
	if err := e.installer(src).Install(ctx, InstallOptions{Repo: fixtureRepo, Force: true}); err != nil {
		t.Fatal(err)
	}
	if src.tag != "v1.0.0" {
		t.Errorf("install --force on pinned package fetched tag %q, want v1.0.0", src.tag)
	}
	if !pinned() {
		t.Error("install --force dropped the pin")
	}

	for range 2 {
		if err := e.storage.SetPinned(ctx, false, name); err != nil || pinned() {
			t.Fatalf("unpin: %v, pinned %v", err, pinned())
		}
	}
	if err := e.installer(fakeSource{release: e.release("v1.1.0", "/ok")}).Update(ctx, name, "", ""); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, name, "v1.1.0")
}

func TestInstallAtTag(t *testing.T) {
	ctx := context.Background()
	const name = "grip-fixture-zz"
	for _, tt := range []struct{ ref, tag string }{
		{fixtureRepo + "@v1.2.3", "v1.2.3"},
		{"github.com/" + fixtureRepo + "@kustomize/v5.8.2", "kustomize/v5.8.2"},
		{"https://github.com/" + fixtureRepo + "@pkg@1.0", "pkg@1.0"},
	} {
		t.Run(tt.tag, func(t *testing.T) {
			e := newInstallerEnv(t)
			src := &countingSource{Source: fakeSource{release: e.release(tt.tag, "/ok")}}
			if err := e.installer(src).Install(ctx, InstallOptions{Repo: tt.ref}); err != nil {
				t.Fatal(err)
			}
			if src.tag != tt.tag {
				t.Errorf("ReleaseByTag(%q), want %q", src.tag, tt.tag)
			}
			if inst, err := e.storage.Get(name); err != nil || !inst.Pinned || inst.Tag != tt.tag {
				t.Errorf("state = %+v, %v; want pinned at %s", inst, err, tt.tag)
			}
		})
	}

	e := newInstallerEnv(t)
	if err := e.installer(nil).Install(ctx, InstallOptions{Repo: fixtureRepo + "@"}); err == nil || !strings.Contains(err.Error(), "empty tag") {
		t.Errorf("empty tag err = %v", err)
	}

	if err := e.installer(fakeSource{release: e.release("v1.0.0", "/ok")}).Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	if inst, _ := e.storage.Get(name); inst.Pinned {
		t.Error("install without tag pinned the package")
	}

	src := &countingSource{Source: fakeSource{release: e.release("v1.0.0", "/ok")}}
	e.hits.Store(0)
	if err := e.installer(src).Install(ctx, InstallOptions{Repo: fixtureRepo + "@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if src.calls.Load() != 0 || e.hits.Load() != 0 {
		t.Errorf("install at the installed tag sent %d forge and %d asset requests", src.calls.Load(), e.hits.Load())
	}
	if inst, _ := e.storage.Get(name); !inst.Pinned {
		t.Error("install at the installed tag did not pin")
	}

	if err := e.installer(fakeSource{release: e.release("v0.9.0", "/ok")}).Install(ctx, InstallOptions{Repo: fixtureRepo + "@v0.9.0"}); err != nil {
		t.Fatalf("switch to another tag: %v", err)
	}
	e.assertInstalled(t, name, "v0.9.0")
	e.assertStore(t, name, "v1.0.0", "v0.9.0", "v1.0.0")
	if inst, _ := e.storage.Get(name); !inst.Pinned {
		t.Error("switch to another tag did not pin")
	}
}

func TestOutdated(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	for _, inst := range []*Installation{
		{Name: "current", Repo: "github.com/o/current", Version: Version{Tag: "v1"}},
		{Name: "old", Repo: "github.com/o/old", Version: Version{Tag: "v1"}},
		{Name: "held", Repo: "github.com/o/held", Version: Version{Tag: "v1"}, Pinned: true},
	} {
		if err := e.storage.Save(inst); err != nil {
			t.Fatal(err)
		}
	}
	src := repoSource{"current": {Tag: "v1"}, "old": {Tag: "v0.9"}, "held": {Tag: "v2"}}
	before := e.snapshot(t)

	got, err := e.installer(src).Outdated(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []OutdatedPackage{{Name: "held", Tag: "v1", Latest: "v2", Pinned: true}, {Name: "old", Tag: "v1", Latest: "v0.9"}}
	if !slices.Equal(got, want) {
		t.Errorf("Outdated = %+v, want %+v", got, want)
	}

	delete(src, "current")
	got, err = e.installer(src).Outdated(ctx)
	if err == nil || !slices.Equal(got, want) {
		t.Errorf("Outdated with a failed lookup = %+v, %v; want %+v and an error", got, err, want)
	}
	if e.hits.Load() != 0 || !maps.Equal(before, e.snapshot(t)) {
		t.Errorf("outdated sent %d asset requests or changed the home", e.hits.Load())
	}
}

func TestUpdateMany(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	for _, inst := range []*Installation{
		{Name: "current", Repo: "github.com/o/current", Version: Version{Tag: "v1"}},
		{Name: "old", Repo: "github.com/o/old", Version: Version{Tag: "v1"}},
		{Name: "held", Repo: "github.com/o/held", Version: Version{Tag: "v1"}, Pinned: true},
		{Name: "broken", Repo: "github.com/o/broken", Version: Version{Tag: "v1"}},
	} {
		if err := e.storage.Save(inst); err != nil {
			t.Fatal(err)
		}
	}
	tag := func(name string) string {
		t.Helper()
		inst, err := e.storage.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		return inst.Tag
	}
	src := repoSource{"current": e.release("v1", "/ok"), "old": e.release("v2", "/ok"), "held": e.release("v2", "/ok")}

	err := e.installer(src).UpdateMany(ctx)
	if err == nil || err.Error() != "1 of 4 updates failed" {
		t.Fatalf("UpdateMany err = %v, want 1 of 4 failed", err)
	}
	if e.hits.Load() != 1 {
		t.Errorf("update --all sent %d asset requests, want 1", e.hits.Load())
	}
	if tag("old") != "v2" || tag("held") != "v1" || tag("current") != "v1" {
		t.Errorf("tags after update --all: old %s, held %s, current %s", tag("old"), tag("held"), tag("current"))
	}

	if err := e.installer(src).UpdateMany(ctx, "current", "old"); err != nil {
		t.Errorf("update of current packages: %v", err)
	}
	if err := e.installer(src).UpdateMany(ctx, "current", "held"); err == nil {
		t.Error("update of a pinned package by name succeeded")
	}
	if e.hits.Load() != 1 {
		t.Errorf("updates of current packages downloaded, %d asset requests", e.hits.Load())
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := e.installer(src).UpdateMany(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled UpdateMany err = %v", err)
	}
}

func TestRollback(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	const name = "grip-fixture-zz"
	src := &countingSource{Source: fakeSource{release: e.release("v1.0.0", "/ok")}}
	inst := e.installer(src)
	if err := inst.Install(ctx, InstallOptions{Repo: fixtureRepo}); err != nil {
		t.Fatal(err)
	}
	if err := inst.Rollback(ctx, name); err == nil || !strings.Contains(err.Error(), "no previous version") {
		t.Fatalf("rollback without previous err = %v", err)
	}
	if err := inst.Rollback(ctx, "unknown-zz"); err == nil {
		t.Fatal("rollback of unknown package succeeded")
	}
	src.Source = fakeSource{release: e.release("v1.1.0", "/ok")}
	if err := inst.Update(ctx, name, "", ""); err != nil {
		t.Fatal(err)
	}

	e.hits.Store(0)
	src.calls.Store(0)
	state := filepath.Join(e.cfg.HomeDir, "grip.json")
	link := filepath.Join(e.cfg.BinDir, name)
	before := e.snapshot(t)
	delete(before, state)
	delete(before, link)
	for _, want := range []struct{ tag, prev string }{{"v1.0.0", "v1.1.0"}, {"v1.1.0", "v1.0.0"}} {
		if err := inst.Rollback(ctx, name); err != nil {
			t.Fatalf("rollback to %s: %v", want.tag, err)
		}
		e.assertInstalled(t, name, want.tag)
		e.assertStore(t, name, want.prev, "v1.0.0", "v1.1.0")
		if got, _ := e.storage.Get(name); !got.Pinned {
			t.Errorf("rollback to %s did not pin", want.tag)
		}
	}
	after := e.snapshot(t)
	delete(after, state)
	delete(after, link)
	if !maps.Equal(before, after) || e.hits.Load() != 0 || src.calls.Load() != 0 {
		t.Errorf("rollback changed the store or sent %d downloads, %d forge requests", e.hits.Load(), src.calls.Load())
	}

	prevBin := filepath.Join(e.cfg.HomeDir, "pkgs", name, "v1.0.0", name)
	b, err := os.ReadFile(prevBin)
	if err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if err := os.Chmod(prevBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prevBin, b, 0o755); err != nil {
		t.Fatal(err)
	}
	before = e.snapshot(t)
	if err := inst.Rollback(ctx, name); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("rollback to tampered binary err = %v", err)
	}
	if !maps.Equal(before, e.snapshot(t)) {
		t.Error("failed rollback changed the home")
	}
	b[0] ^= 0xff
	if err := os.WriteFile(prevBin, b, 0o755); err != nil {
		t.Fatal(err)
	}

	inst.faultHook = func(s string) error {
		if s == "switched" {
			return errFault
		}
		return nil
	}
	if err := inst.Rollback(ctx, name); !errors.Is(err, errFault) {
		t.Fatalf("rollback err = %v, want injected fault", err)
	}
	if got, err := e.storage.Get(name); err != nil || got.Tag != "v1.1.0" {
		t.Fatalf("state = %+v, %v; want tag v1.1.0", got, err)
	}
	inst.faultHook = nil
	if err := inst.Rollback(ctx, name); err != nil {
		t.Fatalf("repairing rollback: %v", err)
	}
	e.assertInstalled(t, name, "v1.0.0")
	e.assertStore(t, name, "v1.1.0", "v1.0.0", "v1.1.0")
}

// limitSource serves repoSource releases and fails with ErrRateLimited for
// the repo named limit.
type limitSource struct {
	repoSource
	limit string
	calls atomic.Int64
}

func (s *limitSource) LatestRelease(ctx context.Context, r Repo) (*Release, error) {
	s.calls.Add(1)
	if r.Name == s.limit {
		return nil, fmt.Errorf("GET x: 403 Forbidden: %w until 12:00:00 UTC", ErrRateLimited)
	}
	return s.repoSource.LatestRelease(ctx, r)
}

func TestRateLimitStopsRun(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	for _, name := range []string{"a", "b", "c"} {
		if err := e.storage.Save(&Installation{Name: name, Repo: "github.com/o/" + name, Version: Version{Tag: "v1"}}); err != nil {
			t.Fatal(err)
		}
	}
	src := &limitSource{repoSource: repoSource{"a": e.release("v2", "/ok"), "c": e.release("v2", "/ok")}, limit: "b"}

	got, err := e.installer(src).Outdated(ctx)
	if !errors.Is(err, ErrRateLimited) || src.calls.Load() != 2 {
		t.Fatalf("Outdated err = %v after %d lookups, want ErrRateLimited after 2", err, src.calls.Load())
	}
	if want := []OutdatedPackage{{Name: "a", Tag: "v1", Latest: "v2"}}; !slices.Equal(got, want) {
		t.Errorf("Outdated = %+v, want %+v", got, want)
	}

	src.calls.Store(0)
	if err := e.installer(src).UpdateMany(ctx); !errors.Is(err, ErrRateLimited) || src.calls.Load() != 2 {
		t.Fatalf("UpdateMany err = %v after %d lookups, want ErrRateLimited after 2", err, src.calls.Load())
	}
	for name, want := range map[string]string{"a": "v2", "b": "v1", "c": "v1"} {
		if inst, err := e.storage.Get(name); err != nil || inst.Tag != want {
			t.Errorf("%s at %+v, %v; want %s", name, inst, err, want)
		}
	}
}
