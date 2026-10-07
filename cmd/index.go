package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/gitinfo"
	"github.com/jverhoeks/repoidx/internal/indexer"
	"github.com/spf13/cobra"
)

func scanCmd(a *app) *cobra.Command {
	var opt indexer.ScanOptions
	var verbose bool
	c := &cobra.Command{
		Use:   "scan [path...]",
		Short: "Discover repos below paths (default: configured roots, i.e. ~) and index them",
		Long: `Walks the given folders (default: the configured roots, ~ without config),
finds git repositories and indexes them. Repos that were indexed below these
folders but are no longer found are removed from the index.

Skips dependency/cache folders (node_modules, .terraform, .venv, ~/Library, ...);
see 'repoidx config show'. Never fetches or modifies repositories.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := a.indexer()
			if err != nil {
				return err
			}
			roots := args
			if len(roots) == 0 {
				roots = append([]string(nil), a.cfg.Roots...)
			}
			for i, r := range roots {
				abs, err := absPath(r)
				if err != nil {
					return err
				}
				if st, err := os.Stat(abs); err != nil || !st.IsDir() {
					return fmt.Errorf("not a directory: %s", r)
				}
				roots[i] = abs
			}
			pr := newProgress()
			x.Progress = pr.update
			var errs []string
			x.OnError = func(p string, err error) {
				errs = append(errs, fmt.Sprintf("%s: %v", config.Shorten(p), err))
			}
			var unreadable []string
			x.OnUnreadable = func(dir string, err error) {
				unreadable = append(unreadable, fmt.Sprintf("%s: %v", config.Shorten(dir), errors.Unwrap(err)))
			}
			res, err := x.Scan(cmd.Context(), roots, opt)
			pr.done()
			if a.jsonOut {
				_ = writeJSON(os.Stdout, map[string]any{"result": res, "errors": errs, "unreadable": unreadable})
				return err
			}
			report(a, res, errs, verbose)
			if len(unreadable) > 0 {
				fmt.Fprintf(os.Stderr, "%d directories could not be read (repos inside are kept in the index)", len(unreadable))
				if !verbose {
					fmt.Fprintln(os.Stderr, "; -v to list")
				} else {
					fmt.Fprintln(os.Stderr, ":")
					for _, u := range unreadable {
						fmt.Fprintln(os.Stderr, "  "+u)
					}
				}
			}
			return err
		},
	}
	c.Flags().BoolVar(&opt.Nested, "nested", false, "also index repos nested inside other repos")
	c.Flags().BoolVar(&opt.Force, "force", false, "recollect everything, even repos whose refs did not change")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "print every error")
	return c
}

func updateCmd(a *app) *cobra.Command {
	var opt indexer.UpdateOptions
	var verbose bool
	var fetchTimeout int
	c := &cobra.Command{
		Use:   "update [repo...]",
		Short: "Refresh already-indexed repos without walking the disk",
		Long: `Re-reads git state for indexed repos (all, or the given ones) and
removes repos that no longer exist. Expensive data (commit counts, README,
file count) is only recollected when a ref moved, unless --force.

--fetch runs 'git fetch --all --prune' first so ahead/behind and remote dates
reflect the server. This is the only operation that changes your repos
(remote-tracking refs only); SSH runs in batch mode and never prompts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := a.indexer()
			if err != nil {
				return err
			}
			for _, ref := range args {
				r, err := findOne(a, ref)
				if err != nil {
					return err
				}
				opt.Paths = append(opt.Paths, r.Path)
			}
			opt.FetchTimeout = time.Duration(fetchTimeout) * time.Second
			pr := newProgress()
			x.Progress = pr.update
			var errs []string
			x.OnError = func(p string, err error) {
				errs = append(errs, fmt.Sprintf("%s: %v", config.Shorten(p), err))
			}
			if opt.Fetch && pr.tty {
				fmt.Fprintln(os.Stderr, "fetching…")
			}
			res, err := x.Update(cmd.Context(), opt)
			pr.done()
			report(a, res, errs, verbose)
			return err
		},
	}
	c.Flags().BoolVar(&opt.Fetch, "fetch", false, "git fetch --all --prune each repo first (network)")
	c.Flags().IntVar(&fetchTimeout, "fetch-timeout", 60, "per-repo fetch timeout in seconds")
	c.Flags().BoolVar(&opt.Force, "force", false, "recollect everything, even repos whose refs did not change")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "print every error")
	return c
}

func report(a *app, res *indexer.Result, errs []string, verbose bool) {
	if res == nil {
		return
	}
	if a.jsonOut {
		_ = writeJSON(os.Stdout, map[string]any{"result": res, "errors": errs})
		return
	}
	fmt.Printf("%d repos: %d indexed (%d unchanged), %d failed, %d removed in %s\n",
		res.Found, res.Indexed, res.Light, res.Failed, res.Removed, res.Elapsed.Round(time.Millisecond))
	if res.Fetched+res.FetchFailed > 0 {
		fmt.Printf("fetch: %d ok, %d failed\n", res.Fetched, res.FetchFailed)
	}
	if len(res.StaleIndex) > 0 {
		fmt.Fprintf(os.Stderr, "%d repo(s) have stale file timestamps in their index (copied or restored),\n"+
			"so git re-reads every file and status takes over %s. repoidx never writes\n"+
			"the index; run 'git status' once in each to refresh it:\n", len(res.StaleIndex), gitinfo.SlowStatus)
		for _, p := range res.StaleIndex {
			fmt.Fprintln(os.Stderr, "  "+config.Shorten(p))
		}
	}
	if len(errs) == 0 {
		return
	}
	n := len(errs)
	if !verbose && n > 5 {
		errs = errs[:5]
	}
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "  "+e)
	}
	if len(errs) < n {
		fmt.Fprintf(os.Stderr, "  … and %d more (use -v)\n", n-len(errs))
	}
}
