package export

import (
	"context"
	"encoding/json"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:  "export",
		Usage: "prints a manifest of the installed executables for grip sync",
		Action: func(_ context.Context, c *cli.Command) error {
			installer, _, err := setup()
			if err != nil {
				return err
			}
			m, err := installer.Export()
			if err != nil {
				return err
			}
			enc := json.NewEncoder(c.Root().Writer)
			enc.SetIndent("", "  ")
			return enc.Encode(m)
		},
	}
	app.Commands = append(app.Commands, cmd)
}
