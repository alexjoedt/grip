package grip

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"debug/elf"
	"debug/macho"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexjoedt/grip/internal/logger"
	"github.com/schollz/progressbar/v3"
	"github.com/ulikunitz/xz"
)

type unpackFn func(archivePath string, root *extractRoot, bar *progressbar.ProgressBar) error

// Extraction limits per archive, counting bytes actually written.
var (
	maxExtractBytes   int64 = 2 << 30
	maxExtractEntries int64 = 100_000
)

// extractRoot confines extraction to a directory and enforces the limits
// across all entries of one archive.
type extractRoot struct {
	*os.Root
	written, entries int64
}

// entry counts one archive entry against maxExtractEntries.
func (r *extractRoot) entry() error {
	r.entries++
	if r.entries > maxExtractEntries {
		return fmt.Errorf("%w: more than %d entries", ErrArchiveTooLarge, maxExtractEntries)
	}
	return nil
}

var unpackers = map[string]unpackFn{
	".tar.gz":  unpackTarGz,
	".tgz":     unpackTarGz,
	".tar.bz2": unpackTarBz2,
	".tbz":     unpackTarBz2,
	".zip":     unpackZip,
	".tar.xz":  unpackTarXz,
	".gz":      unpackSingle(".gz", gunzip),
	".xz":      unpackSingle(".xz", unxz),
	".bz2":     unpackSingle(".bz2", bunzip2),
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
	".gz",
	".xz",
}

// Unpack extracts an archive file to the destination directory and returns
// the path of the executable selected by q. A single-file .gz, .xz or .bz2
// decompresses to one file; an asset without archive extension is used as is.
// Either must be an executable for q's target.
func Unpack(archivePath, destDir string, q binaryQuery) (string, error) {
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return "", fmt.Errorf("stat archive: %w", err)
	}

	ext, fn, err := getUnpackFn(archivePath)
	if err != nil {
		return bareBinary(archivePath, q)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}

	dir, err := os.OpenRoot(destDir)
	if err != nil {
		return "", fmt.Errorf("open destination directory: %w", err)
	}
	root := &extractRoot{Root: dir}
	bar := NewProgressBar(int(archiveInfo.Size()), "[cyan][2/3][reset] Unpacking")
	if err := errors.Join(fn(archivePath, root, bar), root.Close()); err != nil {
		return "", fmt.Errorf("unpack archive: %w", err)
	}
	endProgressBar()

	switch ext {
	case ".gz", ".xz", ".bz2":
		return bareBinary(filepath.Join(destDir, singleName(archivePath, ext)), q)
	}
	execPath, err := findBinary(destDir, q)
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}
	return execPath, nil
}

// bareBinary returns path when it is an executable for q's target.
func bareBinary(path string, q binaryQuery) (string, error) {
	if !executableFor(path, q.OS, q.Arch) {
		if !q.AnyArch || !executableFor(path, q.OS, "") {
			return "", fmt.Errorf("%s is neither a supported archive nor an executable for %s/%s", filepath.Base(path), q.OS, q.Arch)
		}
		logger.Warn("%s is not built for %s/%s", filepath.Base(path), q.OS, q.Arch)
	}
	if q.Override != "" {
		logger.Warn("bin override %q ignored, the asset is a single executable", q.Override)
	}
	return path, nil
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

// binaryQuery says which executable to take from an unpacked archive.
type binaryQuery struct {
	OS, Arch string
	Override string   // bin override; must name a candidate
	Names    []string // install name first, then repository name
	AnyArch  bool     // without a q.Arch executable, take others with a warning
}

// findBinary returns the executable in dir selected by q. Candidates are
// regular ELF (Mach-O on darwin) executables for q.OS and q.Arch.
func findBinary(dir string, q binaryQuery) (string, error) {
	var cands, foreign []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if executableFor(path, q.OS, q.Arch) {
			cands = append(cands, rel)
		} else if q.AnyArch && executableFor(path, q.OS, "") {
			foreign = append(foreign, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(cands) == 0 && len(foreign) > 0 {
		logger.Warn("no executable for %s/%s in the archive, using other architectures: %s", q.OS, q.Arch, strings.Join(foreign, ", "))
		cands = foreign
	}
	logger.Info("binary candidates for %s/%s: %s", q.OS, q.Arch, strings.Join(cands, ", "))

	bin, err := selectBinary(cands, q.Override, q.Names...)
	if err != nil {
		return "", err
	}
	if base := filepath.Base(bin); len(q.Names) > 0 && !strings.HasPrefix(base, q.Names[0]) {
		logger.Println("binary in archive is %q; use --alias %s", base, base)
	}
	return filepath.Join(dir, bin), nil
}

// selectBinary picks one of cands, paths relative to the archive root. The
// candidate whose base name equals override wins; without override a single
// candidate wins, else the one named like the first of names that matches.
func selectBinary(cands []string, override string, names ...string) (string, error) {
	if len(cands) == 0 {
		return "", errors.New("no executable found in archive")
	}
	bases := make([]string, len(cands))
	for i, c := range cands {
		bases[i] = filepath.Base(c)
	}
	if override != "" {
		if c := byBaseName(cands, override); len(c) == 1 {
			logger.Info("binary %s: matches bin override", c[0])
			return c[0], nil
		}
		return "", &choiceError{fmt.Errorf("bin override %q names no single executable in archive", override), "--bin", bases}
	}
	if len(cands) == 1 {
		logger.Info("binary %s: only candidate", cands[0])
		return cands[0], nil
	}
	for _, n := range names {
		if c := byBaseName(cands, n); len(c) == 1 {
			logger.Info("binary %s: base name equals %s", c[0], n)
			return c[0], nil
		}
	}
	return "", &choiceError{ErrAmbiguousBinary, "--bin", bases}
}

func byBaseName(cands []string, name string) []string {
	var out []string
	for _, c := range cands {
		if filepath.Base(c) == name {
			out = append(out, c)
		}
	}
	return out
}

var elfMachines = map[string]elf.Machine{
	"amd64": elf.EM_X86_64,
	"arm64": elf.EM_AARCH64,
	"386":   elf.EM_386,
	"arm":   elf.EM_ARM,
}

var machoCPUs = map[string]macho.Cpu{
	"amd64": macho.CpuAmd64,
	"arm64": macho.CpuArm64,
}

// executableFor reports whether path is an executable for goos and goarch:
// Mach-O (thin or universal) on darwin, ELF elsewhere. Unparsable files are
// not. An empty goarch accepts any architecture.
func executableFor(path, goos, goarch string) bool {
	if goos == "darwin" {
		if f, err := macho.Open(path); err == nil {
			defer func() { _ = f.Close() }()
			return machoFor(f.FileHeader, goarch)
		}
		ff, err := macho.OpenFat(path)
		if err != nil {
			return false
		}
		defer func() { _ = ff.Close() }()
		for _, a := range ff.Arches {
			if machoFor(a.FileHeader, goarch) {
				return true
			}
		}
		return false
	}

	f, err := elf.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	m, ok := elfMachines[goarch]
	return (goarch == "" || ok && f.Machine == m) && (f.Type == elf.ET_EXEC || f.Type == elf.ET_DYN)
}

func machoFor(h macho.FileHeader, goarch string) bool {
	cpu, ok := machoCPUs[goarch]
	return (goarch == "" || ok && h.Cpu == cpu) && h.Type == macho.TypeExec
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
func writeFile(root *extractRoot, name string, mode os.FileMode, r io.Reader) error {
	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxExtractBytes-root.written+1))
	root.written += n
	if err == nil && root.written > maxExtractBytes {
		err = fmt.Errorf("%w: more than %d bytes", ErrArchiveTooLarge, maxExtractBytes)
	}
	if err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// unpackTar iterates over a tar stream and extracts entries into root.
// r should already be a decompressed reader (the caller handles decompression).
func unpackTar(r io.Reader, root *extractRoot) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := root.entry(); err != nil {
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
func openTar(archivePath string, root *extractRoot, bar *progressbar.ProgressBar, decompress func(io.Reader) (io.Reader, error)) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r, err := decompress(io.TeeReader(f, bar))
	if err != nil {
		return err
	}
	return unpackTar(r, root)
}

func gunzip(r io.Reader) (io.Reader, error)  { return gzip.NewReader(r) }
func unxz(r io.Reader) (io.Reader, error)    { return xz.NewReader(r) }
func bunzip2(r io.Reader) (io.Reader, error) { return bzip2.NewReader(r), nil }

func unpackTarGz(archivePath string, root *extractRoot, bar *progressbar.ProgressBar) error {
	return openTar(archivePath, root, bar, gunzip)
}

func unpackTarBz2(archivePath string, root *extractRoot, bar *progressbar.ProgressBar) error {
	return openTar(archivePath, root, bar, bunzip2)
}

func unpackTarXz(archivePath string, root *extractRoot, bar *progressbar.ProgressBar) error {
	return openTar(archivePath, root, bar, unxz)
}

// singleName is the file a single-file compressed asset decompresses to.
func singleName(archivePath, ext string) string {
	name := filepath.Base(archivePath)
	return name[:len(name)-len(ext)]
}

// unpackSingle decompresses a single-file asset into root under singleName.
func unpackSingle(ext string, decompress func(io.Reader) (io.Reader, error)) unpackFn {
	return func(archivePath string, root *extractRoot, bar *progressbar.ProgressBar) error {
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		r, err := decompress(io.TeeReader(f, bar))
		if err != nil {
			return err
		}
		return writeFile(root, singleName(archivePath, ext), 0o644, r)
	}
}

func unpackZip(archivePath string, root *extractRoot, bar *progressbar.ProgressBar) (err error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.Close()) }()

	for _, f := range r.File {
		if err := root.entry(); err != nil {
			return err
		}
		if err := extractZipEntry(f, root); err != nil {
			return fmt.Errorf("extract %q: %w", f.Name, err)
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, root *extractRoot) error {
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
