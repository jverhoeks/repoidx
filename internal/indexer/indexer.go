// Package indexer ties discovery, git metadata collection, grouping and the
// store together.
package indexer

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/discover"
	"github.com/jverhoeks/repoidx/internal/gitinfo"
	"github.com/jverhoeks/repoidx/internal/group"
	"github.com/jverhoeks/repoidx/internal/remote"
	"github.com/jverhoeks/repoidx/internal/store"
)

// Indexer runs scans and updates.
type Indexer struct {
	Cfg      *config.Config
	Store    *store.Store
	Git      *gitinfo.Git
	Resolver *remote.Resolver

	// Progress is called after each repo (may be nil). found is the number
	// of repos discovered so far; it keeps growing during a scan.
	Progress func(done, found int64, path string)
	// OnError is called for repos that could not be indexed (may be nil).
	OnError func(path string, err error)
	// OnUnreadable is called for directories the walk could not read (may be nil).
	OnUnreadable func(dir string, err error)
}

// New builds an Indexer from config.
func New(cfg *config.Config, st *store.Store) *Indexer {
	return &Indexer{
		Cfg:   cfg,
		Store: st,
		Git:   gitinfo.New(time.Duration(cfg.GitTimeout) * time.Second),
		Resolver: &remote.Resolver{
			Hosts:     cfg.Hosts,
			Providers: cfg.Providers,
			UseSSH:    true,
		},
	}
}

// Result summarises a run.
type Result struct {
	Found, Indexed, Light, Failed, Removed int
	Unreadable                             int // directories the walk could not read
	Fetched, FetchFailed                   int
	Elapsed                                time.Duration
	StaleIndex                             []string // repos whose index has stale stat data (slow status)
}

// ScanOptions control Scan.
type ScanOptions struct {
	Nested bool // also index repos nested inside other repos
	Force  bool // recollect everything even when refs did not move
}

// Scan discovers repos below roots, indexes them, and removes index entries
// below those roots that no longer exist.
func (x *Indexer) Scan(ctx context.Context, roots []string, opt ScanOptions) (*Result, error) {
	start := time.Now()
	for i, r := range roots {
		roots[i] = config.Expand(r)
	}
	prev, err := x.Store.Prev()
	if err != nil {
		return nil, err
	}
	skipPaths := make([]string, 0, len(x.Cfg.SkipPaths))
	for _, p := range x.Cfg.SkipPaths {
		skipPaths = append(skipPaths, config.Expand(p))
	}
	var unreadable atomic.Int64
	var umu sync.Mutex
	found := discover.Walk(roots, discover.Options{
		SkipDirs:  x.Cfg.SkipDirs,
		SkipPaths: skipPaths,
		Nested:    opt.Nested,
		OnError: func(dir string, err error) {
			unreadable.Add(1)
			if x.OnUnreadable != nil {
				umu.Lock()
				x.OnUnreadable(dir, err)
				umu.Unlock()
			}
		},
	})

	res := &Result{}
	var nFound atomic.Int64
	paths := make(chan string, 64)
	go func() {
		defer close(paths)
		for f := range found {
			nFound.Add(1)
			select {
			case paths <- f.Path:
			case <-ctx.Done():
			}
		}
	}()
	seen := x.collectAll(ctx, paths, prev, opt.Force, roots, &nFound, res)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	res.Found = int(nFound.Load())
	res.Unreadable = int(unreadable.Load())

	// prune: indexed repos below a scanned root that the walk did not find.
	// A repo that still exists is only dropped when it is now excluded by
	// the skip settings; one hidden behind an unreadable directory (e.g.
	// macOS privacy-protected folders) or a failed git call is kept.
	var gone []string
	for p := range prev {
		if !seen[p] && underAny(p, roots) && (repoGone(p) || x.excluded(p, roots, skipPaths)) {
			gone = append(gone, p)
		}
	}
	if err := x.Store.Delete(gone...); err != nil {
		return res, err
	}
	res.Removed = len(gone)
	for _, r := range roots {
		_ = x.Store.TouchRoot(r)
	}
	if err := x.Regroup(); err != nil {
		return res, err
	}
	res.Elapsed = time.Since(start)
	return res, nil
}

// UpdateOptions control Update.
type UpdateOptions struct {
	Fetch        bool
	FetchTimeout time.Duration
	Force        bool
	Paths        []string // restrict to these indexed paths (default: all)
}

// Update refreshes already-indexed repos without walking the disk.
// Repos that disappeared are removed from the index.
func (x *Indexer) Update(ctx context.Context, opt UpdateOptions) (*Result, error) {
	start := time.Now()
	prev, err := x.Store.Prev()
	if err != nil {
		return nil, err
	}
	targets := opt.Paths
	if len(targets) == 0 {
		targets, err = x.Store.Paths()
		if err != nil {
			return nil, err
		}
	}
	res := &Result{}
	var live []string
	var gone []string
	for _, p := range targets {
		if repoGone(p) {
			gone = append(gone, p)
		} else {
			live = append(live, p)
		}
	}
	if err := x.Store.Delete(gone...); err != nil {
		return nil, err
	}
	res.Removed = len(gone)

	if opt.Fetch {
		x.fetchAll(ctx, live, opt.FetchTimeout, res)
	}

	var n atomic.Int64
	n.Store(int64(len(live)))
	paths := make(chan string, len(live))
	for _, p := range live {
		paths <- p
	}
	close(paths)
	x.collectAll(ctx, paths, prev, opt.Force, nil, &n, res)
	res.Found = len(live)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if err := x.Regroup(); err != nil {
		return res, err
	}
	res.Elapsed = time.Since(start)
	return res, nil
}

func (x *Indexer) fetchAll(ctx context.Context, paths []string, timeout time.Duration, res *Result) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	sem := make(chan struct{}, min(x.Cfg.Concurrency, 8)) // be gentle with remotes
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			err := x.Git.Fetch(ctx, p, timeout)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.FetchFailed++
				if x.OnError != nil {
					x.OnError(p, err)
				}
				return
			}
			res.Fetched++
		}(p)
	}
	wg.Wait()
}

// collectAll runs git collection with bounded concurrency and saves results
// from a single goroutine. Returns the set of paths successfully indexed.
func (x *Indexer) collectAll(ctx context.Context, paths <-chan string, prev map[string]string, force bool, roots []string, found *atomic.Int64, res *Result) map[string]bool {
	type item struct {
		info *gitinfo.Info
		path string
		err  error
	}
	results := make(chan item, 64)
	var wg sync.WaitGroup
	workers := max(x.Cfg.Concurrency, 1)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range paths {
				if ctx.Err() != nil {
					continue // drain
				}
				var pv *gitinfo.Prev
				if h, ok := prev[p]; ok && !force {
					pv = &gitinfo.Prev{RefsHash: h}
				}
				in, err := x.Git.Collect(ctx, p, pv)
				results <- item{in, p, err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	seen := map[string]bool{}
	var done int64
	for it := range results {
		done++
		if it.err != nil {
			res.Failed++
			if x.OnError != nil {
				x.OnError(it.path, it.err)
			}
		} else {
			if it.info.StaleIndex > 0 {
				res.StaleIndex = append(res.StaleIndex, it.path)
			}
			meta := store.Meta{
				Root:    rootOf(it.path, roots),
				Tooling: x.isTooling(it.path),
				Remotes: x.parseRemotes(it.info.Remotes),
			}
			if err := x.Store.Save(it.info, meta); err != nil {
				res.Failed++
				if x.OnError != nil {
					x.OnError(it.path, err)
				}
			} else {
				seen[it.path] = true
				res.Indexed++
				if it.info.Light {
					res.Light++
				}
			}
		}
		if x.Progress != nil {
			x.Progress(done, found.Load(), it.path)
		}
	}
	return seen
}

func (x *Indexer) parseRemotes(rs []gitinfo.RemoteURL) []remote.Remote {
	out := make([]remote.Remote, 0, len(rs))
	for _, r := range rs {
		out = append(out, x.Resolver.Resolve(remote.Parse(r.Name, r.URL)))
	}
	return out
}

func (x *Indexer) isTooling(p string) bool {
	sp := filepath.ToSlash(p)
	for _, t := range x.Cfg.ToolingPaths {
		g := filepath.ToSlash(config.Expand(t))
		if sp == g || strings.HasPrefix(sp, g+"/") {
			return true
		}
		if ok, _ := doublestar.Match(g+"/**", sp); ok {
			return true
		}
	}
	return false
}

// Regroup recomputes groups for every indexed repo. Cheap: no git calls.
func (x *Indexer) Regroup() error {
	inputs, overrides, err := x.Store.GroupInputs()
	if err != nil {
		return err
	}
	a := group.New(x.Cfg.Groups, config.IncludeIfGroups())
	out := make([]store.GroupResult, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, a.Assign(in, overrides))
	}
	return x.Store.SetGroups(out)
}

// Identities returns the emails that count as "me": configured identities,
// the global user.email, and every effective user.email seen in indexed repos.
func (x *Indexer) Identities() []string {
	set := map[string]bool{}
	for _, e := range x.Cfg.Identities {
		set[e] = true
	}
	if e := config.GitGlobal("user.email"); e != "" {
		set[strings.ToLower(e)] = true
	}
	if es, err := x.Store.UserEmails(); err == nil {
		for _, e := range es {
			set[e] = true
		}
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// excluded reports whether p lies below a skip path, or below a directory
// (between its root and p) whose name matches skip_dirs.
func (x *Indexer) excluded(p string, roots, skipPaths []string) bool {
	if underAny(p, skipPaths) {
		return true
	}
	rel, err := filepath.Rel(rootOf(p, roots), p)
	if err != nil {
		return false
	}
	segs := strings.Split(filepath.ToSlash(rel), "/")
	for _, seg := range segs[:len(segs)-1] {
		for _, g := range x.Cfg.SkipDirs {
			if ok, _ := doublestar.Match(g, seg); ok {
				return true
			}
		}
	}
	return false
}

// repoGone reports whether p is definitely no longer a repository. Any
// error other than "does not exist" (permission denied, privacy-protected
// folders, unmounted volume I/O errors) keeps the repo in the index.
func repoGone(p string) bool {
	_, err := os.Stat(filepath.Join(p, ".git"))
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	// bare repo
	_, e1 := os.Stat(filepath.Join(p, "HEAD"))
	_, e2 := os.Stat(filepath.Join(p, "objects"))
	return errors.Is(e1, fs.ErrNotExist) || errors.Is(e2, fs.ErrNotExist)
}

func underAny(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, strings.TrimSuffix(r, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func rootOf(p string, roots []string) string {
	best := ""
	for _, r := range roots {
		if underAny(p, []string{r}) && len(r) > len(best) {
			best = r
		}
	}
	return best
}
