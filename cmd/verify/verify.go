package verify

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:      "verify",
		Usage:     "checks installed executables against their recorded hashes",
		ArgsUsage: "[name...]",
		Action: func(_ context.Context, c *cli.Command) error {
			_, storage, err := setup()
			if err != nil {
				return err
			}
			results, err := storage.Verify(c.Args().Slice()...)
			if err != nil {
				return err
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "NAME\tTAG\tRESULT\tDIGEST SOURCE\n")
			failed := 0
			for _, r := range results {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Tag, r.Result, cmp.Or(r.DigestSource, "unknown"))
				if r.Failed() {
					failed++
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d packages failed verification", failed, len(results))
			}
			return nil
		},
	}
	app.Commands = append(app.Commands, cmd)
}
