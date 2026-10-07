// Package gitinfo collects metadata about a single repository by running the
// git binary. It is strictly read-only: optional locks are disabled so
// `git status` never rewrites the index, fsmonitor daemons are not started,
// and nothing is fetched unless Fetch is called explicitly.
package gitinfo

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Author is a commit count for one email.
type Author struct {
	Name  string
	Email string
	Count int
}

// Info is everything repoidx records about a repository.
type Info struct {
	Path       string
	GitDir     string // absolute git dir (per worktree)
	CommonDir  string // absolute common git dir (shared by worktrees)
	Bare       bool
	Worktree   bool // linked worktree (git dir != common dir)
	Submodule  bool
	HasCommits bool

	HeadSHA       string
	Branch        string // "" when detached or unborn
	Upstream      string // e.g. origin/main
	DefaultBranch string // from refs/remotes/origin/HEAD, e.g. origin/main
	Ahead, Behind int

	Dirty           int // changed tracked entries (incl. conflicts)
	Untracked       int
	Stashes         int
	UnpushedCommits int // commits on local branches not on any remote-tracking ref

	LastLocalCommit  time.Time // newest commit on refs/heads
	LastRemoteCommit time.Time // newest commit on refs/remotes
	LastFetch        time.Time // FETCH_HEAD mtime
	LastActivity     time.Time // max(index, HEAD) mtime: checkouts, adds, commits

	GitSizeKB int64
	FileCount int // tracked files

	Authors       []Author // per email, on HEAD
	RemoteAuthors []Author // per email, on upstream / default branch
	RemoteRef     string   // ref RemoteAuthors was computed from

	RefsHash  string // changes whenever any ref moves; used to skip work on update
	Remotes   []RemoteURL
	UserEmail string // effective user.email in this repo (includeIf applied)

	ReadmeName string
	Readme     string

	// Light is true when only cheap fields were refreshed (refs unchanged).
	Light bool
	// StatusTime is how long git status took. Very slow status on an
	// unchanged tree usually means stale stat data in the index (repo was
	// copied or restored), which repoidx cannot fix since it never writes
	// the index; a single plain 'git status' does.
	StatusTime time.Duration
	StaleIndex int    // index entries with stale stat data (only checked when status is slow)
	Err        string // non-fatal problems, joined
}

// RemoteURL is a configured remote.
type RemoteURL struct {
	Name, URL string
}

// Git runs git commands with a timeout and a safe environment.
type Git struct {
	Timeout time.Duration
	env     []string
}

// New returns a Git runner.
func New(timeout time.Duration) *Git {
	env := make([]string, 0, len(os.Environ())+5)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
			"GIT_OBJECT_DIRECTORY", "GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES",
			"LC_ALL", "LANG":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_LAZY_FETCH=1", // partial clones must not hit the network
		"GIT_PAGER=cat",
		"LC_ALL=C",
	)
	return &Git{Timeout: timeout, env: env}
}

// Available reports whether the git binary can be found.
func Available() error {
	_, err := exec.LookPath("git")
	return err
}

func (g *Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	return g.runT(ctx, g.Timeout, dir, args...)
}

func (g *Git) runT(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{
		"-c", "core.fsmonitor=false",
		"-c", "color.ui=false",
		"-c", "core.quotepath=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = g.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s: timed out after %s", args[0], timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return string(out), &Error{Args: args, Msg: msg, Err: err}
	}
	return string(out), nil
}

// Error is a failed git invocation.
type Error struct {
	Args []string
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("git %s: %s", e.Args[0], e.Msg)
	}
	return fmt.Sprintf("git %s: %v", e.Args[0], e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// SlowStatus is the git status duration above which StaleIndex is checked.
const SlowStatus = 10 * time.Second

// Prev is what is already known about a repo, used to skip expensive work.
type Prev struct {
	RefsHash string
}

// Collect gathers metadata for the repo at path. A non-nil error means the
// path is not a usable repo at all; partial problems are reported in Info.Err.
func (g *Git) Collect(ctx context.Context, path string, prev *Prev) (*Info, error) {
	in := &Info{Path: path}
	var errs []string
	note := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	// --- identity of the repo
	out, err := g.run(ctx, path, "rev-parse", "--is-bare-repository", "--absolute-git-dir", "--git-common-dir", "--show-superproject-working-tree")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		return nil, fmt.Errorf("unexpected rev-parse output: %q", out)
	}
	in.Bare = lines[0] == "true"
	in.GitDir = filepath.Clean(lines[1])
	in.CommonDir = lines[2]
	if !filepath.IsAbs(in.CommonDir) {
		in.CommonDir = filepath.Join(path, in.CommonDir)
	}
	in.GitDir, in.CommonDir = realpath(in.GitDir), realpath(in.CommonDir)
	in.Worktree = !in.Bare && in.GitDir != in.CommonDir && !strings.Contains(filepath.ToSlash(in.GitDir), "/modules/")
	in.Submodule = len(lines) > 3 && strings.TrimSpace(lines[3]) != ""

	if sha, err := g.run(ctx, path, "rev-parse", "-q", "--verify", "HEAD^{commit}"); err == nil {
		in.HasCommits = true
		in.HeadSHA = strings.TrimSpace(sha)
	}
	if b, err := g.run(ctx, path, "symbolic-ref", "-q", "--short", "HEAD"); err == nil {
		in.Branch = strings.TrimSpace(b)
	}

	// --- config: remotes and identity
	if out, err := g.run(ctx, path, "remote"); err == nil {
		for _, name := range strings.Fields(out) {
			u, err := g.run(ctx, path, "remote", "get-url", name)
			if err != nil {
				note(err)
				continue
			}
			in.Remotes = append(in.Remotes, RemoteURL{Name: name, URL: strings.TrimSpace(u)})
		}
	}
	if e, err := g.run(ctx, path, "config", "--get", "user.email"); err == nil {
		in.UserEmail = strings.ToLower(strings.TrimSpace(e))
	}

	// --- working tree status (non-bare)
	if !in.Bare {
		t0 := time.Now()
		out, err := g.run(ctx, path, "status", "--porcelain=v2", "--branch", "-z", "--ignore-submodules=dirty")
		in.StatusTime = time.Since(t0)
		note(err)
		if err == nil {
			parseStatus(out, in)
		}
		// diff-files compares stat data only (no hashing), so it is instant
		// and lists every entry git would have to re-read.
		if in.StatusTime > SlowStatus {
			if out, err := g.run(ctx, path, "diff-files", "--name-only", "-z"); err == nil {
				in.StaleIndex = max(strings.Count(out, "\x00")-in.Dirty, 0)
			}
		}
	}

	// --- refs: dates + change detection
	refs, err := g.run(ctx, path, "for-each-ref", "--format=%(objectname) %(committerdate:unix) %(refname)", "refs/heads", "refs/remotes", "refs/stash")
	note(err)
	h := sha1.Sum([]byte(in.HeadSHA + "\n" + refs))
	in.RefsHash = hex.EncodeToString(h[:])
	parseRefs(refs, in)

	if d, err := g.run(ctx, path, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		in.DefaultBranch = strings.TrimSpace(d)
	}

	in.LastFetch = mtime(filepath.Join(in.CommonDir, "FETCH_HEAD"))
	in.LastActivity = maxTime(mtime(filepath.Join(in.GitDir, "index")), mtime(filepath.Join(in.GitDir, "HEAD")), mtime(filepath.Join(in.GitDir, "logs", "HEAD")))

	if out, err := g.run(ctx, path, "stash", "list"); err == nil {
		in.Stashes = countLines(out)
	}

	// --- expensive part: skipped when no ref moved since last scan
	if prev != nil && prev.RefsHash == in.RefsHash {
		in.Light = true
		in.Err = strings.Join(errs, "; ")
		return in, nil
	}

	if in.HasCommits {
		// commits on any local branch that no remote-tracking ref contains;
		// depends only on refs, so light refreshes keep the stored value
		if out, err := g.run(ctx, path, "rev-list", "--count", "--branches", "--not", "--remotes"); err == nil {
			in.UnpushedCommits, _ = strconv.Atoi(strings.TrimSpace(out))
		} else {
			note(err)
		}
	}

	if out, err := g.run(ctx, path, "count-objects", "-v"); err == nil {
		in.GitSizeKB = parseCountObjects(out)
	}
	if !in.Bare {
		if out, err := g.run(ctx, path, "ls-files", "-z"); err == nil {
			in.FileCount = strings.Count(out, "\x00")
		} else {
			note(err)
		}
		in.ReadmeName, in.Readme = readReadme(path)
	}

	if in.HasCommits {
		long := 4 * g.Timeout // shortlog walks all history; allow big repos more time
		out, err := g.runT(ctx, long, path, "shortlog", "-sne", "HEAD", "--")
		note(err)
		in.Authors = parseShortlog(out)

		ref := in.Upstream
		if ref == "" {
			ref = in.DefaultBranch
		}
		if ref != "" {
			if out, err := g.runT(ctx, long, path, "shortlog", "-sne", ref, "--"); err == nil {
				in.RemoteAuthors = parseShortlog(out)
				in.RemoteRef = ref
			}
		}
	}

	in.Err = strings.Join(errs, "; ")
	return in, nil
}

// Fetch updates remote-tracking refs. This is the only mutating operation and
// only runs when explicitly requested. SSH runs in batch mode so it fails
// instead of prompting.
func (g *Git) Fetch(ctx context.Context, path string, timeout time.Duration) error {
	gg := *g
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		gg.env = append(append([]string(nil), g.env...), "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10")
	}
	_, err := gg.runT(ctx, timeout, path, "fetch", "--all", "--prune", "--quiet")
	return err
}

func parseStatus(out string, in *Info) {
	toks := strings.Split(out, "\x00")
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case strings.HasPrefix(t, "# branch.upstream "):
			in.Upstream = strings.TrimPrefix(t, "# branch.upstream ")
		case strings.HasPrefix(t, "# branch.ab "):
			var a, b int
			fmt.Sscanf(strings.TrimPrefix(t, "# branch.ab "), "+%d -%d", &a, &b)
			in.Ahead, in.Behind = a, b
		case strings.HasPrefix(t, "1 "), strings.HasPrefix(t, "u "):
			in.Dirty++
		case strings.HasPrefix(t, "2 "):
			in.Dirty++
			i++ // rename/copy entries carry the original path as next token
		case strings.HasPrefix(t, "? "):
			in.Untracked++
		}
	}
}

func parseRefs(out string, in *Info) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, " ", 3)
		if len(f) != 3 {
			continue
		}
		sec, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil || sec == 0 {
			continue // annotated tags without committerdate, etc.
		}
		t := time.Unix(sec, 0)
		switch {
		case strings.HasPrefix(f[2], "refs/heads/"):
			in.LastLocalCommit = maxTime(in.LastLocalCommit, t)
		case strings.HasPrefix(f[2], "refs/remotes/"):
			in.LastRemoteCommit = maxTime(in.LastRemoteCommit, t)
		}
	}
}

func parseShortlog(out string) []Author {
	var as []Author
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		n, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		c, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			continue
		}
		name, email := rest, ""
		if i := strings.LastIndex(rest, " <"); i >= 0 && strings.HasSuffix(rest, ">") {
			name, email = rest[:i], rest[i+2:len(rest)-1]
		}
		as = append(as, Author{Name: name, Email: strings.ToLower(email), Count: c})
	}
	return as
}

func parseCountObjects(out string) int64 {
	var total int64
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		if k == "size" || k == "size-pack" || k == "size-garbage" {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			total += n
		}
	}
	return total
}

const maxReadme = 256 << 10

func readReadme(dir string) (string, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	best, rank := "", 99
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		n := strings.ToLower(e.Name())
		if !strings.HasPrefix(n, "readme") {
			continue
		}
		r := 5
		switch n {
		case "readme.md":
			r = 0
		case "readme.markdown", "readme.rst", "readme.adoc", "readme.org":
			r = 1
		case "readme", "readme.txt":
			r = 2
		}
		if r < rank {
			best, rank = e.Name(), r
		}
	}
	if best == "" {
		return "", ""
	}
	f, err := os.Open(filepath.Join(dir, best))
	if err != nil {
		return "", ""
	}
	defer f.Close()
	buf, _ := io.ReadAll(io.LimitReader(f, maxReadme))
	return best, strings.ToValidUTF8(string(buf), "")
}

// realpath resolves symlinks (e.g. macOS /var -> /private/var) so paths
// reported by git and paths we build can be compared.
func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func mtime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func maxTime(ts ...time.Time) time.Time {
	var m time.Time
	for _, t := range ts {
		if t.After(m) {
			m = t
		}
	}
	return m
}

// IsNotRepo reports whether err means the path is not a git repository.
func IsNotRepo(err error) bool {
	var ge *Error
	return errors.As(err, &ge) && strings.Contains(ge.Msg, "not a git repository")
}
