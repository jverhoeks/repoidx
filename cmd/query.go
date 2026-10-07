package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/store"
	"github.com/spf13/cobra"
)

// filterFlags are shared by list, search, stats and dupes.
type filterFlags struct {
	f       store.Filter
	all     bool
	tooling bool
}

func addFilterFlags(c *cobra.Command, ff *filterFlags) {
	fl := c.Flags()
	fl.StringVarP(&ff.f.Group, "group", "g", "", "only this group (glob, e.g. 'customer*')")
	fl.StringVar(&ff.f.Provider, "provider", "", "only this provider (github, gitlab, bitbucket, azure, ...)")
	fl.StringVar(&ff.f.Host, "host", "", "only this remote host (glob)")
	fl.StringVarP(&ff.f.Tag, "tag", "t", "", "only repos with this tag")
	fl.StringVarP(&ff.f.PathLike, "path", "p", "", "only repos whose path or name contains this")
	fl.BoolVar(&ff.f.Stale, "stale", false, "no local/remote activity for stale_days")
	fl.IntVar(&ff.f.StaleDays, "days", 0, "override stale_days")
	fl.BoolVar(&ff.f.Dirty, "dirty", false, "uncommitted or untracked changes")
	fl.BoolVar(&ff.f.Behind, "behind", false, "behind upstream")
	fl.BoolVar(&ff.f.Ahead, "ahead", false, "ahead of upstream")
	fl.BoolVar(&ff.f.Unpushed, "unpushed", false, "has commits that are on no remote")
	fl.BoolVar(&ff.f.NoRemote, "no-remote", false, "no remote configured")
	fl.BoolVar(&ff.f.WrongIdentity, "wrong-identity", false, "user.email differs from the group's identity")
	fl.BoolVar(&ff.f.Unsafe, "unsafe", false, "deleting would lose work (dirty, stash, unpushed, no remote)")
	fl.BoolVar(&ff.f.Safe, "safe", false, "fully pushed and clean: safe to delete")
	fl.BoolVar(&ff.f.Mine, "mine", false, "repos with commits by you")
	fl.BoolVarP(&ff.all, "all", "a", false, "include tooling repos (oh-my-zsh, vim plugins, ...)")
	fl.BoolVar(&ff.tooling, "tooling", false, "only tooling repos")
	fl.IntVarP(&ff.f.Limit, "limit", "n", 0, "max rows")
}

func (ff *filterFlags) build(a *app, identities []string) *store.Filter {
	f := ff.f
	f.IncludeTooling = ff.all
	f.OnlyTooling = ff.tooling
	if f.StaleDays == 0 {
		f.StaleDays = a.cfg.StaleDays
	}
	f.Identities = identities
	return &f
}

func (a *app) identities() []string {
	x, err := a.indexer()
	if err != nil {
		return a.cfg.Identities
	}
	return x.Identities()
}

func listCmd(a *app) *cobra.Command {
	var ff filterFlags
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List indexed repos with filters",
		Long:    "List indexed repos.\n\n" + stateLegend,
		Example: `  repoidx list --stale --safe            # cleanup candidates
  repoidx list -g customerA --dirty
  repoidx list --unsafe --sort last
  repoidx list --wrong-identity
  repoidx list --json | jq -r '.[].path'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			rows, err := a.store.List(ff.build(a, a.identities()))
			if err != nil {
				return err
			}
			if a.jsonOut {
				return writeJSON(os.Stdout, nonNil(rows))
			}
			printRepos(os.Stdout, rows)
			return nil
		},
	}
	addFilterFlags(c, &ff)
	c.Flags().StringVarP(&ff.f.Sort, "sort", "s", "path", "path, name, group, last, oldest, size, files, commits, mine")
	return c
}

func searchCmd(a *app) *cobra.Command {
	var ff filterFlags
	c := &cobra.Command{
		Use:   "search <words...>",
		Short: "Full-text search over name, remote, group, path and README",
		Long: `Full-text search. Every word must match (prefix match: 'terra' finds
'terraform'). Prefix a word with '-' to exclude it. Filters from 'list' apply.`,
		Example: `  repoidx search terraform eks
  repoidx search vpc -g customerB
  cd "$(repoidx search -n1 --paths myproj)"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			if ff.f.Limit == 0 {
				ff.f.Limit = 50
			}
			rows, err := a.store.Search(strings.Join(args, " "), ff.build(a, a.identities()))
			if err != nil {
				return err
			}
			if a.jsonOut {
				return writeJSON(os.Stdout, nonNil(rows))
			}
			if pathsOnly, _ := cmd.Flags().GetBool("paths"); pathsOnly {
				for _, r := range rows {
					fmt.Println(r.Path)
				}
				return nil
			}
			if len(rows) == 0 {
				fmt.Fprintln(os.Stderr, "no matches")
				return nil
			}
			// manual widths: snippet lines must not stretch the columns
			var wn, wg, ws int
			for _, r := range rows {
				wn = max(wn, len(r.Name))
				wg = max(wg, len(r.Group))
				ws = max(ws, len([]rune(state(r))))
			}
			for _, r := range rows {
				st := state(r)
				fmt.Printf("%-*s  %-*s  %s%*s  %s\n", wn, r.Name, wg, r.Group, st, ws-len([]rune(st)), "", config.Shorten(r.Path))
				if r.Snippet != "" {
					fmt.Printf("    %s\n", r.Snippet)
				}
			}
			return nil
		},
	}
	addFilterFlags(c, &ff)
	c.Flags().Bool("paths", false, "print only paths")
	return c
}

func showCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <repo>",
		Short: "Show everything known about a repo (path, name, project or slug)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := findOne(a, args[0])
			if err != nil {
				return err
			}
			d, err := a.store.Detail(r, 10)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return writeJSON(os.Stdout, struct {
					*store.Repo
					*store.Detail
				}{r, d})
			}
			tw := table(os.Stdout)
			kv := func(k, v string) {
				if v != "" {
					fmt.Fprintf(tw, "%s\t%s\n", k, v)
				}
			}
			kv("path", r.Path)
			kv("group", fmt.Sprintf("%s (%s)", r.Group, r.GroupSource))
			kv("tags", strings.Join(r.Tags, ", "))
			var kind []string
			for _, k := range []struct {
				name string
				on   bool
			}{{"bare", r.Bare}, {"worktree", r.Worktree}, {"submodule", r.Submodule}, {"tooling", r.Tooling}} {
				if k.on {
					kind = append(kind, k.name)
				}
			}
			kv("kind", strings.Join(kind, ", "))
			kv("branch", branch(r))
			kv("upstream", r.Upstream)
			kv("default", r.DefaultBranch)
			kv("state", state(r))
			for _, rm := range d.Remotes {
				label := rm.Provider
				if rm.Alias != "" {
					label += ", via ssh alias " + rm.Alias
				}
				kv("remote "+rm.Name, fmt.Sprintf("%s  [%s]  %s", rm.Slug, label, rm.URL))
			}
			kv("last local", when(r.LastLocalCommit))
			kv("last remote", when(r.LastRemoteCommit))
			kv("last fetch", when(r.LastFetch))
			kv("last activity", when(r.LastActivity))
			kv("files", fmt.Sprintf("%d tracked, .git %s", r.Files, size(r.GitSizeKB)))
			kv("commits", fmt.Sprintf("%d local, %d on %s", r.TotalCommits, r.RemoteCommits, orDash(r.RemoteRef)))
			kv("mine", fmt.Sprintf("%d local, %d remote", r.MyCommits, r.MyRemoteCommits))
			id := r.UserEmail
			if r.ExpectedEmail != "" && r.ExpectedEmail != r.UserEmail {
				id += "  (expected " + r.ExpectedEmail + " for group " + r.Group + ")"
			}
			kv("user.email", id)
			kv("scanned", when(r.ScannedAt))
			kv("errors", r.ScanError)
			tw.Flush()

			if len(d.Authors) > 0 {
				fmt.Println("\ntop authors (local / remote):")
				tw = table(os.Stdout)
				for _, au := range d.Authors {
					fmt.Fprintf(tw, "  %d\t%d\t%s <%s>\n", au.Local, au.Remote, au.Name, au.Email)
				}
				tw.Flush()
			}
			if len(d.Related) > 0 {
				fmt.Println("\nrelated:")
				for _, x := range d.Related {
					fmt.Printf("  %-8s %s\n", x.Kind, config.Shorten(x.Path))
				}
			}
			if d.Readme != "" {
				fmt.Printf("\n%s:\n", r.ReadmeName)
				lines := strings.Split(strings.TrimSpace(d.Readme), "\n")
				if len(lines) > 12 {
					lines = append(lines[:12], "…")
				}
				for _, l := range lines {
					fmt.Println("  " + l)
				}
			}
			return nil
		},
	}
}

func pathCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "path <repo>",
		Short:   "Print the path of a repo, e.g. cd \"$(repoidx path myproj)\"",
		Args:    cobra.ExactArgs(1),
		Example: `  cd "$(repoidx path repoidx)"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := findOne(a, args[0])
			if err != nil {
				return err
			}
			fmt.Println(r.Path)
			return nil
		},
	}
}

// findOne resolves a repo reference to exactly one indexed repo.
func findOne(a *app, ref string) (*store.Repo, error) {
	if err := a.open(); err != nil {
		return nil, err
	}
	f := &store.Filter{StaleDays: a.cfg.StaleDays, Identities: a.identities()}
	if abs, err := absPath(ref); err == nil {
		if rows, _ := a.store.Find(abs, f); len(rows) == 1 && rows[0].Path == abs {
			return rows[0], nil
		}
	}
	rows, err := a.store.Find(ref, f)
	if err != nil {
		return nil, err
	}
	switch len(rows) {
	case 0:
		return nil, fmt.Errorf("no indexed repo matches %q", ref)
	case 1:
		return rows[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d repos, be more specific:", ref, len(rows))
	for i, r := range rows {
		if i == 10 {
			fmt.Fprintf(&b, "\n  …")
			break
		}
		fmt.Fprintf(&b, "\n  %s", config.Shorten(r.Path))
	}
	return nil, fmt.Errorf("%s", b.String())
}

func absPath(p string) (string, error) {
	p = config.Expand(p)
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
