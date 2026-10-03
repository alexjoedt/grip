package grip

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// corpusEntry is one repository's latest release, recorded from the forge.
type corpusEntry struct {
	Repo   string            `json:"repo"`
	Tag    string            `json:"tag"`
	Assets []string          `json:"assets"`
	Expect map[string]string `json:"expect"` // "os/arch" -> asset name, "" when none
	Files  []corpusFile      `json:"files,omitempty"`
	Bin    string            `json:"bin,omitempty"`
}

// corpusFile is a regular file inside the linux/amd64 archive.
type corpusFile struct {
	Path string `json:"path"`
	ELF  bool   `json:"elf"`
}

var corpusTargets = []string{"linux/amd64", "linux/arm64", "darwin/arm64"}

func loadCorpus(t *testing.T) []corpusEntry {
	t.Helper()
	paths, err := filepath.Glob("testdata/corpus/*.json")
	require.NoError(t, err)
	require.Len(t, paths, 50)

	entries := make([]corpusEntry, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		var e corpusEntry
		require.NoError(t, json.Unmarshal(data, &e), p)
		for _, target := range corpusTargets {
			want, ok := e.Expect[target]
			require.True(t, ok, "%s: no expectation for %s", p, target)
			if want != "" {
				require.Contains(t, e.Assets, want, "%s: expected asset not published", p)
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// TestCorpus reports how the current selection does on real release asset
// lists. It only logs; 03-02 and 03-05 turn the counts into a gate.
func TestCorpus(t *testing.T) {
	corpus := loadCorpus(t)
	cfg, err := DefaultConfig()
	require.NoError(t, err)

	for _, target := range corpusTargets {
		cfg.OS, cfg.Arch, _ = strings.Cut(target, "/")
		var correct, wrong, missed int
		for _, e := range corpus {
			repo, err := ParseRepo(e.Repo)
			require.NoError(t, err)
			assets := make([]ReleaseAsset, len(e.Assets))
			for i, name := range e.Assets {
				assets[i] = ReleaseAsset{Name: name, URL: "https://example.invalid/" + name}
			}

			want := e.Expect[target]
			got, err := parseAsset(assets, cfg, repo.Owner, repo.Name)
			switch {
			case err != nil && want == "":
				correct++
			case err != nil:
				missed++
				t.Logf("%s %s: none, want %s", target, e.Repo, want)
			case strings.EqualFold(got.Name, want):
				correct++
			default:
				wrong++
				t.Logf("%s %s: got %s, want %q", target, e.Repo, got.Name, want)
			}
		}
		t.Logf("%s: %d correct, %d wrong, %d ambiguous or none (of %d)", target, correct, wrong, missed, len(corpus))
	}

	var hits, listings int
	for _, e := range corpus {
		if len(e.Files) == 0 {
			continue
		}
		listings++
		dir := t.TempDir()
		for _, f := range e.Files {
			content := []byte("not a binary\n")
			if f.ELF {
				content = elfBinary()
			}
			p := filepath.Join(dir, filepath.FromSlash(f.Path))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, content, 0o755))
		}
		got, err := findExecutable(dir)
		if err == nil {
			got, err = filepath.Rel(dir, got)
		}
		if err == nil && filepath.ToSlash(got) == e.Bin {
			hits++
			continue
		}
		t.Logf("binary %s: got %q (%v), want %s", e.Repo, got, err, e.Bin)
	}
	t.Logf("binary linux/amd64: %d of %d listings correct", hits, listings)
}
