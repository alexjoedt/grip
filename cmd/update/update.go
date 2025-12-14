package update

import (
	"context"
	"fmt"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/alexjoedt/grip/internal/logger"
	"github.com/urfave/cli/v2"
)

func Command(app *cli.App, installer *grip.Installer, storage *grip.Storage, cfg *grip.Config) {
	cmd := &cli.Command{
		Name:  "update",
		Usage: "updates an executable",
		Action: func(c *cli.Context) error {
			name := c.Args().First()
			if name == "" {
				return fmt.Errorf("please provide the name of the package to update")
			}

			inst, err := storage.Get(name)
			if err != nil {
				return fmt.Errorf("package not found: %s", name)
			}

			oldTag := inst.Tag

			if err := installer.Update(context.Background(), name); err != nil {
				return err
			}

			// Get updated installation to show new version
			updated, _ := storage.Get(name)
			if updated != nil && updated.Tag != oldTag {
				logger.Success("%s updated successfully from %s to %s", name, oldTag, updated.Tag)
			}

			return nil
		},
	}

	selfCmd := &cli.Command{
		Name:  "self-update",
		Usage: "updates grip",
		Action: func(c *cli.Context) error {
			return grip.SelfUpdate(app.Version)
		},
	}

	app.Commands = append(app.Commands, cmd, selfCmd)
}
