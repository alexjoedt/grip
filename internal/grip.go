package grip

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alexjoedt/grip/internal/semver"
	"github.com/google/go-github/v56/github"
	"github.com/k0kubun/go-ansi"
	"github.com/minio/selfupdate"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/exp/slices"
)

const (
	repository string = "github.com/alexjoedt/grip"
)

var (
	// homePath, default: ~/.grip
	homePath string = ""

	// InstallPath is the path where the executables will be installed.
	// Must be in PATH
	InstallPath string = ""

	// lockFilepath holds the path to the lock file, where all installed executables
	// are indexed. The path will be determined in the init function.
	lockFilepath string = ""

	currentOS   string = runtime.GOOS
	currentArch string = runtime.GOARCH

	// osAliases common aliases used in release packages
	osAliases map[string][]string = map[string][]string{
		"darwin": {"macos"},
		"linux":  {"musl"},
	}

	// archAliases common aliases used in release packages
	archAliases map[string][]string = map[string][]string{
		"amd64": {"x86_64"},
		"arm64": {"aarch64", "universal"},
	}

	// unpacker unpack functions for common package types
	unpacker map[string]unpackFn = map[string]unpackFn{
		".tar.gz":  unpackTarGz,
		".tar.bz2": unpackTarBz2,
		".tbz":     unpackTarBz2,
		".zip":     unpackZip,
		".tar.xz":  unpackTarXz,
		".bz2":     unpackBz2,
	}

	// ghClient github api client
	ghClient *github.Client

	// httpClient
	httpClient *http.Client
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("no user home dir, please provide a install path")
	}

	homePath = filepath.Join(home, ".grip")

	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" && currentOS == "linux" {
		homePath = filepath.Join("/home", sudoUser, ".grip")
	}

	// TODO: read install path from config if config file exists
	InstallPath = filepath.Join(homePath, "bin")
	err = os.MkdirAll(InstallPath, 0755)
	if err != nil {
		fmt.Println(err)
	}

	lockFilepath = filepath.Join(homePath, "grip.lock")
	_, err = os.Stat(lockFilepath)
	if err != nil {
		_, err = os.Create(lockFilepath)
		if err != nil {
			fmt.Printf("failed to create grip.lock\n")
		}
	}

	ghClient = github.NewClient(nil)
	httpClient = &http.Client{
		Timeout: time.Second * 30,
	}
}

func CheckPathEnv() {
	pathEnv := os.Getenv("PATH")
	parts := filepath.SplitList(pathEnv)
	if !slices.Contains(parts, InstallPath) {
		fmt.Printf("WARN: the grip path '%s' isn't in PATH\n", InstallPath)
	}
}

func ParseRepoPath(repo string) (string, string, error) {

	if !strings.HasPrefix(repo, "github.com") {
		return "", "", fmt.Errorf("invalid repo: %s", repo)
	}

	parts := strings.Split(repo, "/")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("invalid repo path: %s", repo)
	}

	return parts[1], parts[2], nil
}

func SelfUpdate(version string) error {
	currentVersion, err := semver.Parse(version)
	if err != nil {
		return err
	}

	owner, name, err := ParseRepoPath(repository)
	if err != nil {
		return err
	}

	asset, err := GetLatest(owner, name)
	if err != nil {
		return err
	}

	assetVersion, err := semver.Parse(asset.Tag)
	if err != nil {
		return err
	}

	res := semver.Compare(currentVersion, assetVersion)
	if res >= 0 {
		fmt.Printf("newest version already installed\n")
		return nil
	}

	defer func() {
		os.RemoveAll(asset.tempDir)
	}()

	err = asset.init()
	if err != nil {
		return err
	}

	err = asset.download()
	if err != nil {
		return err
	}
	err = asset.unpack()
	if err != nil {
		return err
	}

	binPath, err := findExecutable(filepath.Join(asset.tempDir, "unpack"))
	if err != nil {
		return err
	}

	reader, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	err = selfupdate.Apply(reader, selfupdate.Options{})
	if err != nil {
		return err
	}

	// it could be that grip is in the lockfile
	entry, err := GetEntryByName(name)
	if err != nil {
		// grip isn't in the lockfile, no changes
		// TODO: only print this with verbose flag
		fmt.Printf("grip hast no entry in the lockfile")
	} else {
		entry.Tag = asset.Tag
		UpdateEntry(*entry)
	}
	return nil
}

func NewProgressBar(size int, description string) *progressbar.ProgressBar {
	return progressbar.NewOptions(size,
		progressbar.OptionFullWidth(),
		progressbar.OptionSetWriter(ansi.NewAnsiStdout()),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))
}
