package grip

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/schollz/progressbar/v3"
	"github.com/ulikunitz/xz"
)

type unpackFn func(io.Reader, string, *progressbar.ProgressBar) error

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
