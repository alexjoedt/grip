package update

import (
	"fmt"
	"os"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v2"
)

type Config struct {
	version string
	Self    bool
}

func Command(app *cli.App) *Config {
	cfg := Config{
		version: app.Version,
	}
	cmd := &cli.Command{
		Name:   "update",
		Usage:  "updates an executable",
		Flags:  getFlags(&cfg),
		Action: cfg.Action,
	}
	app.Commands = append(app.Commands, cmd)

	return &cfg
}

func getFlags(cfg *Config) []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:        "self",
			Usage:       "self update",
			Destination: &cfg.Self,
		},
	}
}

func (c *Config) Action(ctx *cli.Context) error {

	if c.Self {
		return grip.SelfUpdate(c.version)
	}

	// TODO: update all installed executables by repo
	name := ctx.Args().Get(0)
	entry, err := grip.GetEntryByName(name)
	if nil != err {
		return err
	}

	repoOwner, repoName, err := grip.ParseRepoPath(entry.Repo)
	if err != nil {
		return err
	}

	var asset *grip.Asset
	asset, err = grip.GetLatest(repoOwner, repoName)
	if err != nil {
		return err
	}

	// TODO: compare semversion, dont install if the fetched version is lower then the current
	if asset.Tag == entry.Tag {
		return fmt.Errorf("version %s already installed", entry.Tag)
	}

	asset.Alias = name

	err = asset.Install(entry.InstallPath)
	if err != nil {
		return err
	}

	grip.CheckPathEnv()
	fmt.Fprintf(os.Stdout, "\n --> %s updated successfully from %s to %s\n", asset.BinaryName(), entry.Tag, asset.Tag)

	err = grip.UpdateEntry(grip.RepoEntry{
		Name:        name,
		Tag:         asset.Tag,
		Repo:        repoOwner,
		InstallPath: entry.InstallPath,
	})
	if err != nil {
		return err
	}

	return nil
}
