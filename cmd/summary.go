package cmd

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/store"
)

// summary is the data behind 'stats --summary' (also its JSON form).
type summary struct {
	Repos      int   `json:"repos"`
	Files      int   `json:"files"`
	GitSizeKB  int64 `json:"git_size_kb"`
	MyCommits  int   `json:"my_commits"`
	Groups     int   `json:"groups"`
	StaleDays  int   `json:"stale_days"`
	Candidates int   `json:"cleanup_candidates"` // stale and safe
	CandSizeKB int64 `json:"cleanup_git_size_kb"`

	Health    []healthRow     `json:"health"`
	Activity  []bucket        `json:"activity"`
	Provider  []store.StatRow `json:"by_provider"`
	TopGroups []store.StatRow `json:"top_groups"`
	Largest   []*store.Repo   `json:"largest"`
	Mine      []*store.Repo   `json:"most_mine"`
	Dupes     []store.Dupe    `json:"dupes"`
}

type healthRow struct {
	Label string `json:"label"`
	Count int    `json:"count"`
	Cmd   string `json:"command"`
}

type bucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

func buildSummary(a *app, f *store.Filter, top int) (*summary, error) {
	f.Limit = 0
	rows, err := a.store.List(f)
	if err != nil {
		return nil, err
	}
	s := &summary{Repos: len(rows), StaleDays: f.StaleDays}
	var unsafe, dirty, unpushed, stashed, behind, noRemote, stale, wrongID, failed int
	groups := map[string]bool{}
	for _, r := range rows {
		s.Files += r.Files
		if !r.Worktree { // worktrees share their main repo's .git
			s.GitSizeKB += r.GitSizeKB
		}
		s.MyCommits += r.MyCommits
		groups[r.Group] = true
		if r.Unsafe() {
			unsafe++
		} else if r.Stale {
			s.Candidates++
			if !r.Worktree {
				s.CandSizeKB += r.GitSizeKB
			}
		}
		count := func(c bool, n *int) {
			if c {
				*n++
			}
		}
		count(r.Dirty > 0 || r.Untracked > 0, &dirty)
		count(r.Unpushed > 0, &unpushed)
		count(r.Stashes > 0, &stashed)
		count(r.Behind > 0, &behind)
		count(r.Host == "", &noRemote)
		count(r.Stale, &stale)
		count(r.ExpectedEmail != "" && r.UserEmail != r.ExpectedEmail, &wrongID)
		count(r.ScanError != "", &failed)
	}
	s.Groups = len(groups)
	s.Health = []healthRow{
		{"cleanup candidates (stale, safe)", s.Candidates, "list --stale --safe"},
		{"at risk (would lose work)", unsafe, "list --unsafe"},
		{"uncommitted changes", dirty, "list --dirty"},
		{"commits on no remote", unpushed, "list --unpushed"},
		{"stashes", stashed, "list --unsafe"},
		{"no remote", noRemote, "list --no-remote"},
		{"behind upstream", behind, "update --fetch; list --behind"},
		{fmt.Sprintf("stale (> %dd)", f.StaleDays), stale, "list --stale"},
		{"wrong identity", wrongID, "list --wrong-identity"},
		{"collection errors", failed, "show <repo>"},
	}

	now := time.Now()
	edges := []struct {
		label string
		d     time.Duration
	}{
		{"< 1 week", 7}, {"< 1 month", 30}, {fmt.Sprintf("< %d days", f.StaleDays), time.Duration(f.StaleDays)},
		{"< 6 months", 182}, {"< 1 year", 365}, {"< 2 years", 730}, {"older", 0},
	}
	s.Activity = make([]bucket, len(edges))
	for i, e := range edges {
		s.Activity[i].Label = e.label
	}
	for _, r := range rows {
		age := now.Sub(r.LastAny)
		for i, e := range edges {
			if e.d == 0 || age < e.d*24*time.Hour {
				s.Activity[i].Count++
				break
			}
		}
	}

	if s.Provider, err = a.store.Stats("provider", f); err != nil {
		return nil, err
	}
	if s.TopGroups, err = a.store.Stats("group", f); err != nil {
		return nil, err
	}
	if s.Dupes, err = a.store.Dupes(f); err != nil {
		return nil, err
	}

	s.Largest = topN(rows, top, func(r *store.Repo) int64 {
		if r.Worktree {
			return 0
		}
		return r.GitSizeKB
	})
	s.Mine = topN(rows, top, func(r *store.Repo) int64 { return int64(r.MyCommits) })
	return s, nil
}

func topN(rows []*store.Repo, n int, key func(*store.Repo) int64) []*store.Repo {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b *store.Repo) int { return cmp.Compare(key(b), key(a)) })
	out = slices.DeleteFunc(out, func(r *store.Repo) bool { return key(r) == 0 })
	return out[:min(n, len(out))]
}

func printSummary(w io.Writer, s *summary, top int) {
	section := func(title string) { fmt.Fprintf(w, "\n\033[1m%s\033[0m\n", title) }
	if !isTTY(w) {
		section = func(title string) { fmt.Fprintf(w, "\n== %s\n", title) }
	}

	section("Overview")
	tw := table(w)
	fmt.Fprintf(tw, "repos\t%d\tin %d groups\n", s.Repos, s.Groups)
	fmt.Fprintf(tw, "tracked files\t%d\t\n", s.Files)
	fmt.Fprintf(tw, ".git size\t%s\t%s in cleanup candidates\n", size(s.GitSizeKB), size(s.CandSizeKB))
	fmt.Fprintf(tw, "your commits\t%d\t\n", s.MyCommits)
	tw.Flush()

	section("Health")
	tw = table(w)
	fmt.Fprintln(tw, "\tREPOS\tSEE")
	for _, h := range s.Health {
		if h.Count > 0 {
			fmt.Fprintf(tw, "%s\t%d\trepoidx %s\n", h.Label, h.Count, h.Cmd)
		}
	}
	tw.Flush()

	section("Last activity")
	maxC := 0
	for _, b := range s.Activity {
		maxC = max(maxC, b.Count)
	}
	tw = table(w)
	for _, b := range s.Activity {
		fmt.Fprintf(tw, "%s\t%d\t%s\n", b.Label, b.Count, bar(b.Count, maxC, 40))
	}
	tw.Flush()

	section("By provider")
	printStatRows(w, "PROVIDER", s.Provider, 0)

	section(fmt.Sprintf("Top groups (of %d)", s.Groups))
	printStatRows(w, "GROUP", s.TopGroups, top)

	if len(s.Largest) > 0 {
		section("Largest repos (.git)")
		tw = table(w)
		fmt.Fprintln(tw, "NAME\t.GIT\tFILES\tSTATE\tLAST\tPATH")
		for _, r := range s.Largest {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", r.Name, size(r.GitSizeKB), r.Files, state(r), age(r.LastAny), config.Shorten(r.Path))
		}
		tw.Flush()
	}

	if len(s.Mine) > 0 {
		section("Most of your commits")
		tw = table(w)
		fmt.Fprintln(tw, "NAME\tMINE\tTOTAL\tGROUP\tLAST\tPATH")
		for _, r := range s.Mine {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\n", r.Name, r.MyCommits, r.TotalCommits, r.Group, age(r.LastAny), config.Shorten(r.Path))
		}
		tw.Flush()
	}

	if len(s.Dupes) > 0 {
		section(fmt.Sprintf("Cloned more than once (%d)", len(s.Dupes)))
		tw = table(w)
		for i, d := range s.Dupes {
			if i == top {
				fmt.Fprintf(tw, "… %d more\t(repoidx dupes)\n", len(s.Dupes)-top)
				break
			}
			paths := make([]string, len(d.Paths))
			for j, p := range d.Paths {
				paths[j] = config.Shorten(p)
			}
			fmt.Fprintf(tw, "%s\t%s\n", d.Slug, strings.Join(paths, "  "))
		}
		tw.Flush()
	}
}

// printStatRows prints a stats table; top > 0 folds the remaining rows into one.
func printStatRows(w io.Writer, keyHeader string, rows []store.StatRow, top int) {
	tw := table(w)
	fmt.Fprintf(tw, "%s\tREPOS\tFILES\t.GIT\tSTALE\tDIRTY\tUNPUSHED\tBEHIND\tNO-REMOTE\tMINE\n", keyHeader)
	line := func(r store.StatRow) {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n",
			r.Key, r.Repos, r.Files, size(r.GitSizeKB), r.Stale, r.Dirty, r.Unpushed, r.Behind, r.NoRemote, r.MyCommits)
	}
	var rest, total store.StatRow
	n := 0
	for i, r := range rows {
		addStat(&total, r)
		if top > 0 && i >= top {
			addStat(&rest, r)
			n++
			continue
		}
		line(r)
	}
	if n > 0 {
		rest.Key = fmt.Sprintf("… %d more", n)
		line(rest)
	}
	if len(rows) > 1 {
		total.Key = "TOTAL"
		line(total)
	}
	tw.Flush()
}

func addStat(t *store.StatRow, r store.StatRow) {
	t.Repos += r.Repos
	t.Files += r.Files
	t.GitSizeKB += r.GitSizeKB
	t.Stale += r.Stale
	t.Dirty += r.Dirty
	t.Unpushed += r.Unpushed
	t.Behind += r.Behind
	t.NoRemote += r.NoRemote
	t.MyCommits += r.MyCommits
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func bar(n, maxN, width int) string {
	if maxN == 0 {
		return ""
	}
	cells := n * width * 8 / maxN // eighths of a cell
	s := strings.Repeat("█", cells/8)
	if r := cells % 8; r > 0 {
		s += string([]rune(" ▏▎▍▌▋▊▉")[r])
	}
	return s
}
