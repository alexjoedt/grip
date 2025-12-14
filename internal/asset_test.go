package grip

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

// MockHTTPClient is a mock implementation of HTTPClient
type MockHTTPClient struct {
	mock.Mock
}

func (m *MockHTTPClient) Get(url string) (*http.Response, error) {
	args := m.Called(url)
	if resp := args.Get(0); resp != nil {
		return resp.(*http.Response), args.Error(1)
	}
	return nil, args.Error(1)
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



// createTestAsset creates a test asset with optional HTTP client injection
func createTestAsset(t *testing.T,name, downloadURL string, httpClient HTTPClient) *Asset {
	return &Asset{
		Name:        name,
		DownloadURL: downloadURL,
		repoName:    "test-repo",
		Alias:       "test-alias",
		httpClient: httpClient,
	}
}

func TestInit(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		assetName   string
		expectError bool
		errorType   error
	}{
		{
			name:        "create valid asset",
			assetName:   "Asset-1",
			expectError: false,
		},
		{
			name:        "create asset with empty name",
			assetName:   "",
			expectError: true,
			errorType:   ErrInvalidAsset,
		},
		{
			name:        "create asset with whitespace name",
			assetName:   "   ",
			expectError: false, // whitespace is valid but not recommended
		},
		{
			name:        "create asset with special characters",
			assetName:   "Asset@#$%",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			asset := createTestAsset(t,tc.assetName, "https://example.com/test.tar.gz", nil)
			err := asset.init()

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorType != nil {
					assert.ErrorIs(t, err, tc.errorType)
				}
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, asset.tempDir)
				assert.DirExists(t, asset.tempDir)
				assert.DirExists(t, filepath.Join(asset.tempDir, "unpack"))
				assert.DirExists(t, filepath.Join(asset.tempDir, "download"))

				// Check directory permissions
				info, err := os.Stat(filepath.Join(asset.tempDir, "download"))
				assert.NoError(t, err)
				assert.True(t, info.IsDir())
				
				info, err = os.Stat(filepath.Join(asset.tempDir, "unpack"))
				assert.NoError(t, err)
				assert.True(t, info.IsDir())
			}

			// Cleanup
			t.Cleanup(func() {
				if asset.tempDir != "" {
					_ = os.RemoveAll(asset.tempDir)
				}
			})
		})
	}
}

func TestDownload(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		assetName      string
		downloadURL    string
		mockSetup      func(*MockHTTPClient)
		expectError    bool
		expectedErrMsg string
	}{
		{
			name:        "successful download",
			assetName:   "test.tar.gz",
			downloadURL: "https://example.com/test.tar.gz",
			mockSetup: func(m *MockHTTPClient) {
				resp := createMockResponse(200, "test file content", 17)
				m.On("Get", "https://example.com/test.tar.gz").Return(resp, nil)
			},
			expectError: false,
		},
		{
			name:        "HTTP 404 error",
			assetName:   "missing.tar.gz",
			downloadURL: "https://example.com/missing.tar.gz",
			mockSetup: func(m *MockHTTPClient) {
				resp := createMockResponse(404, "Not Found", 9)
				m.On("Get", "https://example.com/missing.tar.gz").Return(resp, nil)
			},
			expectError:    true,
			expectedErrMsg: "invalid response with status",
		},
		{
			name:        "HTTP 500 error",
			assetName:   "error.tar.gz",
			downloadURL: "https://example.com/error.tar.gz",
			mockSetup: func(m *MockHTTPClient) {
				resp := createMockResponse(500, "Internal Server Error", 21)
				m.On("Get", "https://example.com/error.tar.gz").Return(resp, nil)
			},
			expectError:    true,
			expectedErrMsg: "invalid response with status",
		},
		{
			name:        "network error",
			assetName:   "network.tar.gz",
			downloadURL: "https://example.com/network.tar.gz",
			mockSetup: func(m *MockHTTPClient) {
				m.On("Get", "https://example.com/network.tar.gz").Return(nil, fmt.Errorf("connection refused"))
			},
			expectError:    true,
			expectedErrMsg: "connection refused",
		},
		{
			name:        "empty URL",
			assetName:   "empty.tar.gz",
			downloadURL: "",
			mockSetup: func(m *MockHTTPClient) {
				m.On("Get", "").Return(nil, fmt.Errorf("invalid URL"))
			},
			expectError:    true,
			expectedErrMsg: "invalid URL",
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockClient := new(MockHTTPClient)
			tc.mockSetup(mockClient)

			asset := createTestAsset(t,tc.assetName, tc.downloadURL, mockClient)
			
			// Initialize the asset first
			require.NoError(t, asset.init())

			err := asset.download()

			if tc.expectError {
				assert.Error(t, err)
				if tc.expectedErrMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedErrMsg)
				}
			} else {
				assert.NoError(t, err)
				// Check that file was created
				downloadPath := filepath.Join(asset.tempDir, "download", asset.Name)
				assert.FileExists(t, downloadPath)
				
				// Check file content
				content, err := os.ReadFile(downloadPath)
				assert.NoError(t, err)
				assert.Equal(t, "test file content", string(content))
			}

			// Verify mock expectations
			mockClient.AssertExpectations(t)

			// Cleanup
			t.Cleanup(func() {
				if asset.tempDir != "" {
					_ = os.RemoveAll(asset.tempDir)
				}
			})
		})
	}
}

func TestUnpack(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		assetName   string
		setupMock   func(*MockHTTPClient) []byte
		expectError bool
		errorMsg    string
	}{
		{
			name:      "successful unpack tar.gz",
			assetName: "test.tar.gz",
			setupMock: func(m *MockHTTPClient) []byte {
				// Create test tar.gz content
				tarData, err := createTestTarGz()
				require.NoError(t, err)
				
				resp := createMockResponse(200, string(tarData), int64(len(tarData)))
				m.On("Get", "https://example.com/test.tar.gz").Return(resp, nil)
				return tarData
			},
			expectError: false,
		},
		{
			name:      "invalid archive format",
			assetName: "test.invalid",
			setupMock: func(m *MockHTTPClient) []byte {
				invalidData := []byte("not an archive")
				resp := createMockResponse(200, string(invalidData), int64(len(invalidData)))
				m.On("Get", "https://example.com/test.invalid").Return(resp, nil)
				return invalidData
			},
			expectError: true,
			errorMsg:    "unsupported extension",
		},
		{
			name:      "corrupted archive",
			assetName: "test.tar.gz",
			setupMock: func(m *MockHTTPClient) []byte {
				corruptData := []byte("corrupted gzip data")
				resp := createMockResponse(200, string(corruptData), int64(len(corruptData)))
				m.On("Get", "https://example.com/test.tar.gz").Return(resp, nil)
				return corruptData
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockClient := new(MockHTTPClient)
			testData := tc.setupMock(mockClient)

			asset := createTestAsset(t,tc.assetName, "https://example.com/"+tc.assetName, mockClient)
			
			// Initialize and download first
			require.NoError(t, asset.init())
			require.NoError(t, asset.download())

			err := asset.unpack()

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				
				// Check that unpack directory exists and has content
				unpackDir := filepath.Join(asset.tempDir, "unpack")
				assert.DirExists(t, unpackDir)
				
				entries, err := os.ReadDir(unpackDir)
				assert.NoError(t, err)
				assert.NotEmpty(t, entries, "unpack directory should contain extracted files")
				
				// Verify download file still exists
				downloadPath := filepath.Join(asset.tempDir, "download", asset.Name)
				assert.FileExists(t, downloadPath)
				
				// Verify downloaded content matches expected
				downloadedData, err := os.ReadFile(downloadPath)
				assert.NoError(t, err)
				assert.Equal(t, testData, downloadedData)
			}

			// Verify mock expectations
			mockClient.AssertExpectations(t)

			// Cleanup
			t.Cleanup(func() {
				if asset.tempDir != "" {
					_ = os.RemoveAll(asset.tempDir)
				}
			})
		})
	}
}

func TestInstall(t *testing.T) {
	testCases := []struct {
		name        string
		assetName   string
		installPath string
		repoName    string
		alias       string
		setupMock   func(*MockHTTPClient)
		expectError bool
		expectedErr error
		errorMsg    string
	}{
		{
			name:        "successful install with absolute path",
			assetName:   "grip_Darwin_arm64.tar.gz",
			installPath: filepath.Join(os.TempDir(), "test-install-1"),
			repoName:    "grip",
			alias:       "grip",
			setupMock: func(m *MockHTTPClient) {
				tarData, err := createTestTarGz()
				require.NoError(t, err)
				resp := createMockResponse(200, string(tarData), int64(len(tarData)))
				m.On("Get", "https://example.com/grip_Darwin_arm64.tar.gz").Return(resp, nil)
			},
			expectError: false, // Changed: Install should succeed with proper Mach-O binary
		},
		{
			name:        "install with empty path",
			assetName:   "grip_Darwin_arm64.tar.gz",
			installPath: "",
			repoName:    "grip",
			alias:       "grip",
			setupMock:   func(m *MockHTTPClient) {},
			expectError: true,
			expectedErr: ErrNoInstallPath,
		},
		{
			name:        "install with relative path",
			assetName:   "grip_Darwin_arm64.tar.gz",
			installPath: "temp",
			repoName:    "grip",
			alias:       "grip",
			setupMock:   func(m *MockHTTPClient) {},
			expectError: true,
			expectedErr: ErrNoAbsolutePath,
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			mockClient := new(MockHTTPClient)
			tc.setupMock(mockClient)

			// Create the install directory if specified and valid
			if tc.installPath != "" && filepath.IsAbs(tc.installPath) {
				err := os.MkdirAll(tc.installPath, 0755)
				require.NoError(t, err)
			}

			// Create asset with all required fields
			asset := &Asset{
				Name:        tc.assetName,
				DownloadURL: "https://example.com/" + tc.assetName,
				repoName:    tc.repoName,
				Alias:       tc.alias,
				httpClient:  mockClient,
			}

			err := asset.Install(tc.installPath)

			if tc.expectError {
				assert.Error(t, err)
				if tc.expectedErr != nil {
					assert.ErrorIs(t, err, tc.expectedErr)
				}
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				
				// Check that binary was installed
				expectedBinaryPath := filepath.Join(tc.installPath, asset.BinaryName())
				assert.FileExists(t, expectedBinaryPath)
				
				// Check binary permissions
				info, err := os.Stat(expectedBinaryPath)
				assert.NoError(t, err)
				assert.True(t, info.Mode().Perm() >= 0755, "binary should have executable permissions")
			}

			// Verify mock expectations
			mockClient.AssertExpectations(t)

			// Cleanup
			t.Cleanup(func() {
				if tc.installPath != "" && filepath.IsAbs(tc.installPath) {
					_ = os.RemoveAll(tc.installPath)
				}
				// Asset should clean up temp directory automatically
			})
		})
	}
}

// TestAssetBinaryName tests the BinaryName method
func TestAssetBinaryName(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		alias    string
		repoName string
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
			name:     "empty alias",
			alias:    "",
			repoName: "test-repo",
			expected: "test-repo",
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			asset := &Asset{
				Alias:    tc.alias,
				repoName: tc.repoName,
			}

			result := asset.BinaryName()
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestAssetClean tests the clean method
func TestAssetClean(t *testing.T) {
	t.Parallel()

	asset := createTestAsset(t,"test-clean", "https://example.com/test", nil)
	
	// Initialize to create temp directory
	require.NoError(t, asset.init())
	require.DirExists(t, asset.tempDir)

	// Clean should remove temp directory
	err := asset.clean()
	assert.NoError(t, err)
	assert.NoDirExists(t, asset.tempDir)
}

// TestParseAsset tests the parseAsset function
func TestParseAsset(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		assets      []*github.ReleaseAsset
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
			expectError: true,
			errorMsg:    fmt.Sprintf("no asset found for %s_%s", currentOS, currentArch),
		},
		{
			name: "matching asset with OS alias",
			assets: []*github.ReleaseAsset{
				{
					Name:               stringPtr("tool_macos_arm64.tar.gz"), // macos is alias for darwin
					BrowserDownloadURL: stringPtr("https://example.com/tool_macos_arm64.tar.gz"),
				},
			},
			expectError:  currentOS != "darwin", // Should only work on darwin systems
			expectedOS:   currentOS,
			expectedArch: currentArch,
		},
		{
			name: "matching asset with arch alias",
			assets: []*github.ReleaseAsset{
				{
					Name:               stringPtr(fmt.Sprintf("tool_%s_x86_64.tar.gz", currentOS)), // x86_64 is alias for amd64
					BrowserDownloadURL: stringPtr(fmt.Sprintf("https://example.com/tool_%s_x86_64.tar.gz", currentOS)),
				},
			},
			expectError:  currentArch != "amd64", // Should only work on amd64 systems
			expectedOS:   currentOS,
			expectedArch: currentArch,
		},
		{
			name:        "empty asset list",
			assets:      []*github.ReleaseAsset{},
			expectError: true,
			errorMsg:    fmt.Sprintf("no asset found for %s_%s", currentOS, currentArch),
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Create test config
			cfg, err := DefaultConfig()
			require.NoError(t, err)

			asset, err := parseAsset(tc.assets, cfg)

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
