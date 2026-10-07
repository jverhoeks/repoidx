package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/spf13/cobra"
)

func tagCmd(a *app) *cobra.Command {
	var rm bool
	c := &cobra.Command{
		Use:     "tag <repo> <tag...>",
		Short:   "Add (or --rm remove) free-form tags",
		Example: "  repoidx tag repoidx tools go\n  repoidx list -t tools",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := findOne(a, args[0])
			if err != nil {
				return err
			}
			tags := args[1:]
			for i, t := range tags {
				tags[i] = strings.ToLower(strings.TrimSpace(t))
			}
			if rm {
				return a.store.RemoveTags(r.ID, tags...)
			}
			return a.store.AddTags(r.ID, tags...)
		},
	}
	c.Flags().BoolVar(&rm, "rm", false, "remove the tags instead")
	return c
}

func groupCmd(a *app) *cobra.Command {
	var clear bool
	c := &cobra.Command{
		Use:   "group <repo> [name]",
		Short: "Pin a repo to a group (overrides rules), or --clear the pin",
		Long: `Groups are normally computed, in this order:
  1. manual pin (this command)
  2. 'groups' rules in the config file
  3. your global git includeIf sections (gitdir: and hasconfig:remote.*.url:)
  4. host/namespace of the primary remote, or 'local'`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := findOne(a, args[0])
			if err != nil {
				return err
			}
			name := ""
			if !clear {
				if len(args) != 2 {
					return errors.New("give a group name, or --clear")
				}
				name = args[1]
			}
			if err := a.store.SetOverride(r.Path, name); err != nil {
				return err
			}
			x, err := a.indexer()
			if err != nil {
				return err
			}
			return x.Regroup()
		},
	}
	c.Flags().BoolVar(&clear, "clear", false, "remove the manual group")
	return c
}

func configCmd(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Show or create the configuration",
	}
	c.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration, identities and derived groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			inc := config.IncludeIfGroups()
			ids := a.cfg.Identities
			if _, err := os.Stat(a.cfg.DB); err == nil {
				ids = a.identities()
			} else if e := config.GitGlobal("user.email"); e != "" {
				ids = append(ids, strings.ToLower(e))
			}
			if a.jsonOut {
				return writeJSON(os.Stdout, map[string]any{
					"config_file": a.cfg.FilePath, "config": a.cfg,
					"identities": ids, "includeif_groups": inc,
				})
			}
			src := "(none, using defaults)"
			if a.cfg.FilePath != "" {
				src = config.Shorten(a.cfg.FilePath)
			}
			fmt.Printf("# config file: %s\n# database:    %s\n", src, config.Shorten(a.cfg.DB))
			fmt.Println("# identities counted as 'mine' (config + git user.email + per-repo user.email):")
			for _, e := range ids {
				fmt.Println("#   " + e)
			}
			if len(inc) > 0 {
				fmt.Println("# groups derived from git includeIf (applied after config groups):")
				for _, g := range inc {
					var ms []string
					for _, m := range g.Match {
						ms = append(ms, config.Shorten(m.Path)+m.Remote)
					}
					fmt.Printf("#   %-14s %s  identity=%s  (%s)\n", g.Name, strings.Join(ms, ", "), orDash(g.Identity), g.Source)
				}
			}
			fmt.Println()
			fmt.Print(a.cfg.YAML())
			return nil
		},
	})
	var force bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter config file (only overrides; defaults stay built in)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := a.configPath
			if p == "" {
				p = config.DefaultPath()
			}
			if _, err := os.Stat(p); err == nil && !force {
				return fmt.Errorf("%s exists (use --force to overwrite)", config.Shorten(p))
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(starterConfig()), 0o644); err != nil {
				return err
			}
			fmt.Println("wrote", config.Shorten(p))
			return nil
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	c.AddCommand(initCmd, &cobra.Command{
		Use:   "path",
		Short: "Print the config file and database paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			p := a.configPath
			if p == "" {
				p = config.DefaultPath()
			}
			fmt.Println(p)
			fmt.Println(a.cfg.DB)
			return nil
		},
	})
	return c
}

func starterConfig() string {
	var b strings.Builder
	b.WriteString(`# repoidx configuration. Everything is optional: lists below EXTEND the
# built-in defaults (see 'repoidx config show'). Uncomment what you need.

# Folders to scan when 'repoidx scan' gets no arguments (default: ~)
# roots: [~/src, ~/work]

# Days without local commits, remote commits, fetches or checkouts before a
# repo counts as stale.
# stale_days: 60

# Extra emails that count as "you" (the global user.email and every repo's
# effective user.email are already included).
# identities:
#   - 12345+me@users.noreply.github.com

# SSH host aliases that 'ssh -G' cannot resolve, and providers for
# self-hosted servers.
# hosts:
#   github-work: github.com
# providers:
#   git.customer.example: gitlab

# Extra directory names (globs) to skip anywhere, and paths to skip.
# skip_dirs: [build, dist]
# skip_paths: [~/Downloads]
# no_default_skips: false

# Repos below these paths are indexed but hidden unless --all / --tooling.
# tooling_paths: [~/.config/emacs]

# Groups: first matching rule wins; rules from your git includeIf sections
# are applied after these. Matchers are globs (** allowed).
# groups:
#   - name: customerA
#     identity: me@customera.example      # flags repos with another user.email
#     match:
#       - remote: "gitlab.customera.example/**"
#       - remote: "github.com/customera-*/**"
#       - path: "~/work/customerA/**"
#       - ssh_alias: github-custa
#   - name: personal
#     match:
#       - remote: "github.com/YOUR_USER/*"
`)
	if inc := config.IncludeIfGroups(); len(inc) > 0 {
		b.WriteString("\n# Already derived from your git includeIf (no need to repeat):\n")
		for _, g := range inc {
			fmt.Fprintf(&b, "#   %s (%s)\n", g.Name, g.Source)
		}
	}
	return b.String()
}
