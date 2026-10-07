package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func mk(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWalk(t *testing.T) {
	root := t.TempDir()
	mk(t, root,
		"a/.git",
		"a/sub/.git",                        // nested: not found by default
		"b/node_modules/x/.git",             // skipped dir
		"c/.terraform/modules/m/.git",       // skipped dir
		"d/e/f/.git",                        // deep
		"skipme/g/.git",                     // skip path
		"bare.git/objects", "bare.git/refs", // bare repo
	)
	os.WriteFile(filepath.Join(root, "bare.git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	// worktree: .git is a file
	mk(t, root, "wt")
	os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: /x\n"), 0o644)
	// symlink to a repo must not be followed
	os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link"))

	got := collect(Walk([]string{root}, Options{
		SkipDirs:  []string{"node_modules", ".terraform"},
		SkipPaths: []string{filepath.Join(root, "skipme")},
	}))
	want := []string{"a", "bare.git", "d/e/f", "wt"}
	if !equal(rel(root, got), want) {
		t.Errorf("got %v, want %v", rel(root, got), want)
	}

	got = collect(Walk([]string{root, root + "/a"}, Options{Nested: true, SkipDirs: []string{"node_modules", ".terraform"}}))
	want = []string{"a", "a/sub", "bare.git", "d/e/f", "skipme/g", "wt"}
	if !equal(rel(root, got), want) {
		t.Errorf("nested: got %v, want %v", rel(root, got), want)
	}
}

func collect(ch <-chan Found) []string {
	var out []string
	for f := range ch {
		out = append(out, f.Path)
	}
	return out
}

func rel(root string, ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		r, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(r))
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUnreadableReported(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("chmod-based permission test")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	mk(t, root, "locked/repo/.git")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	var bad []string
	for range Walk([]string{root}, Options{OnError: func(d string, _ error) { bad = append(bad, d) }}) {
	}
	if len(bad) != 1 || bad[0] != locked {
		t.Errorf("unreadable = %v, want [%s]", bad, locked)
	}
}
