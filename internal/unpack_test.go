package grip

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"debug/macho"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

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
		destDir := filepath.Join(tempDir, "output")
		execPath, err := Unpack(archivePath, destDir, darwinAmd64)

		assert.NoError(t, err)
		assert.NotEmpty(t, execPath)
		assert.FileExists(t, execPath)
	})

	t.Run("neither archive nor executable", func(t *testing.T) {
		t.Parallel()

		tempDir := filepath.Join(os.TempDir(), "test-unpack-invalid")
		require.NoError(t, os.MkdirAll(tempDir, 0755))
		defer os.RemoveAll(tempDir)

		archivePath := filepath.Join(tempDir, "test.txt")
		require.NoError(t, os.WriteFile(archivePath, []byte("not an archive"), 0644))

		destDir := filepath.Join(tempDir, "output")
		_, err := Unpack(archivePath, destDir, darwinAmd64)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "test.txt is neither a supported archive nor an executable for darwin/amd64")
	})

	t.Run("IsSupportedFormat", func(t *testing.T) {
		t.Parallel()

		assert.True(t, IsSupportedFormat("test.tar.gz"))
		assert.True(t, IsSupportedFormat("test.zip"))
		assert.True(t, IsSupportedFormat("test.tar.bz2"))
		assert.False(t, IsSupportedFormat("test.txt"))
		assert.False(t, IsSupportedFormat("test.exe"))
	})
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

			destDir := filepath.Join(tempDir, "output")
			_, err := Unpack(archivePath, destDir, darwinAmd64)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "escapes")
			assert.NoFileExists(t, filepath.Join(tempDir, "outside.txt"))
		})

		t.Run("zip traversal: "+entry, func(t *testing.T) {
			t.Parallel()

			data := createMaliciousZip(t, entry)

			tempDir := t.TempDir()
			archivePath := filepath.Join(tempDir, "evil.zip")
			require.NoError(t, os.WriteFile(archivePath, data, 0644))

			destDir := filepath.Join(tempDir, "output")
			_, err := Unpack(archivePath, destDir, darwinAmd64)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "escapes")
			assert.NoFileExists(t, filepath.Join(tempDir, "outside.txt"))
		})
	}
}

// TestUnpackTar exercises the shared unpackTar core function directly.
func TestUnpackTar(t *testing.T) {
	t.Parallel()

	t.Run("extracts regular file", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "hello.txt", content: []byte("hello world")},
		}), openRoot(t, dest)))
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
		}), openRoot(t, dest)))
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
		}), openRoot(t, dest)))
		got, err := os.ReadFile(filepath.Join(dest, "a", "b", "c", "deep.txt"))
		require.NoError(t, err)
		assert.Equal(t, "deep", string(got))
	})

	t.Run("preserves execute bits", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "run.sh", content: []byte("#!/bin/sh"), mode: 0o755},
		}), openRoot(t, dest)))
		info, err := os.Stat(filepath.Join(dest, "run.sh"))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode().Perm()&0o111, "execute bits should be preserved for mode 0755")
	})

	t.Run("non-executable file has no execute bits", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "config.txt", content: []byte("key=val"), mode: 0o600},
		}), openRoot(t, dest)))
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
		}), openRoot(t, dest)))
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
		}), openRoot(t, dest)))
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
		}), openRoot(t, dest)))
		assert.FileExists(t, filepath.Join(dest, "bin", "tool"))
		assert.FileExists(t, filepath.Join(dest, "etc", "config.toml"))
	})

	t.Run("rejects path traversal in entry name", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		err := unpackTar(newTarStream(t, []tarEntry{
			{name: "../escape.txt", content: []byte("evil")},
		}), openRoot(t, dest))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "escapes")
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
		err := unpackTar(&buf, openRoot(t, dest))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "escapes")
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

	require.NoError(t, unpackTarGz(writeArchive(t, "a.tar.gz", buf.Bytes()), openRoot(t, dest), silentBar()))
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

	require.NoError(t, unpackTarXz(writeArchive(t, "a.tar.xz", buf.Bytes()), openRoot(t, dest), silentBar()))
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
	require.NoError(t, unpackTarBz2(writeArchive(t, "a.tar.bz2", compressed), openRoot(t, dest), silentBar()))
	got, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from tar.bz2", string(got))
}

// TestUnpackBz2Direct tests the single-file bzip2 unpacker (no tar layer) directly.
// The test is skipped when bzip2 is not available on the host.
func TestUnpackBz2Direct(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()

	content := []byte("hello from raw bz2")
	compressed := bzip2Compress(t, content)
	require.NoError(t, unpackers[".bz2"](writeArchive(t, "hello.txt.bz2", compressed), openRoot(t, dest), silentBar()))
	got, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
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

	require.NoError(t, unpackZip(writeArchive(t, "a.zip", buf.Bytes()), openRoot(t, dest), silentBar()))
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

	_, err = Unpack(archivePath, filepath.Join(tempDir, "out"), darwinAmd64)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no executable found in archive")
}

// TestUnpackerUnpackZip tests Unpacker.Unpack end-to-end for the .zip format.
func TestUnpackerUnpackZip(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	archivePath := filepath.Join(tempDir, "test.zip")
	require.NoError(t, os.WriteFile(archivePath, createTestZipWithExec(t), 0o644))

	execPath, err := Unpack(archivePath, filepath.Join(tempDir, "out"), darwinAmd64)
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

	execPath, err := Unpack(archivePath, filepath.Join(tempDir, "out"), darwinAmd64)
	assert.NoError(t, err)
	assert.NotEmpty(t, execPath)
	assert.FileExists(t, execPath)
}

// TestUnpackerUnpackTarBz2 tests Unpacker.Unpack end-to-end for the .tar.bz2 format.
// The test is skipped when bzip2 is not available on the host.
func TestUnpackerUnpackTarBz2(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	archivePath := filepath.Join(tempDir, "test.tar.bz2")
	require.NoError(t, os.WriteFile(archivePath, createTestTarBz2(t), 0o644))

	execPath, err := Unpack(archivePath, filepath.Join(tempDir, "out"), darwinAmd64)
	assert.NoError(t, err)
	assert.NotEmpty(t, execPath)
	assert.FileExists(t, execPath)
}

// TestUnpackModes verifies modes are normalized to 0755 or 0644 in tar and zip.
func TestUnpackModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   int64
		want os.FileMode
	}{
		{0o755, 0o755},
		{0o700, 0o755},
		{0o100, 0o755},
		{0o600, 0o644},
		{0o666, 0o644},
		{0o6755, 0o755},
		{0o1777, 0o755},
		{0o4644, 0o644},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("tar %o", tc.in), func(t *testing.T) {
			t.Parallel()
			dest := t.TempDir()
			require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
				{name: "f", content: []byte("x"), mode: tc.in},
			}), openRoot(t, dest)))
			info, err := os.Stat(filepath.Join(dest, "f"))
			require.NoError(t, err)
			assert.Equal(t, tc.want, info.Mode())
		})

		t.Run(fmt.Sprintf("zip %o", tc.in), func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			hdr := &zip.FileHeader{Name: "f"}
			mode := os.FileMode(tc.in).Perm()
			if tc.in&0o4000 != 0 {
				mode |= os.ModeSetuid
			}
			if tc.in&0o2000 != 0 {
				mode |= os.ModeSetgid
			}
			if tc.in&0o1000 != 0 {
				mode |= os.ModeSticky
			}
			hdr.SetMode(mode)
			w, err := zw.CreateHeader(hdr)
			require.NoError(t, err)
			_, err = w.Write([]byte("x"))
			require.NoError(t, err)
			require.NoError(t, zw.Close())

			dest := t.TempDir()
			require.NoError(t, unpackZip(writeArchive(t, "a.zip", buf.Bytes()), openRoot(t, dest), silentBar()))
			info, err := os.Stat(filepath.Join(dest, "f"))
			require.NoError(t, err)
			assert.Equal(t, tc.want, info.Mode())
		})
	}
}

// TestUnpackSkipsLinks verifies symlink and hardlink entries never reach disk.
func TestUnpackSkipsLinks(t *testing.T) {
	t.Parallel()

	t.Run("tar", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.NoError(t, unpackTar(newTarStream(t, []tarEntry{
			{name: "tool", content: []byte("bin"), mode: 0o755},
			{name: "sym", link: tar.TypeSymlink, target: "/etc/passwd"},
			{name: "hard", link: tar.TypeLink, target: "tool"},
		}), openRoot(t, dest)))
		assert.FileExists(t, filepath.Join(dest, "tool"))
		for _, name := range []string{"sym", "hard"} {
			_, err := os.Lstat(filepath.Join(dest, name))
			assert.True(t, os.IsNotExist(err), "%s must not exist", name)
		}
	})

	t.Run("zip", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		hdr := &zip.FileHeader{Name: "sym"}
		hdr.SetMode(os.ModeSymlink | 0o777)
		w, err := zw.CreateHeader(hdr)
		require.NoError(t, err)
		_, err = w.Write([]byte("/etc/passwd"))
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		dest := t.TempDir()
		require.NoError(t, unpackZip(writeArchive(t, "a.zip", buf.Bytes()), openRoot(t, dest), silentBar()))
		_, err = os.Lstat(filepath.Join(dest, "sym"))
		assert.True(t, os.IsNotExist(err))
	})
}

func TestExecutableFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content []byte
		target  string
		want    bool
	}{
		{"elf amd64 on linux/amd64", elfFor(elf.EM_X86_64, elf.ET_EXEC), "linux/amd64", true},
		{"elf pie on linux/amd64", elfFor(elf.EM_X86_64, elf.ET_DYN), "linux/amd64", true},
		{"elf arm64 on linux/arm64", elfFor(elf.EM_AARCH64, elf.ET_EXEC), "linux/arm64", true},
		{"elf amd64 on linux/arm64", elfFor(elf.EM_X86_64, elf.ET_EXEC), "linux/arm64", false},
		{"elf object file", elfFor(elf.EM_X86_64, elf.ET_REL), "linux/amd64", false},
		{"mach-o on linux", machOFor(macho.CpuAmd64, macho.TypeExec), "linux/amd64", false},
		{"mach-o arm64 on darwin/arm64", machOFor(macho.CpuArm64, macho.TypeExec), "darwin/arm64", true},
		{"mach-o amd64 on darwin/arm64", machOFor(macho.CpuAmd64, macho.TypeExec), "darwin/arm64", false},
		{"mach-o dylib", machOFor(macho.CpuArm64, macho.TypeDylib), "darwin/arm64", false},
		{"universal on darwin/arm64", fatBinary(macho.CpuAmd64, macho.CpuArm64), "darwin/arm64", true},
		{"universal amd64 only on darwin/arm64", fatBinary(macho.CpuAmd64), "darwin/arm64", false},
		{"elf on darwin", elfFor(elf.EM_AARCH64, elf.ET_EXEC), "darwin/arm64", false},
		{"script on linux", []byte("#!/bin/sh\necho hi\n"), "linux/amd64", false},
		{"script on darwin", []byte("#!/bin/sh\necho hi\n"), "darwin/arm64", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "bin")
			require.NoError(t, os.WriteFile(p, tt.content, 0o755))
			goos, goarch, _ := strings.Cut(tt.target, "/")
			assert.Equal(t, tt.want, executableFor(p, goos, goarch))
		})
	}
}

func TestSelectBinary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cands    []string
		override string
		names    []string
		want     string
		wantErr  string
	}{
		{"none", nil, "", []string{"tool"}, "", "no executable found"},
		{"single", []string{"dir/rg"}, "", []string{"ripgrep"}, "dir/rg", ""},
		{"install name wins", []string{"tool", "tool-daemon"}, "", []string{"tool", "repo"}, "tool", ""},
		{"repository name second", []string{"uv", "uvx"}, "", []string{"myuv", "uv"}, "uv", ""},
		{"no name matches", []string{"a", "b"}, "", []string{"tool"}, "", "several executables match: a, b"},
		{"duplicate base name", []string{"x/tool", "y/tool"}, "", []string{"tool"}, "", "several executables match"},
		{"override wins", []string{"tool", "helper"}, "helper", []string{"tool"}, "helper", ""},
		{"override misses single", []string{"tool"}, "other", []string{"tool"}, "", `bin override "other" names no single executable in archive: tool`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := selectBinary(tt.cands, tt.override, tt.names...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := map[string][]byte{
		"pkg/age":        elfFor(elf.EM_X86_64, elf.ET_EXEC),
		"pkg/age-keygen": elfFor(elf.EM_X86_64, elf.ET_EXEC),
		"pkg/age-arm":    elfFor(elf.EM_AARCH64, elf.ET_EXEC),
		"pkg/install.sh": []byte("#!/bin/sh\n"),
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, content, 0o755))
	}

	got, err := findBinary(dir, binaryQuery{OS: "linux", Arch: "amd64", Names: []string{"myage", "age"}})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "pkg/age"), got)

	_, err = findBinary(dir, binaryQuery{OS: "linux", Arch: "amd64", Names: []string{"x"}})
	require.ErrorIs(t, err, ErrAmbiguousBinary)
	assert.ErrorContains(t, err, "age, age-keygen")

	_, err = findBinary(dir, binaryQuery{OS: "linux", Arch: "386", Names: []string{"age"}})
	require.ErrorContains(t, err, "no executable found")
	got, err = findBinary(dir, binaryQuery{OS: "linux", Arch: "386", Names: []string{"age"}, AnyArch: true})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "pkg/age"), got)
}

// lowerExtractLimits sets the extraction limits for one non-parallel test.
func lowerExtractLimits(t *testing.T, bytes, entries int64) {
	t.Helper()
	oldBytes, oldEntries := maxExtractBytes, maxExtractEntries
	maxExtractBytes, maxExtractEntries = bytes, entries
	t.Cleanup(func() { maxExtractBytes, maxExtractEntries = oldBytes, oldEntries })
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		require.NoError(t, err)
		_, err = w.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestUnpackLimits(t *testing.T) {
	lowerExtractLimits(t, 1000, 5)
	big := append(machOBinary(), make([]byte, 2000)...)
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	_, err := gw.Write(big)
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	many := map[string][]byte{}
	for i := range 6 {
		many[fmt.Sprintf("f%d", i)] = []byte("x")
	}
	split := map[string][]byte{"a": make([]byte, 600), "b": make([]byte, 600)}

	tests := map[string]struct {
		name string
		data []byte
	}{
		"tar.gz bytes":         {"t.tar.gz", tarGzOf(t, map[string][]byte{"tool": big})},
		"tar.gz bytes summed":  {"t.tar.gz", tarGzOf(t, split)},
		"tar.gz entries":       {"t.tar.gz", tarGzOf(t, many)},
		"zip bytes":            {"t.zip", zipOf(t, map[string][]byte{"tool": big})},
		"zip bytes summed":     {"t.zip", zipOf(t, split)},
		"zip entries":          {"t.zip", zipOf(t, many)},
		"single-file gz bytes": {"tool_darwin_amd64.gz", gz.Bytes()},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			_, err := Unpack(writeArchive(t, tt.name, tt.data), dest, darwinAmd64)
			require.ErrorIs(t, err, ErrArchiveTooLarge)
			assert.LessOrEqual(t, dirBytes(t, dest), maxExtractBytes+1)
		})
	}

	t.Run("within limits", func(t *testing.T) {
		_, err := Unpack(writeArchive(t, "t.tar.gz", tarGzOf(t, map[string][]byte{"tool": machOBinary()})), t.TempDir(), darwinAmd64)
		require.NoError(t, err)
	})
}

// TestUnpackZipUnderstatedSize checks that a zip entry declaring fewer bytes
// than it holds cannot write past the limit.
func TestUnpackZipUnderstatedSize(t *testing.T) {
	lowerExtractLimits(t, 1000, 100)
	data := make([]byte, 5000)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               "tool",
		Method:             zip.Store,
		CompressedSize64:   uint64(len(data)),
		UncompressedSize64: 10,
	})
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	dest := t.TempDir()
	_, err = Unpack(writeArchive(t, "t.zip", buf.Bytes()), dest, darwinAmd64)
	require.Error(t, err)
	assert.LessOrEqual(t, dirBytes(t, dest), maxExtractBytes+1)
}

func dirBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		n += fi.Size()
		return err
	}))
	return n
}
