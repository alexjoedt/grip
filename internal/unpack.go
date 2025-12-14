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

	"github.com/h2non/filetype"
	"github.com/schollz/progressbar/v3"
	"github.com/ulikunitz/xz"
)

type unpackFn func(io.Reader, string, *progressbar.ProgressBar) error

// Unpacker handles extracting various archive formats
type Unpacker struct {
	unpackers map[string]unpackFn
}

// NewUnpacker creates a new Unpacker with support for common archive formats
func NewUnpacker() *Unpacker {
	return &Unpacker{
		unpackers: map[string]unpackFn{
			".tar.gz":  unpackTarGz,
			".tar.bz2": unpackTarBz2,
			".tbz":     unpackTarBz2,
			".zip":     unpackZip,
			".tar.xz":  unpackTarXz,
			".bz2":     unpackBz2,
		},
	}
}

// Unpack extracts an archive file to the destination directory
// Returns the path to the executable binary found in the archive
func (u *Unpacker) Unpack(archivePath, destDir string) (string, error) {
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return "", fmt.Errorf("stat archive: %w", err)
	}

	ext, unpackFn, err := u.getUnpackFn(archivePath)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}

	archive, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer archive.Close()

	unpackDest := destDir
	if ext == ".bz2" {
		// .bz2 files need special handling
		unpackDest = filepath.Join(destDir, filepath.Base(archivePath))
		unpackDest = strings.TrimSuffix(unpackDest, ext)
	}

	bar := NewProgressBar(int(archiveInfo.Size()), "[cyan][2/3][reset] Unpacking")
	if err := unpackFn(archive, unpackDest, bar); err != nil {
		return "", fmt.Errorf("unpack archive: %w", err)
	}
	fmt.Println() // new line after progress bar

	execPath, err := u.findExecutable(destDir)
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}

	return execPath, nil
}

// IsSupportedFormat checks if the filename has a supported archive extension
func (u *Unpacker) IsSupportedFormat(filename string) bool {
	for ext := range u.unpackers {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return true
		}
	}
	return false
}

// getUnpackFn returns the appropriate unpacker function for the filename
func (u *Unpacker) getUnpackFn(filename string) (string, unpackFn, error) {
	filename = strings.ToLower(filename)
	for ext, fn := range u.unpackers {
		if strings.HasSuffix(filename, ext) {
			return ext, fn, nil
		}
	}
	return "", nil, fmt.Errorf("unsupported archive format: %s", filename)
}

// findExecutable searches for an executable binary in the directory tree
func (u *Unpacker) findExecutable(dir string) (string, error) {
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
			mimeType, err := u.detectFileType(path)
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

// detectFileType detects the MIME type of a file
func (u *Unpacker) detectFileType(path string) (string, error) {
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

func unpackTarGz(packageFile io.Reader, destination string, bar *progressbar.ProgressBar) error {
	gzr, err := gzip.NewReader(packageFile)
	if err != nil {
		return err
	}
	defer gzr.Close()

	multi := io.MultiReader(io.TeeReader(gzr, bar))
	tr := tar.NewReader(multi)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destination, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			dir := filepath.Dir(target)
			_, err := os.Stat(dir)
			if err != nil {
				os.MkdirAll(dir, 0755)
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			f.Close()
		}
	}
	return nil
}

func unpackTarBz2(packageFile io.Reader, destination string, bar *progressbar.ProgressBar) error {
	bzr := bzip2.NewReader(packageFile)

	multi := io.MultiReader(io.TeeReader(bzr, bar))

	tr := tar.NewReader(multi)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destination, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			f.Close()
		}
	}
	return nil
}

func unpackBz2(packageReader io.Reader, destination string, bar *progressbar.ProgressBar) error {
	bz2Reader := bzip2.NewReader(packageReader)

	multi := io.MultiReader(io.TeeReader(bz2Reader, bar))

	outFile, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, multi)
	return err
}

func unpackZip(packageFile io.Reader, destination string, bar *progressbar.ProgressBar) error {

	tmpFile, err := os.CreateTemp("", "temp-zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err = io.Copy(tmpFile, packageFile); err != nil {
		return err
	}

	r, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {

		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		fpath := filepath.Join(destination, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
		} else {
			var fdir string
			if lastIndex := strings.LastIndex(fpath, string(os.PathSeparator)); lastIndex > -1 {
				fdir = fpath[:lastIndex]
				os.MkdirAll(fdir, os.ModePerm)
			}

			f, err := os.OpenFile(
				fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return err
			}
			_, err = io.Copy(f, rc)
			if err != nil {
				return err
			}
			f.Close()
		}
	}
	return nil
}

func unpackTarXz(packageFile io.Reader, destination string, bar *progressbar.ProgressBar) error {

	xzr, err := xz.NewReader(packageFile)
	if err != nil {
		return err
	}
	multi := io.MultiReader(io.TeeReader(xzr, bar))

	tr := tar.NewReader(multi)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destination, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			f.Close()
		}
	}
	return nil
}
