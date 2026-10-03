package pin

import (
	"context"
	"fmt"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := func(name, usage string, pinned bool) *cli.Command {
		return &cli.Command{
			Name:      name,
			Usage:     usage,
			ArgsUsage: "<name...>",
			Action: func(ctx context.Context, c *cli.Command) error {
				if c.NArg() == 0 {
					return fmt.Errorf("usage: grip %s <name...>", name)
				}
				_, storage, err := setup()
				if err != nil {
					return err
				}
				return storage.SetPinned(ctx, pinned, c.Args().Slice()...)
			},
		}
	}
	app.Commands = append(app.Commands,
		cmd("pin", "keeps packages at their installed tag during updates", true),
		cmd("unpin", "lets updates move packages to the latest release again", false),
	)
}
