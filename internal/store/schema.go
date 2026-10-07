package store

const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS repos (
	id                 INTEGER PRIMARY KEY,
	path               TEXT NOT NULL UNIQUE,
	name               TEXT NOT NULL,
	root               TEXT NOT NULL DEFAULT '',
	git_dir            TEXT NOT NULL DEFAULT '',
	common_dir         TEXT NOT NULL DEFAULT '',
	is_bare            INTEGER NOT NULL DEFAULT 0,
	is_worktree        INTEGER NOT NULL DEFAULT 0,
	is_submodule       INTEGER NOT NULL DEFAULT 0,
	is_tooling         INTEGER NOT NULL DEFAULT 0,
	has_commits        INTEGER NOT NULL DEFAULT 0,
	head_sha           TEXT NOT NULL DEFAULT '',
	branch             TEXT NOT NULL DEFAULT '',
	upstream           TEXT NOT NULL DEFAULT '',
	default_branch     TEXT NOT NULL DEFAULT '',
	ahead              INTEGER NOT NULL DEFAULT 0,
	behind             INTEGER NOT NULL DEFAULT 0,
	dirty              INTEGER NOT NULL DEFAULT 0,
	untracked          INTEGER NOT NULL DEFAULT 0,
	stashes            INTEGER NOT NULL DEFAULT 0,
	unpushed           INTEGER NOT NULL DEFAULT 0,
	last_local_commit  INTEGER NOT NULL DEFAULT 0,
	last_remote_commit INTEGER NOT NULL DEFAULT 0,
	last_fetch         INTEGER NOT NULL DEFAULT 0,
	last_activity      INTEGER NOT NULL DEFAULT 0,
	git_size_kb        INTEGER NOT NULL DEFAULT 0,
	file_count         INTEGER NOT NULL DEFAULT 0,
	total_commits      INTEGER NOT NULL DEFAULT 0,
	remote_commits     INTEGER NOT NULL DEFAULT 0,
	remote_ref         TEXT NOT NULL DEFAULT '',
	user_email         TEXT NOT NULL DEFAULT '',
	host               TEXT NOT NULL DEFAULT '',   -- primary remote (origin, else first)
	provider           TEXT NOT NULL DEFAULT '',
	namespace          TEXT NOT NULL DEFAULT '',
	project            TEXT NOT NULL DEFAULT '',
	remote_url         TEXT NOT NULL DEFAULT '',
	grp                TEXT NOT NULL DEFAULT '',
	grp_source         TEXT NOT NULL DEFAULT '',
	expected_email     TEXT NOT NULL DEFAULT '',
	readme_name        TEXT NOT NULL DEFAULT '',
	refs_hash          TEXT NOT NULL DEFAULT '',
	scan_error         TEXT NOT NULL DEFAULT '',
	scanned_at         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS repos_grp ON repos(grp);
CREATE INDEX IF NOT EXISTS repos_slug ON repos(host, namespace, project);
CREATE INDEX IF NOT EXISTS repos_common ON repos(common_dir);

CREATE TABLE IF NOT EXISTS remotes (
	repo_id   INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	name      TEXT NOT NULL,
	url       TEXT NOT NULL,
	host      TEXT NOT NULL DEFAULT '',
	alias     TEXT NOT NULL DEFAULT '',
	provider  TEXT NOT NULL DEFAULT '',
	namespace TEXT NOT NULL DEFAULT '',
	project   TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (repo_id, name)
);

CREATE TABLE IF NOT EXISTS authors (
	repo_id        INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	email          TEXT NOT NULL,
	name           TEXT NOT NULL DEFAULT '',
	local_commits  INTEGER NOT NULL DEFAULT 0,
	remote_commits INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (repo_id, email)
);
CREATE INDEX IF NOT EXISTS authors_email ON authors(email);

CREATE TABLE IF NOT EXISTS tags (
	repo_id INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	tag     TEXT NOT NULL,
	PRIMARY KEY (repo_id, tag)
);

-- manual group overrides are keyed by path so they survive a repo being
-- removed from and re-added to the index
CREATE TABLE IF NOT EXISTS group_overrides (
	path TEXT PRIMARY KEY,
	grp  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS roots (
	path      TEXT PRIMARY KEY,
	last_scan INTEGER NOT NULL
);

-- full-text index; rowid = repos.id
CREATE VIRTUAL TABLE IF NOT EXISTS search USING fts5(
	name, slug, grp, path, readme,
	tokenize = 'unicode61 remove_diacritics 2'
);
`
