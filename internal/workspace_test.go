package grip

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test Workspace manager
func TestWorkspace(t *testing.T) {
	t.Parallel()

	t.Run("create and cleanup", func(t *testing.T) {
		t.Parallel()

		ws, err := NewWorkspace("", "test-workspace")
		require.NoError(t, err)

		assert.DirExists(t, ws.rootDir)
		assert.DirExists(t, ws.DownloadDir())
		assert.DirExists(t, ws.UnpackDir())

		err = ws.Cleanup()
		assert.NoError(t, err)
		assert.NoDirExists(t, ws.rootDir)
	})

	t.Run("cleanup idempotent", func(t *testing.T) {
		t.Parallel()

		ws, err := NewWorkspace("", "test-workspace")
		require.NoError(t, err)

		err = ws.Cleanup()
		assert.NoError(t, err)

		// Second cleanup should not error
		err = ws.Cleanup()
		assert.NoError(t, err)
	})
}
