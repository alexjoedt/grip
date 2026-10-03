package grip

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os/exec"
	"testing"

	"github.com/schollz/progressbar/v3"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

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

// createTestTarBz2 builds an in-memory .tar.bz2 containing a mock Mach-O executable.
// The test that calls this helper is skipped when bzip2 is not available on the host.
func createTestTarBz2(t *testing.T) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	content := append(machOBinary(), make([]byte, 1000)...)
	hdr := &tar.Header{Name: "test-executable", Mode: 0o755, Size: int64(len(content))}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return bzip2Compress(t, tarBuf.Bytes())
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
