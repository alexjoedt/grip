package outdated

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:  "outdated",
		Usage: "lists installed executables with a newer release",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "print JSON instead of a table",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			installer, storage, err := setup()
			if err != nil {
				return err
			}
			pkgs, lookupErr := installer.Outdated(ctx)
			if c.Bool("json") {
				type outdated struct {
					grip.PackageJSON
					Latest string `json:"latest"`
				}
				out := []outdated{}
				for _, p := range pkgs {
					inst, err := storage.Get(p.Name)
					if err != nil {
						return errors.Join(lookupErr, err)
					}
					out = append(out, outdated{storage.PackageJSON(inst), p.Latest})
				}
				return errors.Join(lookupErr, json.NewEncoder(c.Root().Writer).Encode(out))
			}
			if len(pkgs) > 0 {
				tw := tabwriter.NewWriter(c.Root().Writer, 0, 0, 2, ' ', 0)
				var werr error
				row := func(format string, a ...any) {
					if _, err := fmt.Fprintf(tw, format, a...); werr == nil {
						werr = err
					}
				}
				row("NAME\tTAG\tLATEST\tPINNED\n")
				for _, p := range pkgs {
					pinned := "no"
					if p.Pinned {
						pinned = "yes"
					}
					row("%s\t%s\t%s\t%s\n", p.Name, p.Tag, p.Latest, pinned)
				}
				if err := errors.Join(werr, tw.Flush()); err != nil {
					return errors.Join(lookupErr, err)
				}
			}
			return lookupErr
		},
	}
	app.Commands = append(app.Commands, cmd)
}
