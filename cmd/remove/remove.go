package remove

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/alexjoedt/grip/internal/logger"
	"github.com/urfave/cli/v2"
)

func Command(app *cli.App, storage *grip.Storage, cfg *grip.Config) {
	cmd := &cli.Command{
		Name:        "remove",
		Usage:       "removes an installed executable by grip",
		Description: "removes an installed executable by grip",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "all",
				Aliases: []string{"a"},
				Usage:   "removes all executables installed by grip",
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "forces remove without confirmation",
			},
		},
		Action: func(c *cli.Context) error {
			if c.Bool("all") {
				if !c.Bool("force") {
					if !askForContinue() {
						return nil
					}
				}

				installations, err := storage.List()
				if err != nil {
					return err
				}

				for _, inst := range installations {
					p := filepath.Join(inst.InstallPath, inst.Name)
					if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
						logger.Error("Failed to remove: %s", p)
						continue
					}

					if err := storage.Delete(inst.Name); err != nil {
						logger.Error("Failed to update storage: %v", err)
						continue
					}

					logger.Success("Removed: %s", p)
				}
				return nil
			}

			name := c.Args().First()
			if name == "" {
				return fmt.Errorf("please provide the name of the executable to remove")
			}

			if strings.HasPrefix(name, "github.com") {
				return fmt.Errorf("please provide the name or alias, not the repo path")
			}

			inst, err := storage.Get(name)
			if err != nil {
				return fmt.Errorf("package not found: %s", name)
			}

			if !c.Bool("force") {
				if !askForContinue() {
					return nil
				}
			}

			p := filepath.Join(inst.InstallPath, inst.Name)
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("failed to remove binary: %w", err)
			}

			if err := storage.Delete(inst.Name); err != nil {
				return fmt.Errorf("failed to update storage: %w", err)
			}

			logger.Success("Removed: %s", name)
			return nil
		},
	}
	app.Commands = append(app.Commands, cmd)
}

func askForContinue() bool {
	reader := bufio.NewReader(os.Stdin)
	logger.Print("Are you sure you want to continue? [y/N]: ")

	input, err := reader.ReadString('\n')
	if err != nil {
		logger.Error("Error reading input: %v", err)
		return false
	}

	return strings.ToLower(strings.TrimSpace(input)) == "y"
}
