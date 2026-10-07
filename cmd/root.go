// Package cmd implements the repoidx command line.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/gitinfo"
	"github.com/jverhoeks/repoidx/internal/indexer"
	"github.com/jverhoeks/repoidx/internal/store"
	"github.com/spf13/cobra"
)

var version = "dev"

// app is shared state for all commands.
type app struct {
	configPath string
	dbPath     string
	jsonOut    bool

	cfg   *config.Config
	store *store.Store
}

func (a *app) load() error {
	if a.cfg != nil {
		return nil
	}
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return err
	}
	if a.dbPath != "" {
		cfg.DB = config.Expand(a.dbPath)
	}
	a.cfg = cfg
	return nil
}

func (a *app) open() error {
	if err := a.load(); err != nil {
		return err
	}
	if a.store != nil {
		return nil
	}
	st, err := store.Open(a.cfg.DB)
	if err != nil {
		return err
	}
	a.store = st
	return nil
}

func (a *app) indexer() (*indexer.Indexer, error) {
	if err := gitinfo.Available(); err != nil {
		return nil, errors.New("git not found in PATH")
	}
	if err := a.open(); err != nil {
		return nil, err
	}
	return indexer.New(a.cfg, a.store), nil
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	a := &app{}
	root := &cobra.Command{
		Use:   "repoidx",
		Short: "Index, search and inspect the git repositories on this machine",
		Long: `repoidx finds git repositories, records their state (remote, branch,
ahead/behind, dirty, stale, file count, your commits, README) in a local
SQLite index, and lets you list, filter and search them.

Works without configuration: it scans your home directory, skips dependency
and cache folders, reads identities and includeIf groups from your git config
and resolves SSH host aliases via ssh -G. See 'repoidx config show'.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.configPath, "config", "", "config file (default "+config.Shorten(config.DefaultPath())+")")
	root.PersistentFlags().StringVar(&a.dbPath, "db", "", "index database (default "+config.Shorten(config.DefaultDB())+")")
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "output JSON")

	root.AddCommand(
		scanCmd(a), updateCmd(a),
		listCmd(a), searchCmd(a), showCmd(a), pathCmd(a),
		statsCmd(a), dupesCmd(a),
		tagCmd(a), groupCmd(a), configCmd(a),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := root.ExecuteContext(ctx)
	if a.store != nil {
		a.store.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "repoidx:", err)
		return 1
	}
	return 0
}
