package grip

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tagSource serves releases by "<repo name>@<tag>", the latest under "<repo name>".
type tagSource map[string]*Release

func (s tagSource) LatestRelease(_ context.Context, r Repo) (*Release, error) {
	if rel, ok := s[r.Name]; ok {
		return rel, nil
	}
	return nil, errors.New("no such repo")
}

func (s tagSource) ReleaseByTag(_ context.Context, r Repo, tag string) (*Release, error) {
	if rel, ok := s[r.Name+"@"+tag]; ok {
		return rel, nil
	}
	return nil, errors.New("no such release")
}

func (e *installerEnv) platform() string { return e.cfg.OS + "/" + e.cfg.Arch }

func TestSync(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	src := tagSource{
		"zz-a@v1": e.release("v1", "/ok"),
		"zz-b@v2": e.release("v2", "/ok"),
		"zz-b@v3": e.release("v3", "/ok"),
		"zz-c":    e.release("v3", "/ok"),
	}
	other := &Installation{Name: "zz-other", Repo: "github.com/o/zz-other", Version: Version{Tag: "v9"}}
	if err := e.storage.Save(other); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Version: 1, Platform: e.platform(), Packages: map[string]ManifestEntry{
		"zz-a":    {Repo: "github.com/o/zz-a", Tag: "v1", Pinned: true},
		"alias-b": {Repo: "o/zz-b", Tag: "v2", BinOverride: "test-executable"},
		"zz-c":    {Repo: "github.com/o/zz-c"},
	}}
	get := func(name string) *Installation {
		t.Helper()
		inst, err := e.storage.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		return inst
	}

	if err := e.installer(src).Sync(ctx, m); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"zz-a": "v1 true", "alias-b": "v2 false", "zz-c": "v3 false", "zz-other": "v9 false"} {
		inst := get(name)
		if got := fmt.Sprintf("%s %t", inst.Tag, inst.Pinned); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	if b := get("alias-b"); b.Repo != "github.com/o/zz-b" || b.BinOverride != "test-executable" {
		t.Errorf("alias-b = %s bin %q", b.Repo, b.BinOverride)
	}
	if _, err := os.Readlink(filepath.Join(e.cfg.BinDir, "alias-b")); err != nil {
		t.Errorf("alias-b not linked: %v", err)
	}

	hits := e.hits.Load()
	if err := e.installer(src).Sync(ctx, m); err != nil {
		t.Fatal(err)
	}
	if e.hits.Load() != hits {
		t.Errorf("second sync downloaded %d assets", e.hits.Load()-hits)
	}

	m.Packages["zz-a"] = ManifestEntry{Repo: "github.com/o/zz-a", Tag: "v1"}
	m.Packages["alias-b"] = ManifestEntry{Repo: "github.com/o/zz-b", Tag: "v3", Pinned: true}
	if err := e.installer(src).Sync(ctx, m); err != nil {
		t.Fatal(err)
	}
	if e.hits.Load() != hits+1 {
		t.Errorf("pin change and switch downloaded %d assets, want 1", e.hits.Load()-hits)
	}
	if a := get("zz-a"); a.Pinned {
		t.Error("zz-a still pinned")
	}
	if b := get("alias-b"); b.Tag != "v3" || !b.Pinned || b.Previous == nil || b.Previous.Tag != "v2" {
		t.Errorf("alias-b = %s pinned %t previous %+v, want v3 pinned with previous v2", b.Tag, b.Pinned, b.Previous)
	}
}

func TestSyncRecordedDigest(t *testing.T) {
	e := newInstallerEnv(t)
	ctx := context.Background()
	content := fixtureArchive(t, 1)
	rel := e.digestRelease("v1", "/a", content, sha256Digest(content))
	m := Manifest{Version: 1, Platform: e.platform(), Packages: map[string]ManifestEntry{
		"zz-a": {Repo: "o/zz-a", Tag: "v1", Asset: rel.Assets[0].Name, AssetDigest: sha256Digest([]byte("other"))},
	}}

	err := e.installer(tagSource{"zz-a@v1": rel}).Sync(ctx, m)
	if err == nil || err.Error() != "1 of 1 packages failed" {
		t.Fatalf("Sync err = %v, want 1 of 1 failed", err)
	}
	e.assertEmptyState(t)
	if _, err := os.Lstat(filepath.Join(e.cfg.BinDir, "zz-a")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("bin/zz-a after digest failure: %v", err)
	}

	m.Packages["zz-a"] = ManifestEntry{Repo: "o/zz-a", Tag: "v1", Asset: rel.Assets[0].Name, AssetDigest: sha256Digest(content)}
	if err := e.installer(tagSource{"zz-a@v1": rel}).Sync(ctx, m); err != nil {
		t.Fatal(err)
	}
}

func TestSyncFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("continues past a failed entry", func(t *testing.T) {
		e := newInstallerEnv(t)
		if err := e.storage.Save(&Installation{Name: "zz-a", Repo: "github.com/o/elsewhere", Version: Version{Tag: "v1"}}); err != nil {
			t.Fatal(err)
		}
		m := Manifest{Version: 1, Packages: map[string]ManifestEntry{
			"zz-a": {Repo: "o/zz-a", Tag: "v1"},
			"zz-b": {Repo: "o/zz-b", Tag: "v1"},
		}}
		src := tagSource{"zz-a@v1": e.release("v1", "/ok"), "zz-b@v1": e.release("v1", "/ok")}
		err := e.installer(src).Sync(ctx, m)
		if err == nil || err.Error() != "1 of 2 packages failed" {
			t.Fatalf("Sync err = %v, want 1 of 2 failed", err)
		}
		if _, err := e.storage.Get("zz-b"); err != nil {
			t.Errorf("zz-b not installed after zz-a failed: %v", err)
		}
	})

	t.Run("stops at the rate limit", func(t *testing.T) {
		e := newInstallerEnv(t)
		src := &limitSource{repoSource: repoSource{"zz-b": e.release("v1", "/ok")}, limit: "zz-a"}
		m := Manifest{Version: 1, Packages: map[string]ManifestEntry{"zz-a": {Repo: "o/zz-a"}, "zz-b": {Repo: "o/zz-b"}}}
		if err := e.installer(src).Sync(ctx, m); !errors.Is(err, ErrRateLimited) || !strings.HasPrefix(err.Error(), "zz-a: ") {
			t.Fatalf("Sync err = %v, want zz-a: rate limited", err)
		}
		e.assertEmptyState(t)
	})

	for name, m := range map[string]Manifest{
		"unknown version": {Version: 2, Packages: map[string]ManifestEntry{"zz-a": {Repo: "o/zz-a"}}},
		"invalid name":    {Version: 1, Packages: map[string]ManifestEntry{"zz-a": {Repo: "o/zz-a"}, "../x": {Repo: "o/x"}}},
		"invalid repo":    {Version: 1, Packages: map[string]ManifestEntry{"zz-a": {Repo: "o/zz-a"}, "zz-b": {Repo: "nope"}}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newInstallerEnv(t)
			if err := e.installer(tagSource{"zz-a": e.release("v1", "/ok")}).Sync(ctx, m); err == nil {
				t.Fatal("Sync succeeded")
			}
			if e.hits.Load() != 0 {
				t.Errorf("invalid manifest downloaded %d assets", e.hits.Load())
			}
			e.assertEmptyState(t)
		})
	}
}

// TestExportSync checks the epic criterion: export on A, sync into an empty
// home on A yields the same names, tags and digests; on B the same names and
// tags with B's digests.
func TestExportSync(t *testing.T) {
	ctx := context.Background()
	a := newInstallerEnv(t)
	src := tagSource{}
	digests := map[string]string{} // os/name -> published digest
	for _, name := range []string{"zz-a", "zz-b", "zz-c"} {
		rel := &Release{Tag: "v1"}
		for os, bin := range map[string][]byte{"darwin": machOBinary(), "linux": elfFor(elf.EM_X86_64, elf.ET_EXEC)} {
			content := tarGzOf(t, map[string][]byte{"tool": append(bin, name...)})
			path := "/" + os + "/" + name
			a.files[path] = content
			digests[os+"/"+name] = sha256Digest(content)
			rel.Assets = append(rel.Assets, ReleaseAsset{Name: "tool_" + os + "_amd64.tar.gz", URL: a.srv.URL + path, Digest: sha256Digest(content)})
		}
		src[name], src[name+"@v1"] = rel, rel
	}
	ia := a.installer(src)
	for _, opts := range []InstallOptions{
		{Repo: "o/zz-a@v1"},
		{Repo: "o/zz-b", Alias: "bee", Bin: "tool"},
		{Repo: "o/zz-c", Asset: "tool_darwin_amd64.tar.gz"},
	} {
		if err := ia.Install(ctx, opts); err != nil {
			t.Fatal(err)
		}
	}
	m, err := ia.Export()
	if err != nil {
		t.Fatal(err)
	}
	if m.Packages["zz-c"].AssetOverride == "" {
		t.Fatal("zz-c exported without asset override")
	}

	sync := func(os string) map[string]*Installation {
		t.Helper()
		e := newInstallerEnv(t)
		e.cfg.OS = os
		if err := e.installer(src).Sync(ctx, m); err != nil {
			t.Fatalf("sync on %s: %v", os, err)
		}
		got := map[string]*Installation{}
		insts, err := e.storage.List()
		if err != nil {
			t.Fatal(err)
		}
		for _, inst := range insts {
			got[inst.Name] = inst
		}
		return got
	}

	same := sync("darwin")
	if len(same) != 3 {
		t.Fatalf("sync on A installed %d packages, want 3", len(same))
	}
	for name, e := range m.Packages {
		inst := same[name]
		if inst == nil || inst.Repo != e.Repo || inst.Tag != e.Tag || inst.AssetDigest != e.AssetDigest || inst.Pinned != e.Pinned {
			t.Errorf("sync on A: %s = %+v, want %+v", name, inst, e)
		}
	}

	var other map[string]*Installation
	_, stderr := captureOutput(t, func() { other = sync("linux") })
	if !strings.Contains(stderr, `zz-c: asset override "tool_darwin_amd64.tar.gz" was chosen on darwin/amd64, not applied`) {
		t.Errorf("no warning about the dropped asset override:\n%s", stderr)
	}
	if len(other) != 3 {
		t.Fatalf("sync on B installed %d packages, want 3", len(other))
	}
	for name, e := range m.Packages {
		inst := other[name]
		repo, _ := ParseRepo(e.Repo)
		if inst == nil || inst.Tag != e.Tag || inst.AssetDigest != digests["linux/"+repo.Name] || inst.DigestSource != DigestSourceAPI {
			t.Errorf("sync on B: %s = %+v, want tag %s, linux digest from the forge", name, inst, e.Tag)
		}
	}
	if c := other["zz-c"]; c.AssetOverride != "" {
		t.Errorf("foreign asset override applied on B: %q", c.AssetOverride)
	}
	if b := other["bee"]; b.BinOverride != "tool" {
		t.Errorf("bin override on B = %q, want tool", b.BinOverride)
	}

	m.Platform = ""
	e := m.Packages["zz-a"]
	e.AssetDigest = sha256Digest([]byte("elsewhere"))
	m.Packages["zz-a"] = e
	if a := sync("darwin")["zz-a"]; a.AssetDigest != digests["darwin/zz-a"] {
		t.Errorf("sync without platform enforced the recorded digest: %s", a.AssetDigest)
	}
}
