package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexjoedt/grip/cmd/install"
	"github.com/alexjoedt/grip/cmd/list"
	"github.com/alexjoedt/grip/cmd/remove"
	"github.com/alexjoedt/grip/cmd/update"
	grip "github.com/alexjoedt/grip/internal"
	"github.com/alexjoedt/grip/internal/logger"
	"github.com/urfave/cli/v2"
)

var (
	version string = "undefined"
	build   string = "undefined"
	date    string = "undefined"
)

func main() {
	// Create config
	cfg, err := grip.DefaultConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	// Ensure directories exist
	if err := cfg.EnsureDirs(); err != nil {
		logger.Fatal("Failed to create directories: %v", err)
	}

	// Create storage
	storage, err := grip.NewStorage(cfg.StorePath, cfg)
	if err != nil {
		logger.Fatal("Failed to initialize storage: %v", err)
	}

	// Create GitHub client
	ghClient := grip.NewGitHubClient()

	// Create HTTP client optimized for downloading large binary files
	httpClient := &http.Client{
		Timeout: 2 * time.Minute, // Max timeout for large downloads
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true, // Don't decompress, we handle archives
		},
	}

	// Create installer
	installer := grip.NewInstaller(cfg, storage, ghClient, httpClient)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app := &cli.App{
		Name:    "grip",
		Usage:   "grip [flags] <command>",
		Version: version,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "verbose",
				Usage: "enable verbose output",
			},
		},
		Before: func(ctx *cli.Context) error {
			if ctx.Bool("verbose") {
				logger.SetVerbose(true)
			}
			return nil
		},
	}

	versionCommand(app)
	install.Command(ctx, app, installer, cfg)
	update.Command(ctx, app, installer, storage, cfg)
	list.Command(app, storage)
	remove.Command(app, storage, cfg)

	if err := app.Run(os.Args); err != nil {
		logger.Error("%s", err.Error())
		os.Exit(1)
	}

}

func versionCommand(app *cli.App) {
	cmd := &cli.Command{
		Name:        "version",
		Usage:       "prints the version of grip",
		Description: "prints the version of grip",
		Action: func(ctx *cli.Context) error {
			logger.Println("grip - Installing effortlessly single-executable releases from GitHub projects")
			logger.Println("%s", version)
			logger.Println("%s", build[:8])
			logger.Println("%s", date)
			return nil
		},
	}
	app.Commands = append(app.Commands, cmd)
}
