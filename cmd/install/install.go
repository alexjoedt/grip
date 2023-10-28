package install

import (
	"fmt"
	"os"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v2"
)

type config struct {
	Tag         string
	Destination string
	Force       bool
}

func New(rootCmd *cli.App) (*config, error) {
	config := config{}
	cmd := &cli.Command{
		Name:  "install",
		Usage: "install a executeable from a github release",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "tag",
				Aliases:     []string{"t"},
				Usage:       "release tag",
				Destination: &config.Tag,
			},
			&cli.StringFlag{
				Name:        "destination",
				Aliases:     []string{"d"},
				Usage:       "specifies the installation path",
				Value:       grip.InstallPath,
				Destination: &config.Destination,
			},
			&cli.BoolFlag{
				Name:        "force",
				Aliases:     []string{"f"},
				Usage:       "forces the installation",
				Value:       false,
				Destination: &config.Force,
			},
		},
		Action: config.Action,
	}

	rootCmd.Commands = append(rootCmd.Commands, cmd)
	return nil, nil
}

func (c *config) Action(ctx *cli.Context) error {

	repo := ctx.Args().Get(0)
	owner, name, err := grip.ParseRepoPath(repo)
	if err != nil {
		return err
	}

	var asset *grip.Asset

	if c.Tag == "" { // get latest

		asset, err = grip.GetLatest(owner, name)
		if err != nil {
			return err
		}

	} else { // get by tag

		asset, err = grip.GetByTag(owner, name, c.Tag)
		if err != nil {
			return err
		}
	}

	entry, err := grip.GetEntryByRepo(grip.Lockfile, repo)
	if nil == err {
		if entry.Name == name && entry.Repo == repo && !c.Force {
			return fmt.Errorf("%s version %s is already installed", name, asset.Tag)
		}
	}

	err = asset.Install(c.Destination)
	if err != nil {
		return err
	}

	grip.CheckPathEnv()
	fmt.Fprintf(os.Stdout, "\n --> %s@%s installed successfully\n", asset.BinaryName(), asset.Tag)

	err = grip.UpdateEntry(grip.Lockfile, grip.RepoEntry{
		Name:        name,
		Tag:         asset.Tag,
		Repo:        repo,
		InstallPath: c.Destination,
	})
	if err != nil {
		return err
	}

	return nil
}
