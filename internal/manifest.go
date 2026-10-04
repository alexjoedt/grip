package grip

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
