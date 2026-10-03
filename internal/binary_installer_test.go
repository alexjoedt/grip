package grip

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test BinaryInstaller service
func TestBinaryInstaller(t *testing.T) {
	t.Parallel()

	t.Run("successful install", func(t *testing.T) {
		t.Parallel()

		// Create test binary
		tempDir := filepath.Join(os.TempDir(), "test-binary-installer")
		require.NoError(t, os.MkdirAll(tempDir, 0755))
		defer os.RemoveAll(tempDir)

		srcPath := filepath.Join(tempDir, "source-binary")
		require.NoError(t, os.WriteFile(srcPath, []byte("test binary content"), 0755))

		// Install
		binDir := filepath.Join(tempDir, "bin")
		err := InstallBinary(srcPath, binDir, "test-binary")
		assert.NoError(t, err)

		// Verify
		installedPath := filepath.Join(binDir, "test-binary")
		assert.FileExists(t, installedPath)

		info, err := os.Stat(installedPath)
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
	})

	t.Run("invalid bin directory", func(t *testing.T) {
		t.Parallel()

		err := InstallBinary("dummy-src", "", "name")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("relative path rejected", func(t *testing.T) {
		t.Parallel()

		err := InstallBinary("dummy-src", "relative/path", "name")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must be absolute path")
	})
}
