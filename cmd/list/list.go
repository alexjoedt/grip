package list

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v3"
)

func Command(app *cli.Command, setup func() (*grip.Installer, *grip.Storage, error)) {
	cmd := &cli.Command{
		Name:  "ls",
		Usage: "lists all installed executables by grip",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "filter",
				Usage: "filters installed executables (format: field=regex)",
			},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			_, storage, err := setup()
			if err != nil {
				return err
			}
			installations, err := storage.List()
			if err != nil {
				return err
			}

			filter := c.String("filter")
			if filter != "" {
				parts := strings.Split(filter, "=")
				if len(parts) != 2 {
					return errors.New("invalid filter format, use: field=regex")
				}

				regEx, err := regexp.Compile(parts[1])
				if err != nil {
					return err
				}

				var filtered []*grip.Installation
				for _, inst := range installations {
					var value string
					switch parts[0] {
					case "name":
						value = inst.Name
					case "tag":
						value = inst.Tag
					case "repo":
						value = inst.Repo
					case "path":
						value = storage.InstallDir(inst)
					default:
						return errors.New("unsupported filter field, valid: name, tag, repo, path")
					}
					if regEx.MatchString(value) {
						filtered = append(filtered, inst)
					}
				}
				installations = filtered
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "NAME\tTAG\tPINNED\tREPO\tINSTALL PATH\n")

			for _, inst := range installations {
				pinned := "no"
				if inst.Pinned {
					pinned = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", inst.Name, inst.Tag, pinned, inst.Repo, storage.InstallDir(inst))
			}
			return tw.Flush()
		},
	}
	app.Commands = append(app.Commands, cmd)
}
