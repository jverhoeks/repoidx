package gitinfo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Me", "GIT_AUTHOR_EMAIL=me@example.com",
		"GIT_COMMITTER_NAME=Me", "GIT_COMMITTER_EMAIL=me@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, path string, prev *Prev) *Info {
	t.Helper()
	in, err := New(30*time.Second).Collect(context.Background(), path, prev)
	if err != nil {
		t.Fatalf("Collect(%s): %v", path, err)
	}
	return in
}

func TestEmptyRepo(t *testing.T) {
	d := t.TempDir()
	git(t, d, "init", "-q", "-b", "main")
	in := collect(t, d, nil)
	if in.HasCommits || in.Branch != "main" || in.UnpushedCommits != 0 || len(in.Authors) != 0 {
		t.Errorf("empty repo: %+v", in)
	}
}

func TestDirtyNoRemote(t *testing.T) {
	d := t.TempDir()
	git(t, d, "init", "-q", "-b", "main")
	write(t, filepath.Join(d, "README.md"), "# Hello\nterraform eks module\n")
	write(t, filepath.Join(d, "a.txt"), "a")
	git(t, d, "add", ".")
	git(t, d, "commit", "-q", "-m", "one")
	write(t, filepath.Join(d, "a.txt"), "changed")
	write(t, filepath.Join(d, "new.txt"), "x")
	git(t, d, "config", "user.email", "Me@Example.com")

	in := collect(t, d, nil)
	if !in.HasCommits || in.Dirty != 1 || in.Untracked != 1 || in.FileCount != 2 {
		t.Errorf("status wrong: dirty=%d untracked=%d files=%d", in.Dirty, in.Untracked, in.FileCount)
	}
	if in.UnpushedCommits != 1 {
		t.Errorf("no remote: all commits are unpushed, got %d", in.UnpushedCommits)
	}
	if in.ReadmeName != "README.md" || in.Readme == "" {
		t.Errorf("readme not read: %q", in.ReadmeName)
	}
	if len(in.Authors) != 1 || in.Authors[0].Email != "me@example.com" || in.Authors[0].Count != 1 {
		t.Errorf("authors: %+v", in.Authors)
	}
	if in.UserEmail != "me@example.com" {
		t.Errorf("user email: %q", in.UserEmail)
	}
	if in.LastLocalCommit.IsZero() || in.GitSizeKB == 0 {
		t.Errorf("dates/size missing: %+v", in)
	}

	// unchanged refs => light refresh
	again := collect(t, d, &Prev{RefsHash: in.RefsHash})
	if !again.Light || again.Dirty != 1 {
		t.Errorf("expected light refresh with status, got light=%v dirty=%d", again.Light, again.Dirty)
	}
}

func TestCloneAheadBehindAndWorktree(t *testing.T) {
	base := t.TempDir()
	up := filepath.Join(base, "up")
	os.Mkdir(up, 0o755)
	git(t, up, "init", "-q", "-b", "main")
	write(t, filepath.Join(up, "f"), "1")
	git(t, up, "add", ".")
	git(t, up, "commit", "-q", "-m", "1")

	cl := filepath.Join(base, "clone")
	git(t, base, "clone", "-q", up, cl)
	// upstream moves on, clone fetches => behind 1
	write(t, filepath.Join(up, "f"), "2")
	git(t, up, "commit", "-qam", "2")
	git(t, cl, "fetch", "-q")
	// local commit => ahead 1, and a stash
	write(t, filepath.Join(cl, "g"), "x")
	git(t, cl, "add", ".")
	git(t, cl, "commit", "-qm", "local")
	write(t, filepath.Join(cl, "g"), "y")
	git(t, cl, "stash", "-q")

	in := collect(t, cl, nil)
	if in.Upstream != "origin/main" || in.Ahead != 1 || in.Behind != 1 {
		t.Errorf("ahead/behind: upstream=%q +%d -%d", in.Upstream, in.Ahead, in.Behind)
	}
	if in.Stashes != 1 || in.UnpushedCommits != 1 || in.Dirty != 0 {
		t.Errorf("stash=%d unpushed=%d dirty=%d", in.Stashes, in.UnpushedCommits, in.Dirty)
	}
	if len(in.Remotes) != 1 || in.Remotes[0].Name != "origin" {
		t.Errorf("remotes: %+v", in.Remotes)
	}
	if in.RemoteRef != "origin/main" || len(in.RemoteAuthors) != 1 || in.RemoteAuthors[0].Count != 2 {
		t.Errorf("remote authors: ref=%q %+v", in.RemoteRef, in.RemoteAuthors)
	}
	if in.LastFetch.IsZero() || in.LastRemoteCommit.IsZero() {
		t.Errorf("fetch/remote dates missing")
	}

	wt := filepath.Join(base, "wt")
	git(t, cl, "worktree", "add", "-q", wt, "-b", "feature")
	w := collect(t, wt, nil)
	if !w.Worktree || w.CommonDir != in.CommonDir || w.Branch != "feature" {
		t.Errorf("worktree: wt=%v common=%q vs %q branch=%q", w.Worktree, w.CommonDir, in.CommonDir, w.Branch)
	}
	if in.Worktree {
		t.Errorf("main checkout must not be a worktree")
	}
}

func TestNotARepo(t *testing.T) {
	_, err := New(10*time.Second).Collect(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error for non-repo")
	}
}
