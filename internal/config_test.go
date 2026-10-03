package grip

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigHome(t *testing.T) {
	t.Setenv("SUDO_USER", "someone-else")

	t.Setenv("GRIP_HOME", "")
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if want := filepath.Join(home, ".grip"); cfg.HomeDir != want {
		t.Errorf("HomeDir = %q, want %q", cfg.HomeDir, want)
	}

	t.Setenv("GRIP_HOME", "/x")
	cfg, err = DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HomeDir != "/x" || cfg.BinDir != "/x/bin" || cfg.StorePath != "/x/grip.json" {
		t.Errorf("GRIP_HOME=/x gives %q, %q, %q", cfg.HomeDir, cfg.BinDir, cfg.StorePath)
	}

	t.Setenv("GRIP_HOME", "rel/home")
	if _, err := DefaultConfig(); !errors.Is(err, ErrNoAbsolutePath) {
		t.Errorf("relative GRIP_HOME err = %v, want ErrNoAbsolutePath", err)
	}
}
