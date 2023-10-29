package main

import (
	"fmt"
	"os"

	"github.com/alexjoedt/grip/cmd/install"
	"github.com/urfave/cli/v2"
)

func main() {

	app := &cli.App{
		Name:  "grip",
		Usage: "grip [flags] <command>",
	}

	installCommand := install.Command()
	addCommand(app, installCommand)

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
		os.Exit(1)
	}

}

func addCommand(a *cli.App, cmd *cli.Command) {
	a.Commands = append(a.Commands, cmd)
}
