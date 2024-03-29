package grip

import (
	"fmt"
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

func TestDownload(t *testing.T) {
	testCases := []struct {
		name string
		url  string
		ok   bool
	}{
		{
			name: "grip",
			url:  "https://github.com/alexjoedt/grip/releases/download/v0.1.0-alpha.6/grip_Darwin_arm64.tar.gz",
			ok:   true,
		},
		{name: "grip", url: "https://github.com/alexjoedt", ok: false},
		{name: "grip", url: "", ok: false},
	}

	for _, tc := range testCases {
		a := Asset{
			Name:        tc.name,
			DownloadURL: tc.url,
		}
		if tc.ok {
			assert.NoError(t, a.init())
			assert.NoError(t, a.download())
			assert.DirExists(t, a.tempDir)
			assert.FileExists(t, filepath.Join(a.tempDir, "download", a.Name))
		} else {
			assert.Error(t, a.download())
		}

		// clean up
		t.Cleanup(func() {
			os.RemoveAll(a.tempDir)
		})
	}
}

func TestUnpack(t *testing.T) {
	testCases := []struct {
		name string
		url  string
		ok   bool
	}{
		{
			name: "grip_Darwin_arm64.tar.gz",
			url:  "https://github.com/alexjoedt/grip/releases/download/v0.1.0-alpha.6/grip_Darwin_arm64.tar.gz",
			ok:   true,
		},
		{name: "grip", url: "https://github.com/alexjoedt", ok: false},
		{name: "grip", url: "", ok: false},
	}

	for _, tc := range testCases {
		a := Asset{
			Name:        tc.name,
			DownloadURL: tc.url,
		}
		if tc.ok {
			assert.NoError(t, a.init())
			assert.NoError(t, a.download())
			assert.NoError(t, a.unpack())
			assert.DirExists(t, a.tempDir)
			assert.DirExists(t, filepath.Join(a.tempDir, "unpack"))
			entries, err := os.ReadDir(filepath.Join(a.tempDir, "unpack"))
			assert.NoError(t, err)
			assert.NotEmpty(t, entries)
			assert.FileExists(t, filepath.Join(a.tempDir, "download", a.Name))
		} else {
			assert.Error(t, a.download())
		}

		// clean up
		t.Cleanup(func() {
			os.RemoveAll(a.tempDir)
		})
	}
}

func TestInstall(t *testing.T) {
	testCases := []struct {
		name string
		url  string
		ok   bool
		path string
		err  error
	}{
		{
			name: "grip_Darwin_arm64.tar.gz",
			url:  "https://github.com/alexjoedt/grip/releases/download/v0.1.0-alpha.6/grip_Darwin_arm64.tar.gz",
			ok:   true,
			path: filepath.Join(os.TempDir()),
		},
		{
			name: "grip_Darwin_arm64.tar.gz",
			url:  "https://github.com/alexjoedt/grip/releases/download/v0.1.0-alpha.6/grip_Darwin_arm64.tar.gz",
			ok:   false,
			path: "",
			err:  ErrNoInstallPath,
		},
		{
			name: "grip_Darwin_arm64.tar.gz",
			url:  "https://github.com/alexjoedt/grip/releases/download/v0.1.0-alpha.6/grip_Darwin_arm64.tar.gz",
			ok:   false,
			path: "temp",
			err:  ErrNoAbsolutePath,
		},
	}

	for i, tc := range testCases {
		a := Asset{
			repoName:    fmt.Sprintf("test-%d", i),
			Alias:       fmt.Sprintf("test-%d", i),
			Name:        tc.name,
			DownloadURL: tc.url,
		}

		assert.NoError(t, a.init())
		assert.NoError(t, a.download())
		assert.NoError(t, a.unpack())
		assert.DirExists(t, a.tempDir)
		assert.DirExists(t, filepath.Join(a.tempDir, "unpack"))
		entries, err := os.ReadDir(filepath.Join(a.tempDir, "unpack"))
		assert.NoError(t, err)
		assert.NotEmpty(t, entries)
		assert.FileExists(t, filepath.Join(a.tempDir, "download", a.Name))

		if tc.ok {
			assert.NoError(t, a.Install(tc.path))
			assert.FileExists(t, filepath.Join(tc.path, a.BinaryName()))

			t.Cleanup(func() {
				os.RemoveAll(tc.path)
			})

		} else {
			assert.ErrorIs(t, tc.err, a.Install(tc.path))
		}

		// clean up
		t.Cleanup(func() {
			os.RemoveAll(a.tempDir)
		})
	}
}
