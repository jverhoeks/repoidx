// Package discover finds git repositories below one or more root folders.
//
// It walks directories in parallel, never follows symlinks, and stops
// descending once it finds a repository (unless Nested is set). Unreadable
// directories are skipped and reported through Options.OnError.
package discover

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/bmatcuk/doublestar/v4"
)

// Options control the walk.
type Options struct {
	SkipDirs  []string // directory base-name globs to skip anywhere
	SkipPaths []string // absolute paths (already ~-expanded) to skip
	Nested    bool     // keep descending into repos to find nested ones
	Workers   int      // concurrent ReadDir calls

	// OnError is called (concurrently) for directories that could not be
	// read, e.g. permission denied or macOS privacy-protected folders.
	OnError func(dir string, err error)
}

// Found is one discovered repository.
type Found struct {
	Path string // working tree, or the repo dir itself for bare repos
	Bare bool
}

// Walk discovers repos under roots and sends them to the returned channel,
// which is closed when the walk is complete.
func Walk(roots []string, opt Options) <-chan Found {
	if opt.Workers <= 0 {
		opt.Workers = 32
	}
	w := &walker{
		opt:   opt,
		out:   make(chan Found, 64),
		sem:   make(chan struct{}, opt.Workers),
		skipP: map[string]bool{},
		seen:  map[string]bool{},
	}
	for _, p := range opt.SkipPaths {
		w.skipP[filepath.Clean(p)] = true
	}
	for _, r := range roots {
		r = filepath.Clean(r)
		if w.markSeen(r) {
			w.wg.Add(1)
			go w.walk(r)
		}
	}
	go func() {
		w.wg.Wait()
		close(w.out)
	}()
	return w.out
}

type walker struct {
	opt   Options
	out   chan Found
	sem   chan struct{}
	wg    sync.WaitGroup
	skipP map[string]bool

	mu   sync.Mutex
	seen map[string]bool // guards against overlapping roots
}

func (w *walker) markSeen(p string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[p] {
		return false
	}
	w.seen[p] = true
	return true
}

func (w *walker) walk(dir string) {
	defer w.wg.Done()
	w.sem <- struct{}{}
	entries, err := os.ReadDir(dir)
	<-w.sem
	if err != nil {
		if w.opt.OnError != nil && !errors.Is(err, fs.ErrNotExist) {
			w.opt.OnError(dir, err)
		}
		return
	}

	isRepo, bare := classify(entries)
	if isRepo {
		w.out <- Found{Path: dir, Bare: bare}
		if !w.opt.Nested || bare {
			return
		}
	}
	for _, e := range entries {
		// DirEntry.Type() comes from lstat: symlinks report ModeSymlink and
		// are skipped, so links are never followed.
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == ".git" || w.skipName(name) {
			continue
		}
		child := filepath.Join(dir, name)
		if w.skipP[child] || !w.markSeen(child) {
			continue
		}
		w.wg.Add(1)
		go w.walk(child)
	}
}

func (w *walker) skipName(name string) bool {
	for _, g := range w.opt.SkipDirs {
		if g == name {
			return true
		}
		if ok, _ := doublestar.Match(g, name); ok {
			return true
		}
	}
	return false
}

// classify reports whether a directory is a repo: it contains a .git entry
// (a directory, or a file for worktrees/submodules), or it is a bare repo
// (HEAD file plus objects/ and refs/ directories).
func classify(entries []os.DirEntry) (isRepo, bare bool) {
	var head, objects, refs bool
	for _, e := range entries {
		switch e.Name() {
		case ".git":
			if e.IsDir() || e.Type().IsRegular() {
				return true, false
			}
		case "HEAD":
			head = e.Type().IsRegular()
		case "objects":
			objects = e.IsDir()
		case "refs":
			refs = e.IsDir()
		}
	}
	return head && objects && refs, head && objects && refs
}
