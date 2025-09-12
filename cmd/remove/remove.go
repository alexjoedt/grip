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

type Config struct {
	All   bool
	Force bool
}

func Command(app *cli.App) *Config {
	cfg := Config{}

	cmd := &cli.Command{
		Name:        "remove",
		Usage:       "removes a installed executable by grip",
		Description: "removes a installed executable by grip",
		Action:      cfg.Action,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "all",
				Aliases:     []string{"a"},
				Usage:       "removes all executeables installed by grip",
				Destination: &cfg.All,
			},
			&cli.BoolFlag{
				Name:        "force",
				Aliases:     []string{"f"},
				Usage:       "forces remove without confirmation",
				Destination: &cfg.Force,
			},
		},
	}

	app.Commands = append(app.Commands, cmd)
	return &cfg
}

func (cfg *Config) Action(cCtx *cli.Context) error {

	if cfg.All {

		if !cfg.Force {
			if !askForContinue() {
				return nil
			}
		}

		entries, err := grip.GetAllEntries()
		if err != nil {
			return err
		}

		for _, e := range entries {
			p := filepath.Join(e.InstallPath, e.Name)
			if _, err := os.Stat(p); err == nil {
				err := os.Remove(p)
				if err != nil {
					logger.Error("Failed to remove: %s", p)
				} else {
					err := grip.DeleteEntryByRepo(e.Repo)
					if err != nil {
						return err
					}
					logger.Success("Removed: %s", p)
				}
			}
		}

	} else {

		arg := cCtx.Args().First()
		isRepo := strings.HasPrefix(arg, "github.com")

		var entry *grip.RepoEntry
		var err error

		if isRepo {
			return fmt.Errorf("please provide the name or alias for the executeable")
		} else {
			entry, err = grip.GetEntryByName(arg)
			if err != nil {
				return err
			}
		}

		if !cfg.Force {
			if !askForContinue() {
				return nil
			}
		}

		err = os.Remove(filepath.Join(entry.InstallPath, entry.Name))
		if err != nil {
			return err
		}

		err = grip.DeleteEntryByName(entry.Name)
		if err != nil {
			return err
		}

	}

	return nil
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
