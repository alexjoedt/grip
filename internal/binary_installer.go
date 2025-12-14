package grip

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BinaryInstaller handles installing binary executables to a target directory
type BinaryInstaller struct {
	binDir string
}

// NewBinaryInstaller creates a new BinaryInstaller for the given binary directory
func NewBinaryInstaller(binDir string) (*BinaryInstaller, error) {
	if binDir == "" {
		return nil, fmt.Errorf("binary directory cannot be empty")
	}

	if !filepath.IsAbs(binDir) {
		return nil, fmt.Errorf("binary directory must be absolute path: %s", binDir)
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return nil, fmt.Errorf("create binary directory: %w", err)
	}

	return &BinaryInstaller{
		binDir: binDir,
	}, nil
}

// Install copies an executable to the binary directory and sets proper permissions
// srcPath is the path to the source executable
// binaryName is the name the binary should have in the bin directory
func (b *BinaryInstaller) Install(srcPath, binaryName string) error {
	if err := b.validateSource(srcPath); err != nil {
		return err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source binary: %w", err)
	}
	defer src.Close()

	srcInfo, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat source binary: %w", err)
	}

	destPath := filepath.Join(b.binDir, binaryName)
	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create destination binary: %w", err)
	}
	defer dest.Close()

	bar := NewProgressBar(int(srcInfo.Size()), "[cyan][3/3][reset] Installing")
	_, err = io.Copy(io.MultiWriter(dest, bar), src)
	if err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	fmt.Println() // new line after progress bar

	if err := os.Chmod(destPath, 0755); err != nil {
		return fmt.Errorf("set binary permissions: %w", err)
	}

	return nil
}

// validateSource ensures the source path is valid and executable
func (b *BinaryInstaller) validateSource(srcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("source binary not found: %w", err)
	}

	if info.IsDir() {
		return fmt.Errorf("source is a directory, not a file: %s", srcPath)
	}

	// Check if file has execute permission
	if info.Mode()&0111 == 0 {
		// Try to set execute permission
		if err := os.Chmod(srcPath, 0755); err != nil {
			return fmt.Errorf("source is not executable and cannot set permissions: %w", err)
		}
	}

	return nil
}

// BinDir returns the binary installation directory
func (b *BinaryInstaller) BinDir() string {
	return b.binDir
}
