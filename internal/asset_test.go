package grip

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInit(t *testing.T) {
	testCases := []Asset{
		{Name: "Test-1"},
		{Name: ""},
	}

	for _, a := range testCases {
		assert.NoError(t, a.init())
		assert.DirExists(t, a.tempDir)
		assert.DirExists(t, filepath.Join(a.tempDir, "unpack"))
		assert.DirExists(t, filepath.Join(a.tempDir, "download"))

		// clean up
		t.Cleanup(func() {
			os.RemoveAll(a.tempDir)
		})
	}
}
