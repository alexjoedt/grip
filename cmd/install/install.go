package install

import (
	"context"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v2"
)

func Command(ctx context.Context, app *cli.App, installer *grip.Installer, cfg *grip.Config) {
	cmd := &cli.Command{
		Name:  "install",
		Usage: "install an executable from a GitHub release",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "tag",
				Aliases: []string{"t"},
				Usage:   "release tag",
			},
			&cli.StringFlag{
				Name:    "destination",
				Aliases: []string{"d"},
				Usage:   "specifies the installation path",
				Value:   cfg.BinDir,
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "forces the installation",
			},
			&cli.StringFlag{
				Name:    "alias",
				Aliases: []string{"a"},
				Usage:   "alias for the executable",
			},
		},
		Action: func(c *cli.Context) error {
			opts := grip.InstallOptions{
				Repo:        c.Args().First(),
				Tag:         c.String("tag"),
				Destination: c.String("destination"),
				Force:       c.Bool("force"),
				Alias:       c.String("alias"),
			}

			return installer.Install(ctx, opts)
		},
	}
	app.Commands = append(app.Commands, cmd)
}
