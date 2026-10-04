package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexjoedt/grip/cmd/info"
	"github.com/alexjoedt/grip/cmd/install"
	"github.com/alexjoedt/grip/cmd/list"
	"github.com/alexjoedt/grip/cmd/outdated"
	"github.com/alexjoedt/grip/cmd/pin"
	"github.com/alexjoedt/grip/cmd/remove"
	"github.com/alexjoedt/grip/cmd/rollback"
	"github.com/alexjoedt/grip/cmd/update"
	"github.com/alexjoedt/grip/cmd/verify"
	grip "github.com/alexjoedt/grip/internal"
	"github.com/alexjoedt/grip/internal/logger"
	"github.com/urfave/cli/v3"
)

var (
	version string = "undefined"
	build   string = "undefined"
	date    string = "undefined"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := newApp().Run(ctx, os.Args); err != nil {
		logger.Error("%s", err.Error())
		os.Exit(1)
	}
}

func newApp() *cli.Command {
	app := &cli.Command{
		Name:    "grip",
		Usage:   "grip [flags] <command>",
		Version: version,
		// The generated completion command is hidden by default.
		EnableShellCompletion:           true,
		ConfigureShellCompletionCommand: func(c *cli.Command) { c.Hidden = false },
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "verbose",
				Usage: "enable verbose output",
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "suppress progress and status output, keep warnings and errors",
			},
		},
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			if c.Bool("quiet") && c.Bool("verbose") {
				return ctx, fmt.Errorf("--quiet and --verbose cannot be used together")
			}
			if c.Bool("verbose") {
				logger.SetVerbose(true)
			}
			logger.SetQuiet(c.Bool("quiet"))
			return ctx, nil
		},
	}

	versionCommand(app)
	install.Command(app, setup)
	update.Command(app, setup, version)
	list.Command(app, setup)
	outdated.Command(app, setup)
	remove.Command(app, setup)
	pin.Command(app, setup)
	rollback.Command(app, setup)
	verify.Command(app, setup)
	info.Command(app, setup)
	return app
}

// setup creates the grip home and its dependencies. Commands call it from
// their actions so that help and version touch nothing on disk.
func setup() (*grip.Installer, *grip.Storage, error) {
	cfg, err := grip.DefaultConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, fmt.Errorf("create directories: %w", err)
	}
	storage, err := grip.NewStorage(cfg.StorePath, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize storage: %w", err)
	}

	// No overall timeout: Download aborts on a stall instead.
	httpClient := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          10,
			MaxIdleConnsPerHost:   5,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true, // Don't decompress, we handle archives
		},
	}

	return grip.NewInstaller(cfg, storage, grip.NewGitHubSource(os.Getenv("GITHUB_TOKEN")), httpClient), storage, nil
}

func versionCommand(app *cli.Command) {
	cmd := &cli.Command{
		Name:        "version",
		Usage:       "prints the version of grip",
		Description: "prints the version of grip",
		Action: func(context.Context, *cli.Command) error {
			fmt.Printf("grip - Installing effortlessly single-executable releases from GitHub projects\n%s\n%s\n%s\n", version, shortBuild(build), date)
			return nil
		},
	}
	app.Commands = append(app.Commands, cmd)
}

func shortBuild(b string) string {
	if len(b) > 8 {
		return b[:8]
	}
	return b
}
