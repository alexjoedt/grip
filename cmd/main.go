package main

import (
	"os"

	"github.com/alexjoedt/grip/cmd/install"
	"github.com/alexjoedt/grip/cmd/list"
	"github.com/alexjoedt/grip/cmd/remove"
	"github.com/alexjoedt/grip/cmd/update"
	"github.com/alexjoedt/grip/internal/logger"
	"github.com/urfave/cli/v2"
)

var (
	version string = "undefined"
	build   string = "undefined"
	date    string = "undefined"
)

func main() {
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
	install.Command(app)
	update.Command(app)
	list.Command(app)
	remove.Command(app)

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
