package grip

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// storeBinary copies the executable at srcPath to dir/name via a synced temp
// file and a rename, so dir/name is either the old or the complete new file.
func storeBinary(srcPath, dir, name string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source binary: %w", err)
	}
	defer func() { _ = src.Close() }()

	srcInfo, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat source binary: %w", err)
	}
	if srcInfo.IsDir() {
		return fmt.Errorf("source is a directory, not a file: %s", srcPath)
	}

	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()

	bar := NewProgressBar(int(srcInfo.Size()), "[cyan][3/3][reset] Installing")
	if _, err := io.Copy(io.MultiWriter(tmp, bar), src); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	fmt.Println() // new line after progress bar

	if err := tmp.Chmod(0o755); err != nil {
		return fmt.Errorf("set binary permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close binary: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

// switchLink points linkPath at target by renaming a fresh symlink over it,
// replacing a previous symlink or regular file atomically.
func switchLink(target, linkPath string) error {
	tmp := filepath.Join(filepath.Dir(linkPath), "."+filepath.Base(linkPath)+".tmp")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("switch symlink: %w", err)
	}
	return nil
}
