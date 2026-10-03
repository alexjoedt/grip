package grip

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
	fi, err := os.Stat(filepath.Join(e.cfg.BinDir, name))
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

	if err := inst.Remove("grip-fixture-zz"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.BinDir, "grip-fixture-zz")); !os.IsNotExist(err) {
		t.Errorf("binary still exists: %v", err)
	}
	e.assertEmptyState(t)

	if err := inst.Remove("unknown-zz"); err == nil {
		t.Error("remove of unknown package succeeded")
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
