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

	"github.com/google/go-github/v56/github"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/exp/slices"
)

var (
	// HomePath, default: ~/.grip
	HomePath string = ""

	// InstallPath is the path where the executables will be installed.
	// Must be in PATH
	InstallPath string = ""
	Lockfile    string = ""

	currentOS   string = runtime.GOOS
	currentArch string = runtime.GOARCH

	// OSAliases common aliases used in release packages
	OSAliases map[string][]string = map[string][]string{
		"darwin": {"macos"},
		"linux":  {"musl"},
	}

	// ArchAliases common aliases used in release packages
	ArchAliases map[string][]string = map[string][]string{
		"x86_64": {"amd64"},
		"amd64":  {"x86_64"},
		"arm64":  {"aarch64", "universal"},
	}

	// unpacker unpack functions for common package types
	unpacker map[string]unpackFn = map[string]unpackFn{
		".tar.gz":  unpackTarGz,
		".tar.bz2": unpackTarBz2,
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

	HomePath = filepath.Join(home, ".grip")

	// TODO: test on mac
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" {
		HomePath = strings.Replace(HomePath, "root", sudoUser, -1)
	}

	// TODO: read install path from config if config file exists
	InstallPath = filepath.Join(HomePath, "bin")
	err = os.MkdirAll(InstallPath, 0755)
	if err != nil {
		fmt.Println(err)
	}

	Lockfile = filepath.Join(HomePath, "grip.lock")
	_, err = os.Stat(Lockfile)
	if err != nil {
		_, err = os.Create(Lockfile)
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

func NewProgressBar(size int, description string) *progressbar.ProgressBar {
	return progressbar.NewOptions(size,
		progressbar.OptionFullWidth(),
		progressbar.OptionSetWriter(os.Stdout),
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
