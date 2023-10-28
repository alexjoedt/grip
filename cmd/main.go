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

	_, err := install.New(app)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
		os.Exit(1)
	}

}
