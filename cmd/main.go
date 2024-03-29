package main

import (
	"fmt"
	"os"

	"github.com/alexjoedt/grip/cmd/install"
	"github.com/alexjoedt/grip/cmd/list"
	"github.com/alexjoedt/grip/cmd/remove"
	"github.com/alexjoedt/grip/cmd/update"
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
	}

	versionCommand(app)
	install.Command(app)
	update.Command(app)
	list.Command(app)
	remove.Command(app)

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
		os.Exit(1)
	}

}

func versionCommand(app *cli.App) {
	cmd := &cli.Command{
		Name:        "version",
		Usage:       "prints the version of grip",
		Description: "prints the version of grip",
		Action: func(ctx *cli.Context) error {
			fmt.Printf("grip - Installing effortlessly single-executable releases from GitHub projects\n")
			fmt.Printf("%s\n", version)
			fmt.Printf("%s\n", build[:8])
			fmt.Printf("%s\n", date)
			return nil
		},
	}
	app.Commands = append(app.Commands, cmd)
}
