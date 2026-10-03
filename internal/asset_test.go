package grip

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		assets       []ReleaseAsset
		repoOwner    string
		repoName     string
		expectError  bool
		errorMsg     string
		expectedOS   string
		expectedArch string
	}{
		{
			name: "successful parsing with matching asset",
			assets: []ReleaseAsset{
				{
					Name: "tool_windows_amd64.zip",
					URL:  "https://example.com/tool_windows_amd64.zip",
				},
				{
					Name: fmt.Sprintf("tool_%s_%s.tar.gz", currentOS, currentArch),
					URL:  fmt.Sprintf("https://example.com/tool_%s_%s.tar.gz", currentOS, currentArch),
				},
				{
					Name: "tool_linux_arm64.tar.gz",
					URL:  "https://example.com/tool_linux_arm64.tar.gz",
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
			assets: []ReleaseAsset{
				{
					Name: "tool_windows_amd64.zip",
					URL:  "https://example.com/tool_windows_amd64.zip",
				},
				{
					Name: "tool_linux_arm64.tar.gz",
					URL:  "https://example.com/tool_linux_arm64.tar.gz",
				},
			},
			repoOwner:   "test-owner",
			repoName:    "test-repo",
			expectError: true,
			errorMsg:    fmt.Sprintf("no asset found for %s_%s", currentOS, currentArch),
		},
		{
			name: "asset with unsupported extension",
			assets: []ReleaseAsset{
				{
					Name: fmt.Sprintf("tool_%s_%s.exe", currentOS, currentArch),
					URL:  fmt.Sprintf("https://example.com/tool_%s_%s.exe", currentOS, currentArch),
				},
			},
			repoOwner:   "test-owner",
			repoName:    "test-repo",
			expectError: true,
			errorMsg:    fmt.Sprintf("no asset found for %s_%s", currentOS, currentArch),
		},
		{
			name:        "empty asset list",
			assets:      []ReleaseAsset{},
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
