package install

import (
	"context"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:      "install",
		Usage:     "install an executable from a GitHub release",
		ArgsUsage: "<owner/repo[@tag]>",
		Flags: []cli.Flag{
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
			&cli.StringFlag{
				Name:  "asset",
				Usage: "release asset name or glob to install, remembered for updates",
			},
			&cli.StringFlag{
				Name:  "bin",
				Usage: "name of the executable inside the archive, remembered for updates",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			installer, _, err := setup()
			if err != nil {
				return err
			}
			opts := grip.InstallOptions{
				Repo:  c.Args().First(),
				Force: c.Bool("force"),
				Alias: c.String("alias"),
				Asset: c.String("asset"),
				Bin:   c.String("bin"),
			}

			return installer.Install(ctx, opts)
		},
	}
	app.Commands = append(app.Commands, cmd)
}
