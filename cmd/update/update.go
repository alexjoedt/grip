package update

import (
	"fmt"
	"os"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v2"
)

type Config struct {
	version string
}

func Command(app *cli.App) *Config {
	cfg := Config{
		version: app.Version,
	}
	cmd := &cli.Command{
		Name:   "update",
		Usage:  "updates an executable",
		Action: cfg.Action,
	}

	selfCmd := &cli.Command{
		Name:  "self-update",
		Usage: "updates grip",
		Action: cfg.SelfUpdate,
	}

	app.Commands = append(app.Commands, cmd, selfCmd)

	return &cfg
}

func (c *Config) SelfUpdate(ctx *cli.Context) error {
	return grip.SelfUpdate(c.version)
}

func (c *Config) Action(ctx *cli.Context) error {

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
		Repo:        entry.Repo,
		InstallPath: entry.InstallPath,
	})
	if err != nil {
		return err
	}

	return nil
}
