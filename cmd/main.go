package main

import (
	"context"
	"fmt"
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
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "verbose",
				Usage: "enable verbose output",
			},
		},
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			if c.Bool("verbose") {
				logger.SetVerbose(true)
			}
			return ctx, nil
		},
	}

	versionCommand(app)
	install.Command(app, setup)
	update.Command(app, setup, version)
	list.Command(app, setup)
	remove.Command(app, setup)
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

	// HTTP client optimized for downloading large binary files
	httpClient := &http.Client{
		Timeout: 2 * time.Minute, // Max timeout for large downloads
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true, // Don't decompress, we handle archives
		},
	}

	return grip.NewInstaller(cfg, storage, grip.NewGitHubSource(), httpClient), storage, nil
}

func versionCommand(app *cli.Command) {
	cmd := &cli.Command{
		Name:        "version",
		Usage:       "prints the version of grip",
		Description: "prints the version of grip",
		Action: func(context.Context, *cli.Command) error {
			logger.Println("grip - Installing effortlessly single-executable releases from GitHub projects")
			logger.Println("%s", version)
			logger.Println("%s", build[:8])
			logger.Println("%s", date)
			return nil
		},
	}
	app.Commands = append(app.Commands, cmd)
}
