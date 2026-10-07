package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/store"
)

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func table(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

// state renders the compact status column. Legend:
//
//	*N changed  ?N untracked  ↑N ahead  ↓N behind  sN stashes
//	!N commits on no remote  local = no remote  stale  err
func state(r *store.Repo) string {
	var p []string
	if r.Dirty > 0 {
		p = append(p, fmt.Sprintf("*%d", r.Dirty))
	}
	if r.Untracked > 0 {
		p = append(p, fmt.Sprintf("?%d", r.Untracked))
	}
	if r.Ahead > 0 {
		p = append(p, fmt.Sprintf("↑%d", r.Ahead))
	}
	if r.Behind > 0 {
		p = append(p, fmt.Sprintf("↓%d", r.Behind))
	}
	if r.Stashes > 0 {
		p = append(p, fmt.Sprintf("s%d", r.Stashes))
	}
	if r.Host == "" {
		p = append(p, "local")
	} else if r.Unpushed > 0 {
		p = append(p, fmt.Sprintf("!%d", r.Unpushed))
	}
	if !r.HasCommits {
		p = append(p, "empty")
	}
	if r.Stale {
		p = append(p, "stale")
	}
	if r.ExpectedEmail != "" && r.UserEmail != r.ExpectedEmail {
		p = append(p, "id!")
	}
	if r.ScanError != "" {
		p = append(p, "err")
	}
	if len(p) == 0 {
		return "ok"
	}
	return strings.Join(p, " ")
}

const stateLegend = `State column:
  *N changed files   ?N untracked   ↑N ahead / ↓N behind upstream   sN stashes
  !N commits (on any local branch) not on any remote   local  no remote configured
  empty  no commits   stale  no activity for stale_days   id!  user.email differs from group identity
  err  some git calls failed (see 'show'); state may be incomplete, never counted as safe`

func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 730*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

func when(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04") + " (" + age(t) + " ago)"
}

func size(kb int64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1fG", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%.1fM", float64(kb)/(1<<10))
	default:
		return fmt.Sprintf("%dK", kb)
	}
}

func branch(r *store.Repo) string {
	switch {
	case r.Bare:
		return "(bare)"
	case r.Branch == "":
		if len(r.HeadSHA) >= 8 {
			return "@" + r.HeadSHA[:8]
		}
		return "-"
	}
	return r.Branch
}

func printRepos(w io.Writer, rows []*store.Repo) {
	tw := table(w)
	fmt.Fprintln(tw, "GROUP\tNAME\tBRANCH\tSTATE\tLAST\tFILES\tMINE\tPATH")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			r.Group, r.Name, branch(r), state(r), age(r.LastAny), r.Files, r.MyCommits, config.Shorten(r.Path))
	}
	tw.Flush()
}

// progress prints a single updating status line on stderr when it is a terminal.
type progress struct {
	tty  bool
	last time.Time
}

func newProgress() *progress {
	st, err := os.Stderr.Stat()
	return &progress{tty: err == nil && st.Mode()&os.ModeCharDevice != 0}
}

func (p *progress) update(done, found int64, path string) {
	if !p.tty || time.Since(p.last) < 80*time.Millisecond {
		return
	}
	p.last = time.Now()
	s := config.Shorten(path)
	if len(s) > 60 {
		s = "…" + s[len(s)-59:]
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%d/%d %s", done, found, s)
}

func (p *progress) done() {
	if p.tty {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
}
