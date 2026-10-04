package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:      "sync",
		Usage:     "installs the executables of a manifest written by grip export",
		ArgsUsage: "<file|->",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() != 1 {
				return errors.New("expected one manifest file, - reads stdin")
			}
			m, err := read(c.Args().First(), c.Root().Reader)
			if err != nil {
				return err
			}
			installer, _, err := setup()
			if err != nil {
				return err
			}
			return installer.Sync(ctx, m)
		},
	}
	app.Commands = append(app.Commands, cmd)
}

func read(path string, stdin io.Reader) (grip.Manifest, error) {
	var m grip.Manifest
	r := stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return m, err
		}
		defer f.Close()
		r = f
	}
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return m, fmt.Errorf("read manifest %s: %w", path, err)
	}
	return m, nil
}
