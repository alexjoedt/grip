package update

import (
	"context"
	"fmt"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error), version string) {
	cmd := &cli.Command{
		Name:      "update",
		Usage:     "updates executables to their latest release",
		ArgsUsage: "[name...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "all",
				Usage: "updates every installed executable except pinned ones",
			},
			&cli.StringFlag{
				Name:  "asset",
				Usage: "release asset name or glob to install, replaces the remembered one",
			},
			&cli.StringFlag{
				Name:  "bin",
				Usage: "name of the executable inside the archive, replaces the remembered one",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			names := c.Args().Slice()
			all := c.Bool("all")
			override := c.String("asset") != "" || c.String("bin") != ""
			switch {
			case all && len(names) > 0:
				return fmt.Errorf("use either --all or package names")
			case !all && len(names) == 0:
				return fmt.Errorf("please provide the name of the package to update, or --all")
			case override && len(names) != 1:
				return fmt.Errorf("--asset and --bin need exactly one package name")
			}

			installer, _, err := setup()
			if err != nil {
				return err
			}
			if len(names) == 1 {
				return installer.Update(ctx, names[0], c.String("asset"), c.String("bin"))
			}
			return installer.UpdateMany(ctx, names...)
		},
	}

	selfCmd := &cli.Command{
		Name:  "self-update",
		Usage: "updates grip",
		Action: func(ctx context.Context, _ *cli.Command) error {
			installer, _, err := setup()
			if err != nil {
				return err
			}
			return grip.SelfUpdate(ctx, version, installer)
		},
	}

	app.Commands = append(app.Commands, cmd, selfCmd)
}
