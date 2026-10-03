package grip

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/h2non/filetype"
	"github.com/schollz/progressbar/v3"
	"github.com/ulikunitz/xz"
)

type unpackFn func(archivePath string, root *os.Root, bar *progressbar.ProgressBar) error

var unpackers = map[string]unpackFn{
	".tar.gz":  unpackTarGz,
	".tgz":     unpackTarGz,
	".tar.bz2": unpackTarBz2,
	".tbz":     unpackTarBz2,
	".zip":     unpackZip,
	".tar.xz":  unpackTarXz,
	".bz2":     unpackBz2,
}

// orderedExts lists supported archive extensions sorted by descending length
// so that longer suffixes (e.g. .tar.bz2) are matched before shorter ones (e.g. .bz2).
var orderedExts = []string{
	".tar.bz2",
	".tar.gz",
	".tgz",
	".tar.xz",
	".tbz",
	".zip",
	".bz2",
}

// Unpack extracts an archive file to the destination directory.
// Returns the path to the executable binary found in the archive.
func Unpack(archivePath, destDir string) (string, error) {
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return "", fmt.Errorf("stat archive: %w", err)
	}

	_, fn, err := getUnpackFn(archivePath)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}

	root, err := os.OpenRoot(destDir)
	if err != nil {
		return "", fmt.Errorf("open destination directory: %w", err)
	}
	bar := NewProgressBar(int(archiveInfo.Size()), "[cyan][2/3][reset] Unpacking")
	if err := errors.Join(fn(archivePath, root, bar), root.Close()); err != nil {
		return "", fmt.Errorf("unpack archive: %w", err)
	}
	fmt.Println() // new line after progress bar

	execPath, err := findExecutable(destDir)
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}

	return execPath, nil
}

// IsSupportedFormat reports whether filename has a supported archive extension.
func IsSupportedFormat(filename string) bool {
	filename = strings.ToLower(filename)
	for _, ext := range orderedExts {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}
	return false
}

// getUnpackFn returns the appropriate unpacker function for the filename.
func getUnpackFn(filename string) (string, unpackFn, error) {
	filename = strings.ToLower(filename)
	for _, ext := range orderedExts {
		if strings.HasSuffix(filename, ext) {
			return ext, unpackers[ext], nil
		}
	}
	return "", nil, fmt.Errorf("unsupported archive format: %s", filename)
}

// findExecutable searches for an executable binary in the directory tree.
func findExecutable(dir string) (string, error) {
	fileTypes := map[string]bool{
		"application/x-mach-binary": true,
		"application/x-executable":  true,
	}

	var executablePath string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			mimeType, err := detectFileType(path)
			if err != nil {
				// Continue searching on error
				return nil
			}

			if fileTypes[mimeType] {
				executablePath = path
				return filepath.SkipAll // Stop walking
			}
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if executablePath == "" {
		return "", errors.New("no executable found in archive")
	}

	return executablePath, nil
}

// detectFileType detects the MIME type of a file.
func detectFileType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buffer := make([]byte, 261)
	n, err := f.Read(buffer)
	if err != nil && err != io.EOF {
		return "", err
	}

	kind, err := filetype.Match(buffer[:n])
	if err != nil {
		return "", err
	}

	return kind.MIME.Value, nil
}

// fileMode maps an archive mode to 0755 when any exec bit is set, else 0644,
// so setuid, setgid and sticky bits never reach disk.
func fileMode(m os.FileMode) os.FileMode {
	if m&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// writeFile creates name inside root with mode and copies r into it.
func writeFile(root *os.Root, name string, mode os.FileMode, r io.Reader) error {
	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// unpackTar iterates over a tar stream and extracts entries into root.
// r should already be a decompressed reader (the caller handles decompression).
func unpackTar(r io.Reader, root *os.Root) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(header.Name, 0o755); err != nil {
				return fmt.Errorf("extract %q: %w", header.Name, err)
			}
		case tar.TypeReg:
			if err := writeFile(root, header.Name, fileMode(os.FileMode(header.Mode)), tr); err != nil {
				return fmt.Errorf("extract %q: %w", header.Name, err)
			}
		case tar.TypeSymlink, tar.TypeLink:
			logger.Info("Skipping link %s -> %s", header.Name, header.Linkname)
		}
	}
}

// openTar opens archivePath and passes it through decompress before extracting.
func openTar(archivePath string, root *os.Root, bar *progressbar.ProgressBar, decompress func(io.Reader) (io.Reader, error)) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := decompress(io.TeeReader(f, bar))
	if err != nil {
		return err
	}
	return unpackTar(r, root)
}

func unpackTarGz(archivePath string, root *os.Root, bar *progressbar.ProgressBar) error {
	return openTar(archivePath, root, bar, func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) })
}

func unpackTarBz2(archivePath string, root *os.Root, bar *progressbar.ProgressBar) error {
	return openTar(archivePath, root, bar, func(r io.Reader) (io.Reader, error) { return bzip2.NewReader(r), nil })
}

func unpackTarXz(archivePath string, root *os.Root, bar *progressbar.ProgressBar) error {
	return openTar(archivePath, root, bar, func(r io.Reader) (io.Reader, error) { return xz.NewReader(r) })
}

// unpackBz2 decompresses a single-file .bz2 into root, named after the archive without its extension.
func unpackBz2(archivePath string, root *os.Root, bar *progressbar.ProgressBar) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	name := filepath.Base(archivePath)
	name = name[:len(name)-len(".bz2")]
	return writeFile(root, name, 0o644, bzip2.NewReader(io.TeeReader(f, bar)))
}

func unpackZip(archivePath string, root *os.Root, bar *progressbar.ProgressBar) (err error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.Close()) }()

	for _, f := range r.File {
		if err := extractZipEntry(f, root); err != nil {
			return fmt.Errorf("extract %q: %w", f.Name, err)
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, root *os.Root) error {
	mode := f.Mode()
	switch {
	case mode.IsDir():
		return root.MkdirAll(f.Name, 0o755)
	case mode&os.ModeSymlink != 0:
		logger.Info("Skipping link %s", f.Name)
		return nil
	case !mode.IsRegular():
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	return errors.Join(writeFile(root, f.Name, fileMode(mode), rc), rc.Close())
}
