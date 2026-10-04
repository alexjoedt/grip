package grip

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/alexjoedt/grip/internal/logger"
)

const manifestVersion = 1

// Manifest is the exported toolset of a grip home, the input of sync.
// Packages are keyed by install name; entries use the state field names.
type Manifest struct {
	Version  int                      `json:"version"`
	Platform string                   `json:"platform"`
	Packages map[string]ManifestEntry `json:"packages"`
}

// ManifestEntry is one package of a manifest. An empty Tag means the latest
// release.
type ManifestEntry struct {
	Repo          string `json:"repo"`
	Tag           string `json:"tag,omitempty"`
	Asset         string `json:"asset,omitempty"`
	AssetDigest   string `json:"assetDigest,omitempty"`
	Pinned        bool   `json:"pinned"`
	AssetOverride string `json:"assetOverride,omitempty"`
	BinOverride   string `json:"binOverride,omitempty"`
}

// Export returns the manifest of all installed packages. It takes no lock.
func (i *Installer) Export() (Manifest, error) {
	insts, err := i.storage.List()
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Version:  manifestVersion,
		Platform: i.config.OS + "/" + i.config.Arch,
		Packages: make(map[string]ManifestEntry, len(insts)),
	}
	for _, inst := range insts {
		m.Packages[inst.Name] = ManifestEntry{
			Repo:          inst.Repo,
			Tag:           inst.Tag,
			Asset:         inst.Asset,
			AssetDigest:   inst.AssetDigest,
			Pinned:        inst.Pinned,
			AssetOverride: inst.AssetOverride,
			BinOverride:   inst.BinOverride,
		}
	}
	return m, nil
}

// Sync installs every package of m at its recorded tag and pin. Installed
// packages at that tag are left alone apart from the pin, others switch to it;
// packages missing from m are not touched. A recorded digest is enforced when
// m was exported on this platform; from another one the recorded asset,
// digest and asset override are dropped. A failure is logged and the run continues;
// cancellation and the rate limit stop it.
func (i *Installer) Sync(ctx context.Context, m Manifest) error {
	if m.Version != manifestVersion {
		return fmt.Errorf("unsupported manifest version %d, want %d", m.Version, manifestVersion)
	}
	names := slices.Sorted(maps.Keys(m.Packages))
	repos := make(map[string]Repo, len(names))
	for _, name := range names {
		if err := validName(name); err != nil {
			return err
		}
		repo, err := ParseRepo(m.Packages[name].Repo)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		repos[name] = repo
	}
	native := m.Platform == i.config.OS+"/"+i.config.Arch

	var installed, switched, current, failed int
	var stopped error
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, repo := m.Packages[name], repos[name]
		before, err := i.storage.Get(name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if before != nil && before.Repo == repo.String() && (e.Tag == "" || e.Tag == before.Tag) {
			if before.Pinned != e.Pinned {
				err = i.storage.SetPinned(ctx, e.Pinned, name)
			}
			if err != nil {
				logger.Error("%s: %v", name, err)
				failed++
			} else {
				current++
			}
			continue
		}

		opts := InstallOptions{Repo: e.Repo, Tag: e.Tag, Asset: e.AssetOverride, Bin: e.BinOverride, pin: &e.Pinned}
		if name != repo.Name {
			opts.Alias = name
		}
		if native {
			opts.recorded = &Version{Tag: e.Tag, Asset: e.Asset, AssetDigest: e.AssetDigest}
		} else if e.AssetOverride != "" {
			logger.Warn("%s: asset override %q was chosen on %s, not applied", name, e.AssetOverride, cmp.Or(m.Platform, "an unknown platform"))
			opts.Asset = ""
		}
		err = i.Install(ctx, opts)
		if errors.Is(err, ErrRateLimited) {
			stopped = fmt.Errorf("%s: %w", name, err)
			failed++
			break
		}
		switch {
		case err != nil:
			logger.Error("%s: %v", name, err)
			failed++
		case before == nil:
			installed++
		default:
			switched++
		}
	}
	logger.Println("%d installed, %d switched, %d current, %d failed", installed, switched, current, failed)
	if stopped != nil {
		return stopped
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d packages failed", failed, len(names))
	}
	return nil
}
