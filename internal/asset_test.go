package grip

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInit(t *testing.T) {
	testCases := []struct {
		name string
		ok   bool
	}{
		{name: "Test-1", ok: true},
		{name: "", ok: false},
	}

	for _, tc := range testCases {
		a := Asset{
			Name: tc.name,
		}

		if tc.ok {
			assert.NoError(t, a.init())
			assert.DirExists(t, a.tempDir)
			assert.DirExists(t, filepath.Join(a.tempDir, "unpack"))
			assert.DirExists(t, filepath.Join(a.tempDir, "download"))
		} else {
			assert.Error(t, a.init())
		}

		// clean up
		t.Cleanup(func() {
			os.RemoveAll(a.tempDir)
		})
	}
}
