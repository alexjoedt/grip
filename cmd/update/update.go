package update

import (
	"context"
	"fmt"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/alexjoedt/grip/internal/logger"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, installer *grip.Installer, storage *grip.Storage, version string) {
	cmd := &cli.Command{
		Name:  "update",
		Usage: "updates an executable",
		Action: func(ctx context.Context, c *cli.Command) error {
			name := c.Args().First()
			if name == "" {
				return fmt.Errorf("please provide the name of the package to update")
			}

			inst, err := storage.Get(name)
			if err != nil {
				return fmt.Errorf("package not found: %s", name)
			}

			oldTag := inst.Tag

			if err := installer.Update(ctx, name); err != nil {
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
		Action: func(ctx context.Context, _ *cli.Command) error {
			return grip.SelfUpdate(ctx, version, installer)
		},
	}

	app.Commands = append(app.Commands, cmd, selfCmd)
}
