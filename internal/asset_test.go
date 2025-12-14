package grip

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-github/v56/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
func createTestTarGz() ([]byte, error) {
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
	
	if err := tw.WriteHeader(header); err != nil {
		return nil, err
	}
	
	if _, err := tw.Write(execContent); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	
	return buf.Bytes(), nil
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
		tc := tc
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
		tarData, err := createTestTarGz()
		require.NoError(t, err)

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
		name     string
		alias    string
		repoName string
		assetName string
		expected string
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
		tc := tc
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
		name        string
		assets      []*github.ReleaseAsset
		repoOwner   string
		repoName    string
		expectError bool
		errorMsg    string
		expectedOS  string
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
			repoOwner:   "test-owner",
			repoName:    "test-repo",
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
		tc := tc
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
