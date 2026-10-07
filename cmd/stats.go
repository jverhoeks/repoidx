package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/spf13/cobra"
)

func statsCmd(a *app) *cobra.Command {
	var ff filterFlags
	var by string
	var summ bool
	var top int
	c := &cobra.Command{
		Use:   "stats",
		Short: "Totals per group, provider, host, namespace or root; --summary for an overview",
		Args:  cobra.NoArgs,
		Example: `  repoidx stats                     # per group
  repoidx stats --by provider
  repoidx stats -s                  # overview: health, activity, top groups, largest, dupes
  repoidx stats -s -g 'customer*' --top 5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			f := ff.build(a, a.identities())
			if summ {
				s, err := buildSummary(a, f, top)
				if err != nil {
					return err
				}
				if a.jsonOut {
					return writeJSON(os.Stdout, s)
				}
				printSummary(os.Stdout, s, top)
				return nil
			}
			rows, err := a.store.Stats(by, f)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return writeJSON(os.Stdout, nonNil(rows))
			}
			if by == "root" {
				for i := range rows {
					rows[i].Key = config.Shorten(rows[i].Key)
				}
			}
			printStatRows(os.Stdout, strings.ToUpper(by), rows, 0)
			return nil
		},
	}
	addFilterFlags(c, &ff)
	c.Flags().StringVar(&by, "by", "group", "group, provider, host, namespace, root")
	c.Flags().BoolVarP(&summ, "summary", "s", false, "overview tables: health, activity, providers, top groups, largest repos, dupes")
	c.Flags().IntVar(&top, "top", 10, "rows per table in --summary")
	return c
}

func dupesCmd(a *app) *cobra.Command {
	var ff filterFlags
	c := &cobra.Command{
		Use:   "dupes",
		Short: "Remotes that are cloned in more than one place",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			ds, err := a.store.Dupes(ff.build(a, nil))
			if err != nil {
				return err
			}
			if a.jsonOut {
				return writeJSON(os.Stdout, nonNil(ds))
			}
			for _, d := range ds {
				fmt.Println(d.Slug)
				for _, p := range d.Paths {
					fmt.Println("  " + config.Shorten(p))
				}
			}
			if len(ds) == 0 {
				fmt.Fprintln(os.Stderr, "no duplicate clones")
			}
			return nil
		},
	}
	addFilterFlags(c, &ff)
	return c
}
