package install

import (
	"fmt"
	"os"
	"os/exec"

	grip "github.com/alexjoedt/grip/internal"
	"github.com/urfave/cli/v2"
)

type Config struct {
	Tag         string
	Destination string
	Force       bool
	Alias       string
}

func Command(app *cli.App) *Config {
	cfg := Config{}
	cmd := &cli.Command{
		Name:   "install",
		Usage:  "install an executable from a GitHub release",
		Flags:  getFlags(&cfg),
		Action: cfg.Action,
	}
	app.Commands = append(app.Commands, cmd)

	return &cfg
}

func getFlags(cfg *Config) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "tag",
			Aliases:     []string{"t"},
			Usage:       "release tag",
			Destination: &cfg.Tag,
		},
		&cli.StringFlag{
			Name:        "destination",
			Aliases:     []string{"d"},
			Usage:       "specifies the installation path",
			Value:       grip.InstallPath,
			Destination: &cfg.Destination,
		},
		&cli.BoolFlag{
			Name:        "force",
			Aliases:     []string{"f"},
			Usage:       "forces the installation",
			Destination: &cfg.Force,
		},
		&cli.StringFlag{
			Name:        "alias",
			Aliases:     []string{"a"},
			Usage:       "alias for the executeable",
			Destination: &cfg.Alias,
		},
	}
}

func (c *Config) Action(ctx *cli.Context) error {

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

	if c.Alias != "" {
		name = c.Alias
		asset.Alias = c.Alias
	}

	entry, err := grip.GetEntryByRepo(repo)
	if nil == err {
		if entry.Name == name && entry.Repo == repo && !c.Force {
			return fmt.Errorf("%s version %s is already installed", name, asset.Tag)
		}
	}

	// dont install, when the project is already installed from
	// another source
	if _, err := exec.LookPath(name); err == nil {
		// there is already a executeable with the same name
		if entry == nil {
			return fmt.Errorf("%s is already installed from another source", name)
		}
	}

	err = asset.Install(c.Destination)
	if err != nil {
		return err
	}

	grip.CheckPathEnv()
	fmt.Fprintf(os.Stdout, "\n --> %s@%s installed successfully\n", asset.BinaryName(), asset.Tag)

	err = grip.UpdateEntry(grip.RepoEntry{
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
