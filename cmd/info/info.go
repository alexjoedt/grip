package info

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:      "info",
		Usage:     "shows the recorded state of an installed executable",
		ArgsUsage: "<name>",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "json",
				Usage: "print JSON instead of a table",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			if c.NArg() != 1 {
				return fmt.Errorf("usage: grip info <name>")
			}
			_, storage, err := setup()
			if err != nil {
				return err
			}
			inst, err := storage.Get(c.Args().First())
			if err != nil {
				return err
			}

			if c.Bool("json") {
				return json.NewEncoder(c.Root().Writer).Encode(storage.PackageJSON(inst))
			}

			link := filepath.Join(storage.InstallDir(inst), inst.Name)
			overrides := func(s string) string { return cmp.Or(s, "none") }
			pinned := "no"
			if inst.Pinned {
				pinned = "yes"
			}

			tw := tabwriter.NewWriter(c.Root().Writer, 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "name:\t%s\n", inst.Name)
			fmt.Fprintf(tw, "repo:\t%s\n", inst.Repo)
			fmt.Fprintf(tw, "tag:\t%s\n", inst.Tag)
			fmt.Fprintf(tw, "asset:\t%s\n", unknown(inst.Asset))
			fmt.Fprintf(tw, "asset digest:\t%s (%s)\n", unknown(inst.AssetDigest), unknown(inst.DigestSource))
			fmt.Fprintf(tw, "sha256:\t%s\n", unknown(inst.SHA256))
			fmt.Fprintf(tw, "installed:\t%s\n", installedAt(inst.InstalledAt))
			fmt.Fprintf(tw, "pinned:\t%s\n", pinned)
			fmt.Fprintf(tw, "asset override:\t%s\n", overrides(inst.AssetOverride))
			fmt.Fprintf(tw, "bin override:\t%s\n", overrides(inst.BinOverride))
			fmt.Fprintf(tw, "link:\t%s\n", link)
			fmt.Fprintf(tw, "store:\t%s\n", storePath(link))
			if p := inst.Previous; p != nil {
				fmt.Fprintf(tw, "previous:\t%s, installed %s\n", p.Tag, installedAt(p.InstalledAt))
			}
			return tw.Flush()
		},
	}
	app.Commands = append(app.Commands, cmd)
}

func unknown(s string) string {
	return cmp.Or(s, "unknown")
}

func installedAt(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Local().Format(time.DateTime)
}

// storePath resolves the link one step, without requiring the target to exist.
func storePath(link string) string {
	target, err := os.Readlink(link)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "missing"
	case err != nil:
		return "not a link"
	case filepath.IsAbs(target):
		return target
	}
	return filepath.Join(filepath.Dir(link), target)
}
