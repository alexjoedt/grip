package grip

import (
	"errors"
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

func TestSelectAsset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		goos, arch  string
		assets      []string
		want        string
		wantErr     error
		wantDropped map[string]string // asset -> stage
	}{
		{
			name: "arm is not arm64", goos: "linux", arch: "arm64",
			assets: []string{"tool_linux_arm.tar.gz", "tool_linux_arm64.tar.gz"},
			want:   "tool_linux_arm64.tar.gz", wantDropped: map[string]string{"tool_linux_arm.tar.gz": stageArch},
		},
		{
			name: "386 only as whole token", goos: "linux", arch: "386",
			assets: []string{"tool-1386_linux_amd64.tar.gz", "tool_linux_386.tar.gz"},
			want:   "tool_linux_386.tar.gz",
		},
		{
			name: "win does not match darwin", goos: "windows", arch: "amd64",
			assets:  []string{"tool_darwin_amd64.tar.gz"},
			wantErr: errNoAsset,
		},
		{
			name: "x86_64 spelling", goos: "linux", arch: "amd64",
			assets: []string{"tool-x86_64-unknown-linux-musl.tar.gz", "tool-aarch64-unknown-linux-musl.tar.gz"},
			want:   "tool-x86_64-unknown-linux-musl.tar.gz",
		},
		{
			name: "musl over gnu", goos: "linux", arch: "amd64",
			assets: []string{"tool-x86_64-unknown-linux-gnu.tar.gz", "tool-x86_64-unknown-linux-musl.tar.gz"},
			want:   "tool-x86_64-unknown-linux-musl.tar.gz", wantDropped: map[string]string{"tool-x86_64-unknown-linux-gnu.tar.gz": stageMusl},
		},
		{
			name: "android disqualifies", goos: "linux", arch: "arm64",
			assets: []string{"tool-aarch64-linux-android.tar.gz", "tool-aarch64-unknown-linux-gnu.tar.gz"},
			want:   "tool-aarch64-unknown-linux-gnu.tar.gz", wantDropped: map[string]string{"tool-aarch64-linux-android.tar.gz": stageOS},
		},
		{
			name: "darwin universal without arch", goos: "darwin", arch: "arm64",
			assets: []string{"tool_darwin_amd64.tar.gz", "tool_darwin_all.tar.gz"},
			want:   "tool_darwin_all.tar.gz",
		},
		{
			name: "exact arch over universal", goos: "darwin", arch: "arm64",
			assets: []string{"tool_darwin_all.tar.gz", "tool_darwin_arm64.tar.gz"},
			want:   "tool_darwin_arm64.tar.gz", wantDropped: map[string]string{"tool_darwin_all.tar.gz": stageExact},
		},
		{
			name: "no rosetta fallback", goos: "darwin", arch: "arm64",
			assets:  []string{"tool-x86_64-apple-darwin.tar.gz"},
			wantErr: errNoAsset,
		},
		{
			name: "linux requires arch token", goos: "linux", arch: "amd64",
			assets:  []string{"tool_linux.tar.gz"},
			wantErr: errNoAsset,
		},
		{
			name: "denylist", goos: "linux", arch: "amd64",
			assets:  []string{"tool_linux_amd64.deb", "tool_linux_amd64.tar.gz.sha256", "tool_linux_amd64.tar.gz.sig"},
			wantErr: errNoAsset,
		},
		{
			name: "plain build over variant", goos: "linux", arch: "amd64",
			assets: []string{"tool_extended_linux_amd64.tar.gz", "tool_linux_amd64.tar.gz"},
			want:   "tool_linux_amd64.tar.gz", wantDropped: map[string]string{"tool_extended_linux_amd64.tar.gz": stageVariant},
		},
		{
			name: "archive over bare binary", goos: "linux", arch: "amd64",
			assets: []string{"tool_linux_amd64", "tool_linux_amd64.zip", "tool_linux_amd64.tar.gz"},
			want:   "tool_linux_amd64.tar.gz",
		},
		{
			name: "bare binary is a candidate", goos: "linux", arch: "amd64",
			assets: []string{"tool.linux-amd64"},
			want:   "tool.linux-amd64",
		},
		{
			name: "canonical spelling over legacy alias", goos: "linux", arch: "amd64",
			assets: []string{"tool_Linux-64bit.tar.gz", "tool_linux-amd64.tar.gz"},
			want:   "tool_linux-amd64.tar.gz", wantDropped: map[string]string{"tool_Linux-64bit.tar.gz": stageSpelling},
		},
		{
			name: "tie is ambiguous", goos: "linux", arch: "amd64",
			assets:  []string{"a_linux_amd64.tar.gz", "b_linux_amd64.tar.gz"},
			wantErr: ErrAmbiguousAsset,
		},
		{
			name: "other goarch by literal name", goos: "linux", arch: "riscv64",
			assets: []string{"tool_linux_amd64.tar.gz", "tool_linux_riscv64.tar.gz"},
			want:   "tool_linux_riscv64.tar.gz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := make([]ReleaseAsset, len(tc.assets))
			for i, n := range tc.assets {
				assets[i] = ReleaseAsset{Name: n, URL: "https://example.com/" + n}
			}

			got, cands, err := selectAsset(assets, tc.goos, tc.arch)
			switch {
			case tc.wantErr == errNoAsset:
				require.Error(t, err)
				assert.NotErrorIs(t, err, ErrAmbiguousAsset)
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
			default:
				require.NoError(t, err)
				assert.Equal(t, tc.want, got.Name)
			}
			assert.Len(t, cands, len(tc.assets))
			for _, c := range cands {
				if stage, ok := tc.wantDropped[c.name]; ok {
					assert.Equal(t, stage, c.stage, c.name)
				}
			}
		})
	}
}

// errNoAsset marks a TestSelectAsset case that expects no candidate at all.
var errNoAsset = errors.New("no asset")

func TestParseAssetKeepsPublishedCase(t *testing.T) {
	cfg := &Config{OS: "linux", Arch: "amd64"}
	assets := []ReleaseAsset{{Name: "Tool_Linux_x86_64.tar.gz", URL: "https://example.com/Tool_Linux_x86_64.tar.gz"}}

	asset, err := parseAsset(assets, cfg, "owner", "tool", "")
	require.NoError(t, err)
	assert.Equal(t, "Tool_Linux_x86_64.tar.gz", asset.Name)
	assert.Equal(t, "https://example.com/Tool_Linux_x86_64.tar.gz", asset.DownloadURL)
}

func TestParseAssetPattern(t *testing.T) {
	cfg := &Config{OS: "linux", Arch: "amd64"}
	var assets []ReleaseAsset
	for _, n := range []string{"tool_linux_amd64.tar.gz", "tool_linux_amd64_musl.tar.gz", "tool_linux_arm64.tar.gz", "tool.deb", "checksums.txt"} {
		assets = append(assets, ReleaseAsset{Name: n, URL: "https://example.com/" + n})
	}

	tests := []struct {
		pattern, want, wantErr string
		anyArch                bool
	}{
		{pattern: "tool_linux_arm64.tar.gz", want: "tool_linux_arm64.tar.gz", anyArch: true},
		{pattern: "*.deb", want: "tool.deb", anyArch: true},
		{pattern: "tool_linux_*", want: "tool_linux_amd64_musl.tar.gz"},
		{pattern: "*.zip", wantErr: `asset override "*.zip" matches no release asset: tool_linux_amd64.tar.gz, `},
		{pattern: "[", wantErr: "syntax error in pattern"},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, err := parseAsset(assets, cfg, "owner", "tool", tt.pattern)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Name)
			assert.Equal(t, tt.anyArch, got.AnyArch)
		})
	}
}

func TestVersionGlob(t *testing.T) {
	tests := []struct{ name, tag, want string }{
		{"tool_1.2.3_linux_amd64.tar.gz", "v1.2.3", "tool_*_linux_amd64.tar.gz"},
		{"tool-v1.2.3-linux.tar.gz", "v1.2.3", "tool-*-linux.tar.gz"},
		{"kustomize_v5.8.2_linux_amd64.tar.gz", "kustomize/v5.8.2", "kustomize_*_linux_amd64.tar.gz"},
		{"jq-1.8.2-linux-amd64", "jq-1.8.2", "*-linux-amd64"},
		{"jq_1.8.2_linux_amd64.tar.gz", "jq-1.8.2", "jq_*_linux_amd64.tar.gz"},
		{"jq-linux-amd64", "jq-1.8.2", "jq-linux-amd64"},
		{"uv-x86_64-unknown-linux-gnu.tar.gz", "0.9.1", "uv-x86_64-unknown-linux-gnu.tar.gz"},
		{"tool_linux.tar.gz", "nightly", "tool_linux.tar.gz"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, versionGlob(tt.name, tt.tag), tt.name)
	}
}
