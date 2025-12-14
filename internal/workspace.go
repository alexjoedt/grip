package grip

import (
	"fmt"
	"os"
	"path/filepath"
)

// Workspace manages temporary directories for download and unpacking operations
type Workspace struct {
	rootDir     string
	downloadDir string
	unpackDir   string
	created     bool
}

// NewWorkspace creates a new workspace with temporary directories
func NewWorkspace(baseTempDir, prefix string) (*Workspace, error) {
	if baseTempDir == "" {
		baseTempDir = os.TempDir()
	}

	rootDir, err := os.MkdirTemp(baseTempDir, prefix+"*")
	if err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}

	ws := &Workspace{
		rootDir:     rootDir,
		downloadDir: filepath.Join(rootDir, "download"),
		unpackDir:   filepath.Join(rootDir, "unpack"),
		created:     true,
	}

	if err := os.MkdirAll(ws.downloadDir, 0755); err != nil {
		ws.Cleanup() // Clean up root if we fail
		return nil, fmt.Errorf("create download directory: %w", err)
	}

	if err := os.MkdirAll(ws.unpackDir, 0755); err != nil {
		ws.Cleanup()
		return nil, fmt.Errorf("create unpack directory: %w", err)
	}

	return ws, nil
}

// DownloadDir returns the path to the download directory
func (w *Workspace) DownloadDir() string {
	return w.downloadDir
}

// UnpackDir returns the path to the unpack directory
func (w *Workspace) UnpackDir() string {
	return w.unpackDir
}

// RootDir returns the root directory of the workspace
func (w *Workspace) RootDir() string {
	return w.rootDir
}

// Cleanup removes all workspace directories
func (w *Workspace) Cleanup() error {
	if !w.created || w.rootDir == "" {
		return nil
	}

	if err := os.RemoveAll(w.rootDir); err != nil {
		return fmt.Errorf("cleanup workspace: %w", err)
	}

	w.created = false
	return nil
}
