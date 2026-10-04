package remove

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/alexjoedt/grip/internal/logger"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
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
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "prints what would be removed and changes nothing",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			installer, storage, err := setup()
			if err != nil {
				return err
			}
			if c.Bool("dry-run") {
				return dryRun(c, installer, storage)
			}
			if c.Bool("all") {
				if !c.Bool("force") && !askForContinue() {
					return errAborted
				}

				installations, err := storage.List()
				if err != nil {
					return err
				}

				failed := 0
				for _, inst := range installations {
					if err := installer.Remove(ctx, inst.Name); err != nil {
						logger.Error("Failed to remove %s: %v", inst.Name, err)
						failed++
					}
				}
				if failed > 0 {
					return fmt.Errorf("%d of %d removals failed", failed, len(installations))
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

			if !c.Bool("force") && !askForContinue() {
				return errAborted
			}

			return installer.Remove(ctx, name)
		},
	}
	app.Commands = append(app.Commands, cmd)
}

// dryRun prints the link, store directory and state entry that remove
// would delete. It takes no lock.
func dryRun(c *cli.Command, installer *grip.Installer, storage *grip.Storage) error {
	var insts []*grip.Installation
	if c.Bool("all") {
		all, err := storage.List()
		if err != nil {
			return err
		}
		insts = all
		slices.SortFunc(insts, func(a, b *grip.Installation) int { return strings.Compare(a.Name, b.Name) })
	} else {
		if c.NArg() == 0 {
			return fmt.Errorf("please provide the name of the executable to remove")
		}
		inst, err := storage.Get(c.Args().First())
		if err != nil {
			return err
		}
		insts = append(insts, inst)
	}
	w := c.Root().Writer
	for _, inst := range insts {
		if _, err := fmt.Fprintf(w, "%s: would remove link %s, store %s and the state entry\n",
			inst.Name, filepath.Join(storage.InstallDir(inst), inst.Name), installer.StoreDir(inst.Name)); err != nil {
			return err
		}
	}
	return nil
}

var errAborted = errors.New("aborted")

// askForContinue reads a y/N answer from stdin; EOF counts as no.
func askForContinue() bool {
	reader := bufio.NewReader(os.Stdin)
	logger.Print("Are you sure you want to continue? [y/N]: ")

	input, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		logger.Error("Error reading input: %v", err)
	}
	return strings.ToLower(strings.TrimSpace(input)) == "y"
}
