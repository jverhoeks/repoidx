package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jverhoeks/repoidx/internal/gitinfo"
	"github.com/jverhoeks/repoidx/internal/remote"
)

func TestSaveListSearch(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var fk int
	var jm string
	must(t, s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	must(t, s.db.QueryRow(`PRAGMA journal_mode`).Scan(&jm))
	if fk != 1 || jm != "wal" {
		t.Fatalf("pragmas not applied: foreign_keys=%d journal_mode=%s", fk, jm)
	}

	old := time.Now().Add(-200 * 24 * time.Hour)
	gh := remote.Parse("origin", "git@github.com:acme/infra.git")
	a := &gitinfo.Info{
		Path: "/r/a", CommonDir: "/r/a/.git", HasCommits: true, Branch: "main",
		LastLocalCommit: old, LastActivity: old, FileCount: 10,
		Authors: []gitinfo.Author{{Name: "Me", Email: "me@x.com", Count: 3}, {Name: "Other", Email: "o@x.com", Count: 7}},
		Readme:  "Terraform modules for the EKS platform",
	}
	b := &gitinfo.Info{
		Path: "/r/b", CommonDir: "/r/b/.git", HasCommits: true, Branch: "main", Dirty: 2,
		LastLocalCommit: time.Now(), LastActivity: time.Now(), Readme: "a web app",
	}
	c := &gitinfo.Info{Path: "/r/c", CommonDir: "/r/c/.git", HasCommits: true, LastActivity: time.Now()}
	must(t, s.Save(a, Meta{Root: "/r", Remotes: []remote.Remote{gh}}))
	must(t, s.Save(b, Meta{Root: "/r", Remotes: []remote.Remote{gh}}))
	must(t, s.Save(c, Meta{Root: "/r"}))
	must(t, s.SetGroups([]GroupResult{{ID: 1, Group: "acme"}, {ID: 2, Group: "acme"}, {ID: 3, Group: "local"}}))

	f := &Filter{StaleDays: 60, Identities: []string{"me@x.com"}}
	rows, err := s.List(f)
	must(t, err)
	if len(rows) != 3 || rows[0].MyCommits != 3 || rows[0].TotalCommits != 10 || !rows[0].Stale || rows[1].Stale {
		t.Fatalf("list: %+v", rows[0])
	}

	for name, want := range map[string]struct {
		f Filter
		n int
	}{
		"stale":     {Filter{Stale: true}, 1},
		"dirty":     {Filter{Dirty: true}, 1},
		"no-remote": {Filter{NoRemote: true}, 1},
		"group":     {Filter{Group: "ac*"}, 2},
		"unsafe":    {Filter{Unsafe: true}, 2}, // b dirty, c has no remote
		"safe":      {Filter{Safe: true}, 1},
		"mine":      {Filter{Mine: true, Identities: []string{"me@x.com"}}, 1},
	} {
		ff := want.f
		ff.StaleDays = 60
		got, err := s.List(&ff)
		must(t, err)
		if len(got) != want.n {
			t.Errorf("%s: got %d rows, want %d", name, len(got), want.n)
		}
	}

	hits, err := s.Search("eks terra", f)
	must(t, err)
	if len(hits) != 1 || hits[0].Path != "/r/a" || hits[0].Snippet == "" {
		t.Errorf("search: %+v", hits)
	}
	if hits, _ := s.Search(`"; DROP TABLE repos; --`, f); len(hits) != 0 {
		t.Errorf("hostile query matched: %v", hits)
	}

	// light update keeps authors and README
	a.Light, a.Authors, a.Readme, a.Dirty, a.UnpushedCommits = true, nil, "", 1, 99
	must(t, s.Save(a, Meta{Remotes: []remote.Remote{gh}}))
	rows, _ = s.List(&Filter{PathLike: "/r/a", Identities: []string{"me@x.com"}})
	if rows[0].MyCommits != 3 || rows[0].Dirty != 1 || rows[0].Unpushed != 0 {
		t.Errorf("light save lost data: %+v", rows[0])
	}
	if hits, _ := s.Search("eks", f); len(hits) != 1 {
		t.Errorf("light save lost readme")
	}

	d, err := s.Dupes(&Filter{})
	must(t, err)
	if len(d) != 1 || len(d[0].Paths) != 2 {
		t.Errorf("dupes: %+v", d)
	}
	st, err := s.Stats("group", f)
	must(t, err)
	if len(st) != 2 || st[0].Key != "acme" || st[0].Repos != 2 {
		t.Errorf("stats: %+v", st)
	}

	must(t, s.AddTags(3, "x"))
	must(t, s.Delete("/r/c"))
	if rows, _ := s.List(f); len(rows) != 2 {
		t.Errorf("delete failed")
	}
	var orphans int
	must(t, s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM tags) + (SELECT COUNT(*) FROM search WHERE rowid = 3)`).Scan(&orphans))
	if orphans != 0 {
		t.Errorf("delete left %d orphan rows (cascade not working)", orphans)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
