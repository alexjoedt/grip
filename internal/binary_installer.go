package grip

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// InstallBinary copies the executable at srcPath into binDir with the given
// binaryName and sets executable permissions (0755).
func InstallBinary(srcPath, binDir, binaryName string) error {
	if binDir == "" {
		return fmt.Errorf("binary directory cannot be empty")
	}

	if !filepath.IsAbs(binDir) {
		return fmt.Errorf("binary directory must be absolute path: %s", binDir)
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create binary directory: %w", err)
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("source binary not found: %w", err)
	}

	if srcInfo.IsDir() {
		return fmt.Errorf("source is a directory, not a file: %s", srcPath)
	}

	if srcInfo.Mode()&0111 == 0 {
		if err := os.Chmod(srcPath, 0755); err != nil {
			return fmt.Errorf("source is not executable and cannot set permissions: %w", err)
		}
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source binary: %w", err)
	}
	defer src.Close()

	destPath := filepath.Join(binDir, binaryName)
	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create destination binary: %w", err)
	}
	defer dest.Close()

	bar := NewProgressBar(int(srcInfo.Size()), "[cyan][3/3][reset] Installing")
	if _, err = io.Copy(io.MultiWriter(dest, bar), src); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	fmt.Println() // new line after progress bar

	if err := os.Chmod(destPath, 0755); err != nil {
		return fmt.Errorf("set binary permissions: %w", err)
	}

	return nil
}
