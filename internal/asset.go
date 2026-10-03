package grip

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alexjoedt/grip/internal/logger"
)

// Asset describes a release asset (pure data structure)
type Asset struct {
	Name        string
	Alias       string
	OS          string
	Arch        string
	DownloadURL string
	Tag         string
	RepoName    string
	RepoOwner   string
	BinOverride string
}

// BinaryName returns the name for the installed binary
func (a *Asset) BinaryName() string {
	if a.Alias != "" {
		return a.Alias
	}
	if a.RepoName != "" {
		return a.RepoName
	}
	// Fallback: extract name from asset filename
	name := strings.ToLower(a.Name)
	for ext := range map[string]bool{
		".tar.gz": true, ".tar.bz2": true, ".tbz": true,
		".zip": true, ".tar.xz": true, ".bz2": true,
	} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// parseAsset selects the appropriate asset for the platform
func parseAsset(assets []ReleaseAsset, cfg *Config, repoOwner, repoName string) (*Asset, error) {
	chosen, cands, err := selectAsset(assets, cfg.OS, cfg.Arch)
	for _, c := range cands {
		if c.stage == "" {
			logger.Info("asset %s: selected", c.name)
		} else {
			logger.Info("asset %s: dropped by %s", c.name, c.stage)
		}
	}
	if err != nil {
		return nil, err
	}

	logger.Println("Selected asset %s", chosen.Name)
	var variants []string
	for _, c := range cands {
		if c.stage == stageVariant {
			variants = append(variants, c.name)
		}
	}
	if len(variants) > 0 {
		logger.Println("Skipped variants: %s", strings.Join(variants, ", "))
	}

	return &Asset{
		Name:        chosen.Name,
		OS:          cfg.OS,
		Arch:        cfg.Arch,
		DownloadURL: chosen.URL,
		RepoOwner:   repoOwner,
		RepoName:    repoName,
	}, nil
}

// Elimination stages, in the order selectAsset applies them.
const (
	stageDenylist = "denylist"
	stageOS       = "os"
	stageArch     = "arch"
	stageExact    = "exact arch"
	stageMusl     = "musl"
	stageVariant  = "variant"
	stageFormat   = "format"
	stageSpelling = "spelling"
)

// candidate is an asset with the stage that eliminated it; empty for the winner.
type candidate struct {
	name  string
	stage string
}

var deniedExts = []string{
	".sha256", ".sha256sum", ".sha512", ".shasum", ".sum", ".txt", ".bundle",
	".asc", ".sig", ".pem", ".minisig", ".pub", ".proof", ".json", ".jsonl",
	".sbom", ".deb", ".rpm", ".apk", ".pkg", ".flatpak", ".msi", ".exe", ".dmg",
	".appimage", ".zst", ".sh", ".ps1", ".md", ".desktop",
}

// formats in preference order; a name with none of them is a bare binary.
var formats = []struct {
	exts []string
	rank int
}{
	{[]string{".tar.gz", ".tgz"}, 0},
	{[]string{".tar.xz"}, 1},
	{[]string{".tar.bz2", ".tbz"}, 2},
	{[]string{".zip"}, 3},
	{[]string{".bz2", ".gz", ".xz"}, 4},
}

const bareRank = 5

var osAliases = map[string][]string{
	"linux":  {"linux"},
	"darwin": {"darwin", "macos", "mac", "osx", "apple"},
}

var foreignOS = []string{"android", "windows"}

// archAliases maps every known arch token to its GOARCH. canonical marks the
// preferred spellings of amd64 and arm64.
var archAliases = map[string]string{
	"amd64": "amd64", "x86_64": "amd64", "x64": "amd64", "64bit": "amd64",
	"arm64": "arm64", "aarch64": "arm64",
	"386": "386", "i386": "386", "i686": "386", "x86": "386", "32bit": "386",
	"arm": "arm", "armv6": "arm", "armv7": "arm", "armhf": "arm",
	"ppc64le": "ppc64le", "s390x": "s390x", "riscv64": "riscv64",
}

var canonicalArch = []string{"amd64", "x86_64", "arm64", "aarch64"}

var neutralTokens = []string{"gnu", "musl", "unknown", "pc", "apple", "universal", "all"}

type scored struct {
	asset     ReleaseAsset
	exact     bool
	musl      bool
	extra     int
	format    int
	canonical bool
}

// selectAsset picks the asset for goos/goarch through hard filters and
// preference stages that each narrow the remaining set.
func selectAsset(assets []ReleaseAsset, goos, goarch string) (ReleaseAsset, []candidate, error) {
	osToks := osAliases[goos]
	if osToks == nil {
		osToks = []string{goos}
	}

	var cands []candidate
	var live []scored
	for _, a := range assets {
		s, stage := classify(a, goos, goarch, osToks)
		if stage != "" {
			cands = append(cands, candidate{a.Name, stage})
			continue
		}
		live = append(live, s)
	}

	// Each stage ranks the survivors; only the lowest rank stays.
	stages := []struct {
		name string
		rank func(scored) int
	}{
		{stageExact, func(s scored) int { return unless(s.exact) }},
		{stageMusl, func(s scored) int { return unless(s.musl) }},
		{stageVariant, func(s scored) int { return s.extra }},
		{stageFormat, func(s scored) int { return s.format }},
		{stageSpelling, func(s scored) int { return unless(s.canonical) }},
	}
	for _, st := range stages {
		if len(live) == 0 {
			break
		}
		best := st.rank(live[0])
		for _, s := range live[1:] {
			best = min(best, st.rank(s))
		}
		var next []scored
		for _, s := range live {
			if st.rank(s) == best {
				next = append(next, s)
			} else {
				cands = append(cands, candidate{s.asset.Name, st.name})
			}
		}
		live = next
	}

	for _, s := range live {
		cands = append(cands, candidate{name: s.asset.Name})
	}
	switch len(live) {
	case 0:
		return ReleaseAsset{}, cands, fmt.Errorf("no asset found for %s/%s", goos, goarch)
	case 1:
		return live[0].asset, cands, nil
	}
	names := make([]string, len(live))
	for i, s := range live {
		names[i] = s.asset.Name
	}
	return ReleaseAsset{}, cands, fmt.Errorf("%w for %s/%s: %s", ErrAmbiguousAsset, goos, goarch, strings.Join(names, ", "))
}

// classify applies the hard filters; a non-empty stage means a is out.
func classify(a ReleaseAsset, goos, goarch string, osToks []string) (scored, string) {
	name := strings.ToLower(a.Name)
	for _, ext := range deniedExts {
		if strings.HasSuffix(name, ext) {
			return scored{}, stageDenylist
		}
	}

	s := scored{asset: a, format: bareRank}
formats:
	for _, f := range formats {
		for _, ext := range f.exts {
			if strings.HasSuffix(name, ext) {
				name = strings.TrimSuffix(name, ext)
				s.format = f.rank
				break formats
			}
		}
	}

	toks := tokenize(name)
	if !slices.ContainsFunc(toks, func(t string) bool { return slices.Contains(osToks, t) }) ||
		slices.ContainsFunc(toks, func(t string) bool { return slices.Contains(foreignOS, t) }) {
		return scored{}, stageOS
	}

	universal := slices.Contains(toks, "universal") || slices.Contains(toks, "all")
	foreignArch := false
	for _, t := range toks {
		arch, ok := archAliases[t]
		if !ok && t == goarch {
			arch, ok = goarch, true
		}
		switch {
		case !ok:
			if !slices.Contains(osToks, t) && !slices.Contains(neutralTokens, t) {
				s.extra++
			}
		case arch == goarch:
			s.exact = true
			s.canonical = s.canonical || slices.Contains(canonicalArch, t) || t == goarch
		default:
			foreignArch = true
		}
		if t == "musl" {
			s.musl = true
		}
	}
	if !s.exact && (goos != "darwin" || foreignArch && !universal) {
		return scored{}, stageArch
	}
	return s, ""
}

// tokenize splits a lowercased name on '-', '_' and '.', keeping the
// multi-part spellings x86_64 and 64-bit/32-bit as one token.
func tokenize(name string) []string {
	name = strings.NewReplacer("x86_64", "x86~64", "x86-64", "x86~64", "64-bit", "64bit", "32-bit", "32bit").Replace(name)
	toks := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i, t := range toks {
		toks[i] = strings.ReplaceAll(t, "~", "_")
	}
	return toks
}

func unless(b bool) int {
	if b {
		return 0
	}
	return 1
}
