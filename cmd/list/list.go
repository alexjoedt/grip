package list

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v2"
)

type Config struct {
	Filter string
}

func Command(app *cli.App) *Config {
	cfg := Config{}
	cmd := &cli.Command{
		Name:   "ls",
		Usage:  "lists all installed executables by grip",
		Action: cfg.Action,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "filter",
				Usage:       "filters installed executables",
				Destination: &cfg.Filter,
			},
		},
	}

	app.Commands = append(app.Commands, cmd)
	return &cfg
}

func (cfg *Config) Action(cCtx *cli.Context) error {

	filteredEntries := make([]grip.RepoEntry, 0)

	if cfg.Filter != "" {

		rawLines, err := grip.GetLockFileLines()
		if err != nil {
			return err
		}
		parts := strings.Split(cfg.Filter, "=")
		if len(parts) != 2 {
			return errors.New("invalid filter")
		}

		var index int
		switch parts[0] {
		case "name":
			index = 0
		case "tag":
			index = 1
		case "repo":
			index = 2
		case "path":
			index = 3
		default:
			return errors.New("unsupported key for filter, valid filters: name, tag, repo, path")
		}

		regEx, err := regexp.Compile(parts[1])
		if err != nil {
			return err
		}

		for _, line := range rawLines {
			if regEx.MatchString(line[index]) {
				entry := grip.RepoEntry{
					Name:        line[0],
					Tag:         line[1],
					Repo:        line[2],
					InstallPath: line[3],
				}
				filteredEntries = append(filteredEntries, entry)
			}
		}

	} else {

		entries, err := grip.ReadLockFile()
		if err != nil {
			return err
		}
		filteredEntries = entries

	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "NAME\tTAG\tREPO\tINSTALL PATH\n")

	for _, e := range filteredEntries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, e.Tag, e.Repo, e.InstallPath)
	}
	return tw.Flush()
}
