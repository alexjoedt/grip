package grip

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/alexjoedt/grip/internal/logger"
)

// Asset describes a release asset (pure data structure)
type Asset struct {
	Name        string
	Alias       string
	OS          string
	Arch        string
	DownloadURL string
	Digest      string // published "<algo>:<hex>", may be empty
	Size        int64  // declared size, 0 when unknown
	Tag         string
	RepoName    string
	RepoOwner   string
	BinOverride string
	AnyArch     bool // explicitly chosen; a binary for another arch only warns
}

// choiceError reports candidates that the flag chooses between.
type choiceError struct {
	err   error
	flag  string
	cands []string
}

func (e *choiceError) Error() string { return e.err.Error() + ": " + strings.Join(e.cands, ", ") }
func (e *choiceError) Unwrap() error { return e.err }

// versionGlob replaces the release version in an asset name with '*'. It
// tries the full tag, the part after the last '/', that without a leading
// 'v', and that from its first digit; a name without any is kept.
func versionGlob(name, tag string) string {
	v := tag[strings.LastIndex(tag, "/")+1:]
	tries := []string{tag, v, strings.TrimPrefix(v, "v")}
	if i := strings.IndexFunc(v, unicode.IsDigit); i >= 0 {
		tries = append(tries, v[i:])
	}
	for _, s := range tries {
		if s != "" && strings.Contains(name, s) {
			return strings.Replace(name, s, "*", 1)
		}
	}
	return name
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

// parseAsset selects the asset for the platform. A non-empty pattern limits
// the assets to the names it matches; a single match is taken as is.
func parseAsset(assets []ReleaseAsset, cfg *Config, repoOwner, repoName, pattern string) (*Asset, error) {
	if pattern != "" {
		var matched []ReleaseAsset
		for _, a := range assets {
			ok, err := path.Match(pattern, a.Name)
			if err != nil {
				return nil, fmt.Errorf("asset pattern %q: %w", pattern, err)
			}
			if ok {
				matched = append(matched, a)
			}
		}
		switch len(matched) {
		case 0:
			names := make([]string, len(assets))
			for i, a := range assets {
				names[i] = a.Name
			}
			return nil, &choiceError{fmt.Errorf("asset override %q matches no release asset", pattern), "--asset", names}
		case 1:
			logger.Println("Selected asset %s (matches %s)", matched[0].Name, pattern)
			return &Asset{
				Name:        matched[0].Name,
				OS:          cfg.OS,
				Arch:        cfg.Arch,
				DownloadURL: matched[0].URL,
				Digest:      matched[0].Digest,
				Size:        matched[0].Size,
				RepoOwner:   repoOwner,
				RepoName:    repoName,
				AnyArch:     true,
			}, nil
		}
		assets = matched
	}

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
		Digest:      chosen.Digest,
		Size:        chosen.Size,
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
	return ReleaseAsset{}, cands, &choiceError{fmt.Errorf("%w for %s/%s", ErrAmbiguousAsset, goos, goarch), "--asset", names}
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
