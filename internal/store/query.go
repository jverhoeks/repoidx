package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Repo is one row as shown by list/show/search (and emitted as JSON).
type Repo struct {
	ID            int64  `json:"id"`
	Path          string `json:"path"`
	Name          string `json:"name"`
	Group         string `json:"group"`
	GroupSource   string `json:"group_source"`
	ExpectedEmail string `json:"expected_email,omitempty"`
	UserEmail     string `json:"user_email,omitempty"`

	Bare       bool `json:"bare,omitempty"`
	Worktree   bool `json:"worktree,omitempty"`
	Submodule  bool `json:"submodule,omitempty"`
	Tooling    bool `json:"tooling,omitempty"`
	HasCommits bool `json:"has_commits"`

	Branch        string `json:"branch"`
	Upstream      string `json:"upstream,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	HeadSHA       string `json:"head,omitempty"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
	Dirty         int    `json:"dirty"`
	Untracked     int    `json:"untracked"`
	Stashes       int    `json:"stashes"`
	Unpushed      int    `json:"unpushed_commits"`

	LastLocalCommit  time.Time `json:"last_local_commit"`
	LastRemoteCommit time.Time `json:"last_remote_commit"`
	LastFetch        time.Time `json:"last_fetch"`
	LastActivity     time.Time `json:"last_activity"`
	LastAny          time.Time `json:"last_any"`
	Stale            bool      `json:"stale"`

	GitSizeKB       int64  `json:"git_size_kb"`
	Files           int    `json:"files"`
	TotalCommits    int    `json:"total_commits"`
	RemoteCommits   int    `json:"remote_commits"`
	RemoteRef       string `json:"remote_ref,omitempty"`
	MyCommits       int    `json:"my_commits"`
	MyRemoteCommits int    `json:"my_remote_commits"`

	Host      string `json:"host,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Project   string `json:"project,omitempty"`
	RemoteURL string `json:"remote_url,omitempty"`

	Tags       []string  `json:"tags,omitempty"`
	ReadmeName string    `json:"readme,omitempty"`
	ScanError  string    `json:"scan_error,omitempty"`
	ScannedAt  time.Time `json:"scanned_at"`

	Snippet string  `json:"snippet,omitempty"` // search only
	Rank    float64 `json:"-"`
}

// Slug returns host/namespace/project for the primary remote.
func (r *Repo) Slug() string {
	var parts []string
	for _, p := range []string{r.Host, r.Namespace, r.Project} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// Unsafe reports whether deleting this clone would lose work.
func (r *Repo) Unsafe() bool {
	return r.Dirty > 0 || r.Untracked > 0 || r.Stashes > 0 || r.Unpushed > 0 || r.Host == "" && r.HasCommits || r.ScanError != ""
}

// Filter selects repos.
type Filter struct {
	Group, Provider, Host, Tag, PathLike string

	Stale, Dirty, Behind, Ahead, Unpushed, NoRemote bool
	WrongIdentity, Unsafe, Safe                     bool
	Mine                                            bool // has commits by one of Identities

	IncludeTooling bool // show tooling repos too
	OnlyTooling    bool

	StaleDays  int
	Identities []string
	Sort       string // path, name, group, last, size, files, mine, commits
	Limit      int
}

const lastAnyExpr = `MAX(r.last_local_commit, r.last_remote_commit, r.last_fetch, r.last_activity)`

func (f *Filter) cutoff() int64 {
	d := f.StaleDays
	if d <= 0 {
		d = 60
	}
	return time.Now().Add(-time.Duration(d) * 24 * time.Hour).Unix()
}

func (f *Filter) where() (string, []any) {
	var w []string
	var a []any
	add := func(cond string, args ...any) {
		w = append(w, cond)
		a = append(a, args...)
	}
	switch {
	case f.OnlyTooling:
		add(`r.is_tooling = 1`)
	case !f.IncludeTooling:
		add(`r.is_tooling = 0`)
	}
	if f.Group != "" {
		add(`r.grp LIKE ?`, likePattern(f.Group))
	}
	if f.Provider != "" {
		add(`r.provider = ?`, f.Provider)
	}
	if f.Host != "" {
		add(`r.host LIKE ?`, likePattern(f.Host))
	}
	if f.Tag != "" {
		add(`EXISTS (SELECT 1 FROM tags t WHERE t.repo_id = r.id AND t.tag = ?)`, f.Tag)
	}
	if f.PathLike != "" {
		add(`(r.path LIKE ? OR r.name LIKE ?)`, "%"+f.PathLike+"%", "%"+f.PathLike+"%")
	}
	if f.Stale {
		add(lastAnyExpr+` < ?`, f.cutoff())
	}
	if f.Dirty {
		add(`(r.dirty > 0 OR r.untracked > 0)`)
	}
	if f.Behind {
		add(`r.behind > 0`)
	}
	if f.Ahead {
		add(`r.ahead > 0`)
	}
	if f.Unpushed {
		add(`r.unpushed > 0`)
	}
	if f.NoRemote {
		add(`r.host = '' AND NOT EXISTS (SELECT 1 FROM remotes m WHERE m.repo_id = r.id)`)
	}
	if f.WrongIdentity {
		add(`r.expected_email != '' AND r.user_email != r.expected_email`)
	}
	// a repo with collection errors (e.g. status timed out) is not proven safe
	unsafe := `(r.dirty > 0 OR r.untracked > 0 OR r.stashes > 0 OR r.unpushed > 0 OR (r.host = '' AND r.has_commits = 1) OR r.scan_error != '')`
	if f.Unsafe {
		add(unsafe)
	}
	if f.Safe {
		add(`NOT ` + unsafe)
	}
	if f.Mine {
		ids, args := f.idents()
		add(`EXISTS (SELECT 1 FROM authors x WHERE x.repo_id = r.id AND x.email IN (`+ids+`))`, args...)
	}
	if len(w) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(w, " AND "), a
}

func (f *Filter) idents() (string, []any) {
	if len(f.Identities) == 0 {
		return "''", nil
	}
	args := make([]any, len(f.Identities))
	for i, e := range f.Identities {
		args[i] = e
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(args)), ","), args
}

// likePattern turns a user glob (customer*) into a LIKE pattern; without
// wildcards it matches exactly.
func likePattern(s string) string {
	return strings.NewReplacer("*", "%", "?", "_").Replace(s)
}

func (f *Filter) orderBy() string {
	switch f.Sort {
	case "name":
		return ` ORDER BY r.name COLLATE NOCASE, r.path`
	case "group":
		return ` ORDER BY r.grp COLLATE NOCASE, r.name COLLATE NOCASE`
	case "last", "age":
		return ` ORDER BY last_any DESC`
	case "oldest":
		return ` ORDER BY last_any ASC`
	case "size":
		return ` ORDER BY r.git_size_kb DESC`
	case "files":
		return ` ORDER BY r.file_count DESC`
	case "mine":
		return ` ORDER BY my_commits DESC`
	case "commits":
		return ` ORDER BY r.total_commits DESC`
	default:
		return ` ORDER BY r.path`
	}
}

func (f *Filter) selectCols() (string, []any) {
	ids, args := f.idents()
	cols := `r.id, r.path, r.name, r.grp, r.grp_source, r.expected_email, r.user_email,
		r.is_bare, r.is_worktree, r.is_submodule, r.is_tooling, r.has_commits,
		r.branch, r.upstream, r.default_branch, r.head_sha, r.ahead, r.behind, r.dirty, r.untracked, r.stashes, r.unpushed,
		r.last_local_commit, r.last_remote_commit, r.last_fetch, r.last_activity, ` + lastAnyExpr + ` AS last_any,
		r.git_size_kb, r.file_count, r.total_commits, r.remote_commits, r.remote_ref,
		COALESCE((SELECT SUM(x.local_commits) FROM authors x WHERE x.repo_id = r.id AND x.email IN (` + ids + `)), 0) AS my_commits,
		COALESCE((SELECT SUM(x.remote_commits) FROM authors x WHERE x.repo_id = r.id AND x.email IN (` + ids + `)), 0) AS my_remote,
		r.host, r.provider, r.namespace, r.project, r.remote_url,
		COALESCE((SELECT group_concat(t.tag, ',') FROM tags t WHERE t.repo_id = r.id), ''),
		r.readme_name, r.scan_error, r.scanned_at`
	return cols, append(args, args...)
}

func scanRepo(sc interface{ Scan(...any) error }, extra ...any) (*Repo, error) {
	var r Repo
	var bare, wt, sub, tool, hc int
	var ll, lr, lf, la, lany, scanned int64
	var tags string
	dest := []any{&r.ID, &r.Path, &r.Name, &r.Group, &r.GroupSource, &r.ExpectedEmail, &r.UserEmail,
		&bare, &wt, &sub, &tool, &hc,
		&r.Branch, &r.Upstream, &r.DefaultBranch, &r.HeadSHA, &r.Ahead, &r.Behind, &r.Dirty, &r.Untracked, &r.Stashes, &r.Unpushed,
		&ll, &lr, &lf, &la, &lany,
		&r.GitSizeKB, &r.Files, &r.TotalCommits, &r.RemoteCommits, &r.RemoteRef,
		&r.MyCommits, &r.MyRemoteCommits,
		&r.Host, &r.Provider, &r.Namespace, &r.Project, &r.RemoteURL,
		&tags, &r.ReadmeName, &r.ScanError, &scanned}
	if err := sc.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	r.Bare, r.Worktree, r.Submodule, r.Tooling, r.HasCommits = bare == 1, wt == 1, sub == 1, tool == 1, hc == 1
	r.LastLocalCommit, r.LastRemoteCommit, r.LastFetch, r.LastActivity, r.LastAny = ts(ll), ts(lr), ts(lf), ts(la), ts(lany)
	r.ScannedAt = ts(scanned)
	if tags != "" {
		r.Tags = strings.Split(tags, ",")
	}
	return &r, nil
}

func (s *Store) finish(rows []*Repo, f *Filter) []*Repo {
	cut := time.Unix(f.cutoff(), 0)
	for _, r := range rows {
		r.Stale = r.LastAny.Before(cut)
	}
	return rows
}

// List returns repos matching f.
func (s *Store) List(f *Filter) ([]*Repo, error) {
	cols, cargs := f.selectCols()
	where, wargs := f.where()
	q := `SELECT ` + cols + ` FROM repos r` + where + f.orderBy()
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.Query(q, append(cargs, wargs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return s.finish(out, f), rows.Err()
}

// Search runs a full-text query over name, remote slug, group, path and README.
func (s *Store) Search(query string, f *Filter) ([]*Repo, error) {
	fts := ftsQuery(query)
	if fts == "" {
		return nil, fmt.Errorf("empty search query")
	}
	cols, cargs := f.selectCols()
	where, wargs := f.where()
	cond := ` WHERE search MATCH ?`
	if where != "" {
		cond += " AND " + strings.TrimPrefix(where, " WHERE ")
	}
	q := `SELECT ` + cols + `, snippet(search, 4, '[', ']', '…', 10), bm25(search, 10.0, 6.0, 3.0, 2.0, 1.0) AS rank
		FROM search JOIN repos r ON r.id = search.rowid` + cond + ` ORDER BY rank`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	args := append(cargs, fts)
	args = append(args, wargs...)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Repo
	for rows.Next() {
		var snip string
		var rank float64
		r, err := scanRepo(rows, &snip, &rank)
		if err != nil {
			return nil, err
		}
		r.Snippet = strings.Join(strings.Fields(snip), " ")
		r.Rank = rank
		out = append(out, r)
	}
	return s.finish(out, f), rows.Err()
}

// ftsQuery makes user input safe for FTS5: every word becomes a quoted
// prefix term, all terms must match. Words starting with '-' are excluded.
func ftsQuery(q string) string {
	var pos, neg []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.')
	}) {
		not := strings.HasPrefix(w, "-")
		w = strings.Trim(w, "-_.")
		if w == "" {
			continue
		}
		term := `"` + strings.ReplaceAll(w, `"`, `""`) + `"*`
		if not {
			neg = append(neg, term)
		} else {
			pos = append(pos, term)
		}
	}
	if len(pos) == 0 {
		return ""
	}
	out := strings.Join(pos, " ")
	for _, n := range neg {
		out += " NOT " + n
	}
	return out
}

// Find resolves a user-supplied repo reference: exact path, then exact name,
// project or slug, then a unique substring of the path.
func (s *Store) Find(ref string, f *Filter) ([]*Repo, error) {
	all := *f
	all.IncludeTooling = true
	all.OnlyTooling = false
	cols, cargs := all.selectCols()
	for _, q := range []struct {
		cond string
		arg  string
	}{
		{`r.path = ?`, ref},
		{`r.name = ? OR r.project = ? OR (r.host || '/' || r.namespace || '/' || r.project) = ? OR (r.namespace || '/' || r.project) = ?`, ref},
		{`r.path LIKE ?`, "%" + ref + "%"},
	} {
		n := strings.Count(q.cond, "?")
		args := append([]any(nil), cargs...)
		for i := 0; i < n; i++ {
			args = append(args, q.arg)
		}
		rows, err := s.db.Query(`SELECT `+cols+` FROM repos r WHERE `+q.cond+` ORDER BY r.path`, args...)
		if err != nil {
			return nil, err
		}
		var out []*Repo
		for rows.Next() {
			r, err := scanRepo(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, r)
		}
		rows.Close()
		if len(out) > 0 {
			return s.finish(out, f), nil
		}
	}
	return nil, nil
}

// Detail is the extra information shown by `show`.
type Detail struct {
	Remotes []RemoteRow  `json:"remotes"`
	Authors []AuthorRow  `json:"authors"`
	Readme  string       `json:"readme_excerpt,omitempty"`
	Related []RelatedRow `json:"related,omitempty"`
}

type RemoteRow struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Host     string `json:"host"`
	Alias    string `json:"alias,omitempty"`
	Provider string `json:"provider"`
	Slug     string `json:"slug"`
}

type AuthorRow struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	Local  int    `json:"local"`
	Remote int    `json:"remote"`
}

type RelatedRow struct {
	Kind string `json:"kind"` // clone (same remote), worktree (same git dir)
	Path string `json:"path"`
}

// Detail loads remotes, top authors, a README excerpt and related repos.
func (s *Store) Detail(r *Repo, topAuthors int) (*Detail, error) {
	d := &Detail{}
	rows, err := s.db.Query(`SELECT name, url, host, alias, provider, namespace, project FROM remotes WHERE repo_id=? ORDER BY name='origin' DESC, name`, r.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x RemoteRow
		var ns, proj string
		if err := rows.Scan(&x.Name, &x.URL, &x.Host, &x.Alias, &x.Provider, &ns, &proj); err != nil {
			rows.Close()
			return nil, err
		}
		x.Slug = strings.Trim(x.Host+"/"+ns+"/"+proj, "/")
		d.Remotes = append(d.Remotes, x)
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT email, name, local_commits, remote_commits FROM authors WHERE repo_id=? ORDER BY local_commits DESC, remote_commits DESC LIMIT ?`, r.ID, topAuthors)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a AuthorRow
		if err := rows.Scan(&a.Email, &a.Name, &a.Local, &a.Remote); err != nil {
			rows.Close()
			return nil, err
		}
		d.Authors = append(d.Authors, a)
	}
	rows.Close()

	var readme sql.NullString
	_ = s.db.QueryRow(`SELECT readme FROM search WHERE rowid=?`, r.ID).Scan(&readme)
	d.Readme = readme.String

	rows, err = s.db.Query(`SELECT 'worktree', path FROM repos WHERE common_dir = ? AND id != ? AND common_dir != ''
		UNION SELECT 'clone', path FROM repos WHERE host = ? AND namespace = ? AND project = ? AND host != '' AND id != ? AND common_dir != ?`,
		commonDir(s, r.ID), r.ID, r.Host, r.Namespace, r.Project, r.ID, commonDir(s, r.ID))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x RelatedRow
		if err := rows.Scan(&x.Kind, &x.Path); err != nil {
			rows.Close()
			return nil, err
		}
		d.Related = append(d.Related, x)
	}
	rows.Close()
	return d, nil
}

func commonDir(s *Store, id int64) string {
	var c string
	_ = s.db.QueryRow(`SELECT common_dir FROM repos WHERE id=?`, id).Scan(&c)
	return c
}

// StatRow is an aggregate for one group/provider/host.
type StatRow struct {
	Key       string `json:"key"`
	Repos     int    `json:"repos"`
	Files     int    `json:"files"`
	GitSizeKB int64  `json:"git_size_kb"`
	Stale     int    `json:"stale"`
	Dirty     int    `json:"dirty"`
	Unpushed  int    `json:"unpushed"`
	Behind    int    `json:"behind"`
	NoRemote  int    `json:"no_remote"`
	MyCommits int    `json:"my_commits"`
}

// Stats aggregates the filtered repos by "group", "provider", "host" or "namespace".
func (s *Store) Stats(by string, f *Filter) ([]StatRow, error) {
	key := map[string]string{
		"group": "r.grp", "provider": "r.provider", "host": "r.host",
		"namespace": "(r.host || '/' || r.namespace)", "root": "r.root",
	}[by]
	if key == "" {
		return nil, fmt.Errorf("unknown --by %q (group, provider, host, namespace, root)", by)
	}
	ids, iargs := f.idents()
	where, wargs := f.where()
	q := `SELECT COALESCE(NULLIF(` + key + `, ''), '-') AS k, COUNT(*), SUM(r.file_count), SUM(CASE WHEN r.is_worktree THEN 0 ELSE r.git_size_kb END),
		SUM(` + lastAnyExpr + ` < ?), SUM(r.dirty > 0 OR r.untracked > 0), SUM(r.unpushed > 0), SUM(r.behind > 0), SUM(r.host = ''),
		COALESCE(SUM((SELECT SUM(x.local_commits) FROM authors x WHERE x.repo_id = r.id AND x.email IN (` + ids + `))), 0)
		FROM repos r` + where + ` GROUP BY k ORDER BY COUNT(*) DESC, k`
	args := append([]any{f.cutoff()}, iargs...)
	rows, err := s.db.Query(q, append(args, wargs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatRow
	for rows.Next() {
		var x StatRow
		if err := rows.Scan(&x.Key, &x.Repos, &x.Files, &x.GitSizeKB, &x.Stale, &x.Dirty, &x.Unpushed, &x.Behind, &x.NoRemote, &x.MyCommits); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Dupe is a remote that is cloned in more than one place.
type Dupe struct {
	Slug  string   `json:"slug"`
	Paths []string `json:"paths"`
}

// Dupes finds remotes cloned more than once (worktrees of the same clone do
// not count as duplicates).
func (s *Store) Dupes(f *Filter) ([]Dupe, error) {
	where, wargs := f.where()
	cond := ` WHERE r.host != '' AND r.is_worktree = 0`
	if where != "" {
		cond += " AND " + strings.TrimPrefix(where, " WHERE ")
	}
	rows, err := s.db.Query(`SELECT r.host || '/' || r.namespace || '/' || r.project AS slug, r.path FROM repos r`+cond+`
		AND (r.host, r.namespace, r.project) IN (
			SELECT host, namespace, project FROM repos WHERE host != '' AND is_worktree = 0
			GROUP BY host, namespace, project HAVING COUNT(DISTINCT common_dir) > 1)
		ORDER BY slug, r.path`, wargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dupe
	for rows.Next() {
		var slug, p string
		if err := rows.Scan(&slug, &p); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Slug != slug {
			out = append(out, Dupe{Slug: slug})
		}
		out[len(out)-1].Paths = append(out[len(out)-1].Paths, p)
	}
	return out, rows.Err()
}

func ts(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
