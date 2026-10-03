package grip

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Config holds grip configuration with no global state
type Config struct {
	HomeDir     string
	BinDir      string
	StorePath   string
	TempDir     string
	OS          string
	Arch        string
	OSAliases   map[string][]string
	ArchAliases map[string][]string
}

// DefaultConfig creates config with sensible defaults
func DefaultConfig() (*Config, error) {
	gripHome := os.Getenv("GRIP_HOME")
	if gripHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home directory: %w", err)
		}
		gripHome = filepath.Join(home, ".grip")
	} else if !filepath.IsAbs(gripHome) {
		return nil, fmt.Errorf("GRIP_HOME %q: %w", gripHome, ErrNoAbsolutePath)
	}

	return &Config{
		HomeDir:   gripHome,
		BinDir:    filepath.Join(gripHome, "bin"),
		StorePath: filepath.Join(gripHome, "grip.json"),
		TempDir:   os.TempDir(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		OSAliases: map[string][]string{
			"darwin": {"macos", "mac"},
			"linux":  {"musl"},
		},
		ArchAliases: map[string][]string{
			"amd64": {"x86_64"},
			"arm64": {"aarch64", "universal"},
		},
	}, nil
}

// EnsureDirs creates necessary directories
func (c *Config) EnsureDirs() error {
	return os.MkdirAll(c.BinDir, 0o755)
}

// CheckPathEnv checks if BinDir is in PATH
func (c *Config) CheckPathEnv() bool {
	pathEnv := os.Getenv("PATH")
	parts := filepath.SplitList(pathEnv)
	for _, p := range parts {
		if p == c.BinDir {
			return true
		}
	}
	return false
}
