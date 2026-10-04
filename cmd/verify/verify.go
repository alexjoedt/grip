package verify

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:      "verify",
		Usage:     "checks installed executables against their recorded hashes",
		ArgsUsage: "[name...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "print JSON instead of a table",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			_, storage, err := setup()
			if err != nil {
				return err
			}
			results, err := storage.Verify(c.Args().Slice()...)
			if err != nil {
				return err
			}

			failed := 0
			for _, r := range results {
				if r.Failed() {
					failed++
				}
			}
			if err := printResults(c, storage, results); err != nil {
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

func printResults(c *cli.Command, storage *grip.Storage, results []grip.VerifyResult) error {
	if c.Bool("json") {
		type result struct {
			grip.PackageJSON
			Status string `json:"status"`
		}
		out := []result{}
		for _, r := range results {
			inst, err := storage.Get(r.Name)
			if err != nil {
				return err
			}
			out = append(out, result{storage.PackageJSON(inst), r.Result})
		}
		return json.NewEncoder(c.Root().Writer).Encode(out)
	}

	tw := tabwriter.NewWriter(c.Root().Writer, 0, 0, 2, ' ', 0)
	var werr error
	row := func(format string, a ...any) {
		if _, err := fmt.Fprintf(tw, format, a...); werr == nil {
			werr = err
		}
	}
	row("NAME\tTAG\tRESULT\tDIGEST SOURCE\n")
	for _, r := range results {
		row("%s\t%s\t%s\t%s\n", r.Name, r.Tag, r.Result, cmp.Or(r.DigestSource, "unknown"))
	}
	return cmp.Or(werr, tw.Flush())
}
