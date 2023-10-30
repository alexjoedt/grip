package main

import (
	"fmt"
	"os"

	"github.com/alexjoedt/grip/cmd/install"
	"github.com/alexjoedt/grip/cmd/list"
	"github.com/urfave/cli/v2"
)

func main() {

	app := &cli.App{
		Name:  "grip",
		Usage: "grip [flags] <command>",
	}

	install.Command(app)
	list.Command(app)

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
		os.Exit(1)
	}

}
