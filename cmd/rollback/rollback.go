package rollback

import (
	"context"
	"fmt"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:      "rollback",
		Usage:     "switches an executable back to its previous version and pins it",
		ArgsUsage: "<name>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.NArg() != 1 {
				return fmt.Errorf("usage: grip rollback <name>")
			}
			installer, _, err := setup()
			if err != nil {
				return err
			}
			return installer.Rollback(ctx, c.Args().First())
		},
	}
	app.Commands = append(app.Commands, cmd)
}
