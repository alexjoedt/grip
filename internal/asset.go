package grip

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v56/github"
	"github.com/h2non/filetype"
)

// Asset describes a release asset
type Asset struct {
	Name        string
	Alias       string
	OS          string
	Arch        string
	DownloadURL string
	Tag         string
	tempDir     string
	repoName    string
	repoOwner   string
}

// init initializes temp dirs
func (a *Asset) init() error {
	tempDir, err := os.MkdirTemp(os.TempDir(), a.Name+"*")
	if err != nil {
		return err
	}

	a.tempDir = tempDir

	// create download dir
	err = os.MkdirAll(filepath.Join(tempDir, "download"), 0775)
	if err != nil {
		return err
	}

	// create unpack dir
	err = os.MkdirAll(filepath.Join(tempDir, "unpack"), 0775)
	if err != nil {
		return err
	}

	return nil
}

// download downloads the asset from github
func (a *Asset) download() error {
	res, err := httpClient.Get(a.DownloadURL)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode > 299 {
		return fmt.Errorf("invalid response with status %s", res.Status)
	}

	f, err := os.Create(filepath.Join(a.tempDir, "download", a.Name))
	if err != nil {
		return err
	}
	defer f.Close()

	bar := NewProgressBar(int(res.ContentLength), "[cyan][1/3][reset] Downloading")

	io.Copy(io.MultiWriter(f, bar), res.Body)
	if err != nil {
		return err
	}

	fmt.Println() // new line after progressbar
	return nil
}

// unpack extracts the package if needed
func (a *Asset) unpack() error {

	packagePath := filepath.Join(a.tempDir, "download", a.Name)
	packageInfo, err := os.Stat(packagePath)
	if err != nil {
		return err
	}

	ext, fn, err := getUnpackFn(a.Name)
	if err != nil {
		return err
	}

	unpackDir := filepath.Join(a.tempDir, "unpack")
	pack, err := os.Open(packagePath)
	if err != nil {
		return err
	}
	defer pack.Close()

	if ext == ".bz2" {
		unpackDir = filepath.Join(unpackDir, a.BinaryName())
	}

	bar := NewProgressBar(int(packageInfo.Size()), "[cyan][2/3][reset] Unpacking")
	defer func() {
		fmt.Println()
	}()

	return fn(pack, unpackDir, bar)
}

// install installs the executable
func (a *Asset) Install(p string) error {
	defer a.clean()
	p = strings.TrimSuffix(p, "/")

	var err error

	err = a.init()
	if err != nil {
		return err
	}

	err = a.download()
	if err != nil {
		return err
	}

	err = a.unpack()
	if err != nil {
		return err
	}

	binPath, err := findExecutable(filepath.Join(a.tempDir, "unpack"))
	if err != nil {
		return err
	}

	err = os.Chmod(binPath, 0744)
	if err != nil {
		return err
	}

	info, err := os.Stat(binPath)
	if err != nil {
		return err
	}

	unpacked, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer unpacked.Close()

	destBin := filepath.Join(p, a.BinaryName())
	dest, err := os.Create(destBin)
	if err != nil {
		return err
	}
	defer dest.Close()

	bar := NewProgressBar(int(info.Size()), "[cyan][3/3][reset] Installing")
	multi := io.MultiWriter(dest, bar)
	io.Copy(multi, unpacked)
	if err != nil {
		return err
	}

	fmt.Println()

	err = os.Chmod(destBin, 0755)
	if err != nil {
		return err
	}

	return nil
}

// BinaryName guesses the binary name
func (a *Asset) BinaryName() string {
	if a.Alias != "" {
		return a.Alias
	}
	return a.repoName
}

// clean removes the temp folder that was used for installing
func (a *Asset) clean() error {
	return os.RemoveAll(a.tempDir)
}

func findExecutable(dir string) (string, error) {

	fileTypes := map[string]int{
		"application/x-mach-binary": 0,
		"application/x-executable":  0,
	}

	var executablePath string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ft, err := detectFileType(path)
			if err != nil {
				return err
			}

			if _, ok := fileTypes[ft]; ok {
				executablePath = path
				return nil
			}
		}

		return nil
	})

	if executablePath == "" {
		err = errors.New("no executable found")
	}

	return executablePath, err
}

func detectFileType(p string) (string, error) {
	r, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer r.Close()

	buffer := make([]byte, 261)
	n, err := r.Read(buffer)
	if err != nil && err != io.EOF {
		return "", err
	}
	kind, err := filetype.Match(buffer[:n])
	if err != nil {
		return "", err
	}
	return kind.MIME.Value, nil
}

// parseAsset gets the right asset for OS and Arch
func parseAsset(assets []*github.ReleaseAsset) (*Asset, error) {
	var (
		name  string
		url   string
		found bool
	)

	for _, a := range assets {

		name = strings.ToLower(*a.Name)

		if containsCurrentOSAndArch(name) && isSupportedExt(name) {
			url = *a.BrowserDownloadURL
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("no asset found for %s_%s", currentOS, currentArch)
	}

	return &Asset{
		Name:        name,
		OS:          currentOS,
		Arch:        currentArch,
		DownloadURL: url,
	}, nil
}

// getUnpackFn gets the right unpacker for the given asset
func getUnpackFn(name string) (string, unpackFn, error) {
	for ext, mt := range unpacker {
		if strings.HasSuffix(name, ext) {
			return ext, mt, nil
		}
	}
	return "", nil, fmt.Errorf("unsupported extension in filename: %s", name)
}

// containsCurrentOSAndArch checks the filename for OS and Arch
func containsCurrentOSAndArch(name string) bool {
	if stringContainsAny(name, append(osAliases[currentOS], currentOS)...) &&
		stringContainsAny(name, append(archAliases[currentArch], currentArch)...) {
		return true
	}
	return false
}

// stringContainsAny helper function
func stringContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func isSupportedExt(name string) bool {
	for ext := range unpacker {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}
