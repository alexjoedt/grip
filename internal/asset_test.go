package grip

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-github/v56/github"
	"github.com/schollz/progressbar/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

// MockRoundTripper is a mock implementation of http.RoundTripper for testing HTTP clients
type MockRoundTripper struct {
	mock.Mock
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if resp := args.Get(0); resp != nil {
		return resp.(*http.Response), args.Error(1)
	}
	return nil, args.Error(1)
}

// Helper to create a mock HTTP client
func newMockHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
	}
}

// Test helpers and fixtures

// createMockResponse creates a mock HTTP response
func createMockResponse(statusCode int, body string, contentLength int64) *http.Response {
	return &http.Response{
		StatusCode:    statusCode,
		Status:        http.StatusText(statusCode),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: contentLength,
	}
}

// createTestTarGz creates a test tar.gz archive with a mock executable
func createTestTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Create a simple Mach-O binary for macOS (minimal valid Mach-O header)
	// This is a minimal Mach-O binary that will be detected as executable
	machOHeader := []byte{
		0xcf, 0xfa, 0xed, 0xfe, // MH_MAGIC_64 (little-endian)
		0x07, 0x00, 0x00, 0x01, // CPU_TYPE_X86_64
		0x03, 0x00, 0x00, 0x00, // CPU_SUBTYPE_X86_64_ALL
		0x02, 0x00, 0x00, 0x00, // MH_EXECUTE
		0x00, 0x00, 0x00, 0x00, // ncmds
		0x00, 0x00, 0x00, 0x00, // sizeofcmds
		0x00, 0x00, 0x00, 0x00, // flags
		0x00, 0x00, 0x00, 0x00, // reserved
	}

	// Pad with some additional bytes to make it look more like a real binary
	execContent := append(machOHeader, make([]byte, 1000)...)

	header := &tar.Header{
		Name: "test-executable",
		Mode: 0755,
		Size: int64(len(execContent)),
	}

	require.NoError(t, tw.WriteHeader(header))
	_, err := tw.Write(execContent)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	return buf.Bytes()
}

// Test Downloader service
func TestDownloader(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		filename       string
		downloadURL    string
		mockSetup      func(*MockRoundTripper)
		expectError    bool
		expectedErrMsg string
	}{
		{
			name:        "successful download",
			filename:    "test.tar.gz",
			downloadURL: "https://example.com/test.tar.gz",
			mockSetup: func(m *MockRoundTripper) {
				resp := createMockResponse(200, "test file content", 17)
				m.On("RoundTrip", mock.Anything).Return(resp, nil)
			},
			expectError: false,
		},
		{
			name:        "HTTP 404 error",
			filename:    "missing.tar.gz",
			downloadURL: "https://example.com/missing.tar.gz",
			mockSetup: func(m *MockRoundTripper) {
				resp := createMockResponse(404, "Not Found", 9)
				m.On("RoundTrip", mock.Anything).Return(resp, nil)
			},
			expectError:    true,
			expectedErrMsg: "download failed with status",
		},
		{
			name:        "network error",
			filename:    "network.tar.gz",
			downloadURL: "https://example.com/network.tar.gz",
			mockSetup: func(m *MockRoundTripper) {
				m.On("RoundTrip", mock.Anything).Return(nil, fmt.Errorf("connection refused"))
			},
			expectError:    true,
			expectedErrMsg: "connection refused",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockTransport := new(MockRoundTripper)
			tc.mockSetup(mockTransport)

			httpClient := newMockHTTPClient(mockTransport)
			downloader := NewDownloader(httpClient)
			destDir := filepath.Join(os.TempDir(), "test-download-"+tc.filename)
			defer os.RemoveAll(destDir)

			ctx := context.Background()
			err := downloader.Download(ctx, tc.downloadURL, destDir, tc.filename)

			if tc.expectError {
				assert.Error(t, err)
				if tc.expectedErrMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedErrMsg)
				}
			} else {
				assert.NoError(t, err)
				downloadPath := filepath.Join(destDir, tc.filename)
				assert.FileExists(t, downloadPath)

				content, err := os.ReadFile(downloadPath)
				assert.NoError(t, err)
				assert.Equal(t, "test file content", string(content))
			}

			mockTransport.AssertExpectations(t)
		})
	}
}

// Test Unpacker service
func TestUnpacker(t *testing.T) {
	t.Parallel()

	t.Run("unpack tar.gz", func(t *testing.T) {
		t.Parallel()

		// Create test archive
		tarData := createTestTarGz(t)

		// Write to temp file
		tempDir := filepath.Join(os.TempDir(), "test-unpack")
		require.NoError(t, os.MkdirAll(tempDir, 0755))
		defer os.RemoveAll(tempDir)

		archivePath := filepath.Join(tempDir, "test.tar.gz")
		require.NoError(t, os.WriteFile(archivePath, tarData, 0644))

		// Unpack
		unpacker := NewUnpacker()
		destDir := filepath.Join(tempDir, "output")
		execPath, err := unpacker.Unpack(archivePath, destDir)

		assert.NoError(t, err)
		assert.NotEmpty(t, execPath)
		assert.FileExists(t, execPath)
	})

	t.Run("unsupported format", func(t *testing.T) {
		t.Parallel()

		tempDir := filepath.Join(os.TempDir(), "test-unpack-invalid")
		require.NoError(t, os.MkdirAll(tempDir, 0755))
		defer os.RemoveAll(tempDir)

		archivePath := filepath.Join(tempDir, "test.txt")
		require.NoError(t, os.WriteFile(archivePath, []byte("not an archive"), 0644))

		unpacker := NewUnpacker()
		destDir := filepath.Join(tempDir, "output")
		_, err := unpacker.Unpack(archivePath, destDir)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported archive format")
	})

	t.Run("IsSupportedFormat", func(t *testing.T) {
		t.Parallel()

		unpacker := NewUnpacker()

		assert.True(t, unpacker.IsSupportedFormat("test.tar.gz"))
		assert.True(t, unpacker.IsSupportedFormat("test.zip"))
		assert.True(t, unpacker.IsSupportedFormat("test.tar.bz2"))
		assert.False(t, unpacker.IsSupportedFormat("test.txt"))
		assert.False(t, unpacker.IsSupportedFormat("test.exe"))
	})
}

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
		installer, err := NewBinaryInstaller(binDir)
		require.NoError(t, err)

		err = installer.Install(srcPath, "test-binary")
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

		_, err := NewBinaryInstaller("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("relative path rejected", func(t *testing.T) {
		t.Parallel()

		_, err := NewBinaryInstaller("relative/path")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must be absolute path")
	})
}

// Test Workspace manager
func TestWorkspace(t *testing.T) {
	t.Parallel()

	t.Run("create and cleanup", func(t *testing.T) {
		t.Parallel()

		ws, err := NewWorkspace("", "test-workspace")
		require.NoError(t, err)

		assert.DirExists(t, ws.RootDir())
		assert.DirExists(t, ws.DownloadDir())
		assert.DirExists(t, ws.UnpackDir())

		err = ws.Cleanup()
		assert.NoError(t, err)
		assert.NoDirExists(t, ws.RootDir())
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

// TestAssetBinaryName tests the BinaryName method
func TestAssetBinaryName(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		alias     string
		repoName  string
		assetName string
		expected  string
	}{
		{
			name:     "with alias",
			alias:    "my-tool",
			repoName: "original-repo",
			expected: "my-tool",
		},
		{
			name:     "without alias",
			alias:    "",
			repoName: "repo-name",
			expected: "repo-name",
		},
		{
			name:      "fallback to asset name",
			alias:     "",
			repoName:  "",
			assetName: "tool_linux_amd64.tar.gz",
			expected:  "tool_linux_amd64",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			asset := &Asset{
				Alias:    tc.alias,
				RepoName: tc.repoName,
				Name:     tc.assetName,
			}

			result := asset.BinaryName()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestParseAsset tests the parseAsset function
func TestParseAsset(t *testing.T) {
	t.Parallel()

	// Get current OS and Arch from config
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	currentOS := cfg.OS
	currentArch := cfg.Arch

	testCases := []struct {
		name         string
		assets       []*github.ReleaseAsset
		repoOwner    string
		repoName     string
		expectError  bool
		errorMsg     string
		expectedOS   string
		expectedArch string
	}{
		{
			name: "successful parsing with matching asset",
			assets: []*github.ReleaseAsset{
				{
					Name:               stringPtr("tool_windows_amd64.zip"),
					BrowserDownloadURL: stringPtr("https://example.com/tool_windows_amd64.zip"),
				},
				{
					Name:               stringPtr(fmt.Sprintf("tool_%s_%s.tar.gz", currentOS, currentArch)),
					BrowserDownloadURL: stringPtr(fmt.Sprintf("https://example.com/tool_%s_%s.tar.gz", currentOS, currentArch)),
				},
				{
					Name:               stringPtr("tool_linux_arm64.tar.gz"),
					BrowserDownloadURL: stringPtr("https://example.com/tool_linux_arm64.tar.gz"),
				},
			},
			repoOwner:    "test-owner",
			repoName:     "test-repo",
			expectError:  false,
			expectedOS:   currentOS,
			expectedArch: currentArch,
		},
		{
			name: "no matching asset for current OS/Arch",
			assets: []*github.ReleaseAsset{
				{
					Name:               stringPtr("tool_windows_amd64.zip"),
					BrowserDownloadURL: stringPtr("https://example.com/tool_windows_amd64.zip"),
				},
				{
					Name:               stringPtr("tool_linux_arm64.tar.gz"),
					BrowserDownloadURL: stringPtr("https://example.com/tool_linux_arm64.tar.gz"),
				},
			},
			repoOwner:   "test-owner",
			repoName:    "test-repo",
			expectError: true,
			errorMsg:    fmt.Sprintf("no asset found for %s_%s", currentOS, currentArch),
		},
		{
			name: "asset with unsupported extension",
			assets: []*github.ReleaseAsset{
				{
					Name:               stringPtr(fmt.Sprintf("tool_%s_%s.exe", currentOS, currentArch)),
					BrowserDownloadURL: stringPtr(fmt.Sprintf("https://example.com/tool_%s_%s.exe", currentOS, currentArch)),
				},
			},
			repoOwner:   "test-owner",
			repoName:    "test-repo",
			expectError: true,
			errorMsg:    fmt.Sprintf("no asset found for %s_%s", currentOS, currentArch),
		},
		{
			name:        "empty asset list",
			assets:      []*github.ReleaseAsset{},
			repoOwner:   "test-owner",
			repoName:    "test-repo",
			expectError: true,
			errorMsg:    fmt.Sprintf("no asset found for %s_%s", currentOS, currentArch),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := DefaultConfig()
			require.NoError(t, err)

			asset, err := parseAsset(tc.assets, cfg, tc.repoOwner, tc.repoName)

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
				assert.Nil(t, asset)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, asset)
				assert.Equal(t, tc.expectedOS, asset.OS)
				assert.Equal(t, tc.expectedArch, asset.Arch)
				assert.Equal(t, tc.repoOwner, asset.RepoOwner)
				assert.Equal(t, tc.repoName, asset.RepoName)
				assert.NotEmpty(t, asset.Name)
				assert.NotEmpty(t, asset.DownloadURL)
				assert.True(t, strings.HasPrefix(asset.DownloadURL, "https://"))
			}
		})
	}
}

// stringPtr is a helper function to create string pointers for test data
func stringPtr(s string) *string {
	return &s
}

// createMaliciousTarGz creates a tar.gz archive with a path traversal entry.
func createMaliciousTarGz(t *testing.T, entryName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := []byte("malicious content")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: entryName,
		Mode: 0644,
		Size: int64(len(content)),
	}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// createMaliciousZip creates a zip archive with a path traversal entry.
func createMaliciousZip(t *testing.T, entryName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.Create(entryName)
	require.NoError(t, err)
	_, err = w.Write([]byte("malicious content"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestSanitizePath verifies the zip-slip protection helper.
func TestSanitizePath(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "safedest")

	tests := []struct {
		name        string
		entry       string
		expectError bool
	}{
		{"normal file", "subdir/file.txt", false},
		{"file at root", "file.txt", false},
		{"traversal with ..", "../../../etc/passwd", true},
		{"traversal mixed", "subdir/../../etc/passwd", true},
		{"absolute path entry", string(os.PathSeparator) + "etc" + string(os.PathSeparator) + "passwd", true},
		{"double dot disguised", "subdir/../../../etc/shadow", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitizePath(dest, tc.entry)
			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "path traversal attempt")
				assert.Empty(t, got)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, got)
			}
		})
	}
}

// TestUnpackerZipSlip verifies that crafted archives with traversal paths are rejected.
func TestUnpackerZipSlip(t *testing.T) {
	t.Parallel()

	maliciousEntries := []string{
		"../../../etc/passwd",
		"subdir/../../outside.txt",
	}

	for _, entry := range maliciousEntries {

		t.Run("tar.gz traversal: "+entry, func(t *testing.T) {
			t.Parallel()

			data := createMaliciousTarGz(t, entry)

			tempDir := t.TempDir()
			archivePath := filepath.Join(tempDir, "evil.tar.gz")
			require.NoError(t, os.WriteFile(archivePath, data, 0644))

			unpacker := NewUnpacker()
			destDir := filepath.Join(tempDir, "output")
			_, err := unpacker.Unpack(archivePath, destDir)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "path traversal attempt")
		})

		t.Run("zip traversal: "+entry, func(t *testing.T) {
			t.Parallel()

			data := createMaliciousZip(t, entry)

			tempDir := t.TempDir()
			archivePath := filepath.Join(tempDir, "evil.zip")
			require.NoError(t, os.WriteFile(archivePath, data, 0644))

			unpacker := NewUnpacker()
			destDir := filepath.Join(tempDir, "output")
			_, err := unpacker.Unpack(archivePath, destDir)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "path traversal attempt")
		})
	}
}

// ==================== unpack.go direct tests ====================

// machOBinary returns a minimal 64-bit Mach-O header that filetype.Match detects
// as "application/x-mach-binary".
func machOBinary() []byte {
	return []byte{
		0xcf, 0xfa, 0xed, 0xfe, // MH_MAGIC_64 (little-endian)
		0x07, 0x00, 0x00, 0x01, // CPU_TYPE_X86_64
		0x03, 0x00, 0x00, 0x00, // CPU_SUBTYPE_X86_64_ALL
		0x02, 0x00, 0x00, 0x00, // MH_EXECUTE
		0x00, 0x00, 0x00, 0x00, // ncmds
		0x00, 0x00, 0x00, 0x00, // sizeofcmds
		0x00, 0x00, 0x00, 0x00, // flags
		0x00, 0x00, 0x00, 0x00, // reserved
	}
}

// tarEntry describes a single entry for newTarStream.
type tarEntry struct {
	name    string
	content []byte
	mode    int64
	isDir   bool
}

// newTarStream builds an in-memory tar stream from the given entries.
func newTarStream(t *testing.T, entries []tarEntry) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			if e.isDir {
				mode = 0o755
			} else {
				mode = 0o644
			}
		}
		typeflag := byte(tar.TypeReg)
		if e.isDir {
			typeflag = tar.TypeDir
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     mode,
			Size:     int64(len(e.content)),
			Typeflag: typeflag,
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if len(e.content) > 0 {
			_, err := tw.Write(e.content)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return bytes.NewReader(buf.Bytes())
}

// silentBar returns a progress bar that discards all output, suitable for tests.
func silentBar() *progressbar.ProgressBar {
	return progressbar.NewOptions(-1, progressbar.OptionSetWriter(io.Discard))
}

// bzip2Compress compresses data using the system bzip2 command.
// The calling test is skipped when bzip2 is not present on the host.
func bzip2Compress(t *testing.T, data []byte) []byte {
	t.Helper()
	bzip2Bin, err := exec.LookPath("bzip2")
	if err != nil {
		t.Skip("bzip2 command not available")
	}
	var out bytes.Buffer
	cmd := exec.Command(bzip2Bin, "--compress", "--stdout")
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &out
	require.NoError(t, cmd.Run())
	return out.Bytes()
}

// createTestTarXz builds an in-memory .tar.xz containing a mock Mach-O executable.
func createTestTarXz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	require.NoError(t, err)
	tw := tar.NewWriter(xw)
	content := append(machOBinary(), make([]byte, 1000)...)
	hdr := &tar.Header{Name: "test-executable", Mode: 0o755, Size: int64(len(content))}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, xw.Close())
	return buf.Bytes()
}

// createTestZipWithExec builds an in-memory .zip containing a mock Mach-O executable.
func createTestZipWithExec(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fh := &zip.FileHeader{Name: "test-executable", Method: zip.Deflate}
	fh.SetMode(0o755)
	w, err := zw.CreateHeader(fh)
	require.NoError(t, err)
	content := append(machOBinary(), make([]byte, 1000)...)
	_, err = w.Write(content)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestUnpackTar exercises the shared unpackTar core function directly.
func TestUnpackTar(t *testing.T) {
	t.Parallel()

	t.Run("extracts regular file", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "hello.txt", content: []byte("hello world")},
		}), dest))
		got, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(got))
	})

	t.Run("creates explicit directory entry", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "mydir/", isDir: true},
			{name: "mydir/file.txt", content: []byte("inside")},
		}), dest))
		assert.DirExists(t, filepath.Join(dest, "mydir"))
		got, err := os.ReadFile(filepath.Join(dest, "mydir", "file.txt"))
		require.NoError(t, err)
		assert.Equal(t, "inside", string(got))
	})

	t.Run("creates parent directories implicitly", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "a/b/c/deep.txt", content: []byte("deep")},
		}), dest))
		got, err := os.ReadFile(filepath.Join(dest, "a", "b", "c", "deep.txt"))
		require.NoError(t, err)
		assert.Equal(t, "deep", string(got))
	})

	t.Run("preserves execute bits", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "run.sh", content: []byte("#!/bin/sh"), mode: 0o755},
		}), dest))
		info, err := os.Stat(filepath.Join(dest, "run.sh"))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode().Perm()&0o111, "execute bits should be preserved for mode 0755")
	})

	t.Run("non-executable file has no execute bits", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "config.txt", content: []byte("key=val"), mode: 0o600},
		}), dest))
		info, err := os.Stat(filepath.Join(dest, "config.txt"))
		require.NoError(t, err)
		assert.Zero(t, info.Mode().Perm()&0o111, "execute bits must not be set for mode 0600")
	})

	// Defense-in-depth: verify setuid/setgid bits from an archive are not set on
	// extracted files or directories. On typical systems non-root processes cannot
	// set these bits anyway, but we strip them explicitly via .Perm() to ensure
	// the intent is clear and to guard against privileged execution environments.
	t.Run("strips setuid and setgid bits from file", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		// 0o6755 = setuid + setgid + rwxr-xr-x
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "suid-sgid", content: []byte("data"), mode: 0o6755},
		}), dest))
		info, err := os.Stat(filepath.Join(dest, "suid-sgid"))
		require.NoError(t, err)
		assert.Zero(t, info.Mode()&os.ModeSetuid, "setuid bit must not be set on extracted file")
		assert.Zero(t, info.Mode()&os.ModeSetgid, "setgid bit must not be set on extracted file")
	})

	t.Run("strips setuid bit from directory", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "suiddir/", isDir: true, mode: 0o4755},
		}), dest))
		info, err := os.Stat(filepath.Join(dest, "suiddir"))
		require.NoError(t, err)
		assert.Zero(t, info.Mode()&os.ModeSetuid, "setuid bit must not be set on extracted directory")
	})

	t.Run("multiple files and dirs", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "bin/", isDir: true},
			{name: "bin/tool", content: []byte("binary"), mode: 0o755},
			{name: "etc/config.toml", content: []byte("k=v"), mode: 0o644},
		}), dest))
		assert.FileExists(t, filepath.Join(dest, "bin", "tool"))
		assert.FileExists(t, filepath.Join(dest, "etc", "config.toml"))
	})

	t.Run("rejects path traversal in entry name", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		err := unpackTar(newTarStream(t, []tarEntry{
			{name: "../escape.txt", content: []byte("evil")},
		}), dest)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "path traversal attempt")
		_, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt"))
		assert.True(t, os.IsNotExist(statErr), "traversal file must not have been created")
	})

	t.Run("rejects absolute path in entry name", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		// Build the tar manually: tar.Writer may normalize names, so we write the
		// header bytes directly to preserve the absolute path.
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		content := []byte("evil")
		_ = tw.WriteHeader(&tar.Header{Name: "/etc/passwd", Mode: 0o644, Size: int64(len(content))})
		_, _ = tw.Write(content)
		_ = tw.Close()
		err := unpackTar(&buf, dest)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "path traversal attempt")
	})
}

// TestUnpackTarGzDirect tests unpackTarGz directly, bypassing Unpacker.Unpack.
func TestUnpackTarGzDirect(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("hello from tar.gz")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: int64(len(content))}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	require.NoError(t, unpackTarGz(bytes.NewReader(buf.Bytes()), dest, silentBar()))
	got, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from tar.gz", string(got))
}

// TestUnpackTarXzDirect tests unpackTarXz directly, bypassing Unpacker.Unpack.
func TestUnpackTarXzDirect(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	require.NoError(t, err)
	tw := tar.NewWriter(xw)
	content := []byte("hello from tar.xz")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: int64(len(content))}))
	_, err = tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, xw.Close())

	require.NoError(t, unpackTarXz(bytes.NewReader(buf.Bytes()), dest, silentBar()))
	got, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from tar.xz", string(got))
}

// TestUnpackTarBz2Direct tests unpackTarBz2 directly using the system bzip2 command.
// The test is skipped when bzip2 is not available on the host.
func TestUnpackTarBz2Direct(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	content := []byte("hello from tar.bz2")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0o644, Size: int64(len(content))}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	compressed := bzip2Compress(t, tarBuf.Bytes())
	require.NoError(t, unpackTarBz2(bytes.NewReader(compressed), dest, silentBar()))
	got, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from tar.bz2", string(got))
}

// TestUnpackBz2Direct tests unpackBz2 (raw bzip2, no tar layer) directly.
// The test is skipped when bzip2 is not available on the host.
func TestUnpackBz2Direct(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	content := []byte("hello from raw bz2")
	compressed := bzip2Compress(t, content)
	outPath := filepath.Join(dest, "hello.txt")
	require.NoError(t, unpackBz2(bytes.NewReader(compressed), outPath, silentBar()))
	got, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Equal(t, "hello from raw bz2", string(got))
}

// TestUnpackZipDirect tests unpackZip directly, including directory entries.
func TestUnpackZipDirect(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Explicit directory entry.
	dirHdr := &zip.FileHeader{Name: "subdir/"}
	dirHdr.SetMode(os.ModeDir | 0o755)
	_, err := zw.CreateHeader(dirHdr)
	require.NoError(t, err)

	// File inside that directory.
	fileHdr := &zip.FileHeader{Name: "subdir/hello.txt", Method: zip.Deflate}
	fileHdr.SetMode(0o644)
	w, err := zw.CreateHeader(fileHdr)
	require.NoError(t, err)
	_, err = w.Write([]byte("hello from zip"))
	require.NoError(t, err)

	// File at root.
	rootHdr := &zip.FileHeader{Name: "root.txt", Method: zip.Deflate}
	rootHdr.SetMode(0o644)
	w2, err := zw.CreateHeader(rootHdr)
	require.NoError(t, err)
	_, err = w2.Write([]byte("root file"))
	require.NoError(t, err)

	require.NoError(t, zw.Close())

	require.NoError(t, unpackZip(bytes.NewReader(buf.Bytes()), dest, silentBar()))
	assert.DirExists(t, filepath.Join(dest, "subdir"))
	got, err := os.ReadFile(filepath.Join(dest, "subdir", "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from zip", string(got))
	got2, err := os.ReadFile(filepath.Join(dest, "root.txt"))
	require.NoError(t, err)
	assert.Equal(t, "root file", string(got2))
}

// TestUnpackerNoExecutableFound verifies that Unpacker.Unpack returns an error
// when no executable binary is present in the archive.
func TestUnpackerNoExecutableFound(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("just a text file, no binary")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "readme.txt", Mode: 0o644, Size: int64(len(content))}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	archivePath := filepath.Join(tempDir, "noexec.tar.gz")
	require.NoError(t, os.WriteFile(archivePath, buf.Bytes(), 0o644))

	unpacker := NewUnpacker()
	_, err = unpacker.Unpack(archivePath, filepath.Join(tempDir, "out"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no executable found in archive")
}

// TestUnpackerUnpackZip tests Unpacker.Unpack end-to-end for the .zip format.
func TestUnpackerUnpackZip(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	archivePath := filepath.Join(tempDir, "test.zip")
	require.NoError(t, os.WriteFile(archivePath, createTestZipWithExec(t), 0o644))

	unpacker := NewUnpacker()
	execPath, err := unpacker.Unpack(archivePath, filepath.Join(tempDir, "out"))
	assert.NoError(t, err)
	assert.NotEmpty(t, execPath)
	assert.FileExists(t, execPath)
}

// TestUnpackerUnpackTarXz tests Unpacker.Unpack end-to-end for the .tar.xz format.
func TestUnpackerUnpackTarXz(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	archivePath := filepath.Join(tempDir, "test.tar.xz")
	require.NoError(t, os.WriteFile(archivePath, createTestTarXz(t), 0o644))

	unpacker := NewUnpacker()
	execPath, err := unpacker.Unpack(archivePath, filepath.Join(tempDir, "out"))
	assert.NoError(t, err)
	assert.NotEmpty(t, execPath)
	assert.FileExists(t, execPath)
}
