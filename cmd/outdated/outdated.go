package outdated

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:  "outdated",
		Usage: "lists installed executables with a newer release",
		Action: func(ctx context.Context, c *cli.Command) error {
			installer, _, err := setup()
			if err != nil {
				return err
			}
			pkgs, lookupErr := installer.Outdated(ctx)
			if len(pkgs) > 0 {
				tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintf(tw, "NAME\tTAG\tLATEST\tPINNED\n")
				for _, p := range pkgs {
					pinned := "no"
					if p.Pinned {
						pinned = "yes"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, p.Tag, p.Latest, pinned)
				}
				if err := tw.Flush(); err != nil {
					return errors.Join(lookupErr, err)
				}
			}
			return lookupErr
		},
	}
	app.Commands = append(app.Commands, cmd)
}
