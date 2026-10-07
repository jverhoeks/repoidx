// Package store persists the repo index in a SQLite database (pure Go
// driver, no cgo) with an FTS5 table for README / name search.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jverhoeks/repoidx/internal/gitinfo"
	"github.com/jverhoeks/repoidx/internal/remote"

	_ "modernc.org/sqlite"
)

// Store is an open index database.
type Store struct {
	db *sql.DB
}

// Open opens (and creates or migrates) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	// plain path, not a file: URI (C:\ paths on Windows); the driver splits
	// the query string off and applies the pragmas on every connection
	db, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; avoids SQLITE_BUSY between our own goroutines
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("index was created by a newer repoidx (schema %d > %d)", v, schemaVersion)
	}
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	_, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
	return err
}

// Prev returns path -> refs_hash for all indexed repos.
func (s *Store) Prev() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT path, refs_hash FROM repos`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			return nil, err
		}
		m[p] = h
	}
	return m, rows.Err()
}

// Paths returns all indexed repo paths.
func (s *Store) Paths() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM repos ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Meta is per-repo context the scanner adds on top of gitinfo.
type Meta struct {
	Root    string
	Tooling bool
	Remotes []remote.Remote // parsed + alias-resolved
}

// Save writes one repo. When info.Light is set, only the cheap columns are
// updated and authors/README are left as they were.
func (s *Store) Save(in *gitinfo.Info, m Meta) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var prim remote.Remote
	for i, r := range m.Remotes {
		if i == 0 || r.Name == "origin" {
			prim = r
		}
	}
	now := time.Now().Unix()
	name := filepath.Base(in.Path)

	cheap := []any{
		in.Path, name, m.Root, in.GitDir, in.CommonDir, b(in.Bare), b(in.Worktree), b(in.Submodule), b(m.Tooling),
		b(in.HasCommits), in.HeadSHA, in.Branch, in.Upstream, in.DefaultBranch, in.Ahead, in.Behind,
		in.Dirty, in.Untracked, in.Stashes,
		unix(in.LastLocalCommit), unix(in.LastRemoteCommit), unix(in.LastFetch), unix(in.LastActivity),
		in.UserEmail, prim.Host, prim.Provider, prim.Namespace, prim.Project, prim.URL,
		in.RefsHash, in.Err, now,
	}
	const cheapCols = `path, name, root, git_dir, common_dir, is_bare, is_worktree, is_submodule, is_tooling,
		has_commits, head_sha, branch, upstream, default_branch, ahead, behind,
		dirty, untracked, stashes,
		last_local_commit, last_remote_commit, last_fetch, last_activity,
		user_email, host, provider, namespace, project, remote_url,
		refs_hash, scan_error, scanned_at`

	cols, args := cheapCols, cheap
	if !in.Light {
		var total, remoteTotal int
		for _, a := range in.Authors {
			total += a.Count
		}
		for _, a := range in.RemoteAuthors {
			remoteTotal += a.Count
		}
		cols += `, unpushed, git_size_kb, file_count, total_commits, remote_commits, remote_ref, readme_name`
		args = append(args, in.UnpushedCommits, in.GitSizeKB, in.FileCount, total, remoteTotal, in.RemoteRef, in.ReadmeName)
	}
	colList := strings.Split(strings.Join(strings.Fields(cols), ""), ",")
	set := make([]string, 0, len(colList))
	for _, c := range colList[1:] { // all but path
		if c == "root" { // update runs without roots: keep what scan recorded
			set = append(set, "root=CASE WHEN excluded.root='' THEN repos.root ELSE excluded.root END")
			continue
		}
		set = append(set, c+"=excluded."+c)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(colList)), ",")
	var id int64
	err = tx.QueryRow(`INSERT INTO repos (`+strings.Join(colList, ",")+`) VALUES (`+ph+`)
		ON CONFLICT(path) DO UPDATE SET `+strings.Join(set, ",")+` RETURNING id`, args...).Scan(&id)
	if err != nil {
		return fmt.Errorf("save %s: %w", in.Path, err)
	}

	if _, err := tx.Exec(`DELETE FROM remotes WHERE repo_id=?`, id); err != nil {
		return err
	}
	for _, r := range m.Remotes {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO remotes(repo_id,name,url,host,alias,provider,namespace,project) VALUES(?,?,?,?,?,?,?,?)`,
			id, r.Name, r.URL, r.Host, r.Alias, r.Provider, r.Namespace, r.Project); err != nil {
			return err
		}
	}

	if !in.Light {
		if _, err := tx.Exec(`DELETE FROM authors WHERE repo_id=?`, id); err != nil {
			return err
		}
		type ac struct {
			name         string
			local, remot int
		}
		byEmail := map[string]*ac{}
		get := func(a gitinfo.Author) *ac {
			key := a.Email
			if key == "" {
				key = "name:" + a.Name
			}
			x := byEmail[key]
			if x == nil {
				x = &ac{name: a.Name}
				byEmail[key] = x
			}
			return x
		}
		for _, a := range in.Authors {
			get(a).local += a.Count
		}
		for _, a := range in.RemoteAuthors {
			get(a).remot += a.Count
		}
		for email, x := range byEmail {
			if _, err := tx.Exec(`INSERT INTO authors(repo_id,email,name,local_commits,remote_commits) VALUES(?,?,?,?,?)`,
				id, email, x.name, x.local, x.remot); err != nil {
				return err
			}
		}
	}

	// search index: readme only changes on a full scan
	var readme string
	if in.Light {
		_ = tx.QueryRow(`SELECT readme FROM search WHERE rowid=?`, id).Scan(&readme)
	} else {
		readme = in.Readme
	}
	if _, err := tx.Exec(`DELETE FROM search WHERE rowid=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO search(rowid,name,slug,grp,path,readme) VALUES(?,?,?,(SELECT grp FROM repos WHERE id=?),?,?)`,
		id, name, prim.Slug(), id, in.Path, readme); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete removes repos by path.
func (s *Store) Delete(paths ...string) error {
	for _, p := range paths {
		var id int64
		if err := s.db.QueryRow(`SELECT id FROM repos WHERE path=?`, p).Scan(&id); err != nil {
			continue
		}
		if _, err := s.db.Exec(`DELETE FROM search WHERE rowid=?`, id); err != nil {
			return err
		}
		if _, err := s.db.Exec(`DELETE FROM repos WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// TouchRoot records a completed scan of root.
func (s *Store) TouchRoot(root string) error {
	_, err := s.db.Exec(`INSERT INTO roots(path,last_scan) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET last_scan=excluded.last_scan`,
		root, time.Now().Unix())
	return err
}

// GroupInput is what grouping needs to know about a repo.
type GroupInput struct {
	ID      int64
	Path    string
	Remotes []remote.Remote
}

// GroupInputs loads every repo with its remotes.
func (s *Store) GroupInputs() ([]GroupInput, map[string]string, error) {
	rows, err := s.db.Query(`SELECT r.id, r.path, COALESCE(m.name,''), COALESCE(m.url,''), COALESCE(m.host,''), COALESCE(m.alias,''),
		COALESCE(m.provider,''), COALESCE(m.namespace,''), COALESCE(m.project,'')
		FROM repos r LEFT JOIN remotes m ON m.repo_id = r.id ORDER BY r.id, m.name='origin' DESC, m.name`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []GroupInput
	for rows.Next() {
		var id int64
		var p string
		var r remote.Remote
		if err := rows.Scan(&id, &p, &r.Name, &r.URL, &r.Host, &r.Alias, &r.Provider, &r.Namespace, &r.Project); err != nil {
			return nil, nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			out = append(out, GroupInput{ID: id, Path: p})
		}
		if r.Name != "" {
			out[len(out)-1].Remotes = append(out[len(out)-1].Remotes, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	ov := map[string]string{}
	orows, err := s.db.Query(`SELECT path, grp FROM group_overrides`)
	if err != nil {
		return nil, nil, err
	}
	defer orows.Close()
	for orows.Next() {
		var p, g string
		if err := orows.Scan(&p, &g); err != nil {
			return nil, nil, err
		}
		ov[p] = g
	}
	return out, ov, orows.Err()
}

// GroupResult is the outcome of grouping one repo.
type GroupResult struct {
	ID       int64
	Group    string
	Source   string
	Identity string
}

// SetGroups stores grouping results (and mirrors the group into the search index).
func (s *Store) SetGroups(rs []GroupResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rs {
		if _, err := tx.Exec(`UPDATE repos SET grp=?, grp_source=?, expected_email=? WHERE id=?`, r.Group, r.Source, r.Identity, r.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE search SET grp=? WHERE rowid=?`, r.Group, r.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetOverride pins a repo to a group ("" clears the override).
func (s *Store) SetOverride(path, grp string) error {
	if grp == "" {
		_, err := s.db.Exec(`DELETE FROM group_overrides WHERE path=?`, path)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO group_overrides(path,grp) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET grp=excluded.grp`, path, grp)
	return err
}

// AddTags / RemoveTags manage free-form tags.
func (s *Store) AddTags(id int64, tags ...string) error {
	for _, t := range tags {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO tags(repo_id,tag) VALUES(?,?)`, id, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RemoveTags(id int64, tags ...string) error {
	for _, t := range tags {
		if _, err := s.db.Exec(`DELETE FROM tags WHERE repo_id=? AND tag=?`, id, t); err != nil {
			return err
		}
	}
	return nil
}

// UserEmails returns all distinct effective user.email values in the index.
func (s *Store) UserEmails() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT user_email FROM repos WHERE user_email != '' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func b(v bool) int {
	if v {
		return 1
	}
	return 0
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
