# repoidx

**Index, search and inspect every git repository on your machine.**

Over the years a laptop collects hundreds of clones: work for different
customers, personal projects, half-finished experiments, third-party code
you cloned once to read. `repoidx` finds them all, records the state of
each one in a local SQLite index, and answers questions like:

- Where did I clone that project again?
- Which repos have work that exists **only on this machine**?
- What can I safely delete?
- Which repos belong to customer A, and am I committing there with the right email?
- What did I work on last month?
- What is cloned three times in three different folders?

It is a single static binary (pure Go, no cgo) for macOS, Linux and Windows.
It needs `git` in your `PATH` and works without any configuration.

```console
$ repoidx scan
312 repos: 312 indexed (0 unchanged), 0 failed, 0 removed in 1m24s

$ repoidx stats -s
== Overview
repos          312     in 41 groups
tracked files  96410
.git size      6.4G    402.7M in cleanup candidates
your commits   8731

== Health
                                  REPOS  SEE
cleanup candidates (stale, safe)  141    repoidx list --stale --safe
at risk (would lose work)         88     repoidx list --unsafe
uncommitted changes               71     repoidx list --dirty
commits on no remote              23     repoidx list --unpushed
...
```

---

## Contents

- [Features](#features)
- [Install](#install)
- [Quick start](#quick-start)
- [A tour](#a-tour)
- [What is recorded](#what-is-recorded)
- [The state column](#the-state-column)
- [Commands](#commands)
- [Filters](#filters)
- [Groups: personal, customer A, customer B](#groups-personal-customer-a-customer-b)
- [Identities and aliases](#identities-and-aliases)
- [Zero config defaults](#zero-config-defaults)
- [Config file](#config-file)
- [Scripting and JSON](#scripting-and-json)
- [Safety: what repoidx will never do](#safety-what-repoidx-will-never-do)
- [Performance](#performance)
- [Troubleshooting](#troubleshooting)
- [How it works](#how-it-works)
- [Development](#development)
- [Roadmap](#roadmap)

---

## Features

| | |
|---|---|
| **Discovery** | Parallel walk of one or more folders (default `~`). It skips `node_modules`, `.terraform`, virtualenvs, caches and `~/Library`, and never follows symlinks. It finds normal repos, worktrees, submodules and bare repos. |
| **Remotes** | Parses SSH, scp-style, HTTPS, Azure DevOps and local remotes into host / provider / org / project. GitLab subgroups are kept. SSH aliases (`github-work`) are resolved via `ssh -G`. Credentials in URLs are never stored. |
| **Sync state** | Branch, upstream, ahead / behind, commits that are on no remote at all, stashes, dirty and untracked files. |
| **Staleness** | Last local commit, last remote commit, last fetch and last activity. A repo is *stale* when all of them are older than `stale_days` (default 60). |
| **Your work** | Commit counts per author, locally and on the remote branch; "mine" is matched against all your identities. |
| **Size** | Tracked file count and `.git` size, without walking the working tree. |
| **README** | Full-text indexed (SQLite FTS5) together with name, remote, group and path. |
| **Groups** | Personal / customer A / customer B, from your git `includeIf` setup, config rules or manual pins. |
| **Cleanup help** | `--safe` and `--unsafe` filters, duplicate clones, stale repos and largest repos. |
| **Fast rescans** | Unchanged repos (same refs) only get a cheap status refresh. |
| **Read-only** | Never modifies your repos, unless you explicitly ask for `update --fetch`. |

## Install

**Homebrew** (macOS and Linux):

```sh
brew install --cask jverhoeks/tap/repoidx
```

**Binaries**: download the archive for your platform from the
[releases](https://github.com/jverhoeks/repoidx/releases) page
(macOS, Linux, Windows; amd64 and arm64) and put `repoidx` on your `PATH`.

**Go**:

```sh
go install github.com/jverhoeks/repoidx@latest
```

**From source**:

```sh
make build     # ./bin/repoidx
make dist      # cross-compiled binaries in ./dist
```

Shell completion: `repoidx completion zsh|bash|fish|powershell --help`.

## Quick start

```sh
repoidx scan                      # index everything below ~ (or: repoidx scan ~/src ~/work)
repoidx stats -s                  # overview
repoidx list --unsafe             # repos with work that would be lost
repoidx list --stale --safe       # cleanup candidates
repoidx search terraform eks      # full text, including READMEs
cd "$(repoidx path myproject)"    # jump to a repo
repoidx update                    # refresh known repos without walking the disk
```

Every command accepts `--json`.

## A tour

The examples below use made-up data for a user `alex` who works on
personal projects (`alex-dev` on GitHub) and for two customers, *acme*
(self-hosted GitLab) and *globex* (GitHub org).

### Overview

```console
$ repoidx stats -s --top 5

== Overview
repos          312     in 41 groups
tracked files  96410
.git size      6.4G    402.7M in cleanup candidates
your commits   8731

== Health
                                  REPOS  SEE
cleanup candidates (stale, safe)  141    repoidx list --stale --safe
at risk (would lose work)         88     repoidx list --unsafe
uncommitted changes               71     repoidx list --dirty
commits on no remote              23     repoidx list --unpushed
stashes                           9      repoidx list --unsafe
no remote                         19     repoidx list --no-remote
behind upstream                   12     repoidx update --fetch; list --behind
stale (> 60d)                     197    repoidx list --stale
wrong identity                    3      repoidx list --wrong-identity

== Last activity
< 1 week    54   ███████████████▌
< 1 month   31   ████████▉
< 60 days   12   ███▍
< 6 months  139  ████████████████████████████████████████
< 1 year    22   ██████▎
< 2 years   33   █████████▍
older       21   ██████

== By provider
PROVIDER  REPOS  FILES  .GIT   STALE  DIRTY  UNPUSHED  BEHIND  NO-REMOTE  MINE
github    251    71022  3.1G   171    52     11        7       0          4120
gitlab    42     24021  3.3G   8      12     6         5       0          4502
-         19     1367   21.4M  18     7      6         0       19         109
TOTAL     312    96410  6.4G   197    71     23        12      19         8731

== Top groups (of 41)
GROUP      REPOS  FILES  .GIT    STALE  DIRTY  UNPUSHED  BEHIND  NO-REMOTE  MINE
acme       42     24021  3.3G    8      12     6         5       0          4502
globex     61     8120   611.0M  44     9      3         2       0          2210
personal   38     11402  402.3M  27     21     5         1       0          1810
local      19     1367   21.4M   18     7      6         0       19         109
oss        112    45320  1.9G    90     18     2         4       0          100
… 36 more  40     6180   190.2M  10     4      1         0       0          0
TOTAL      312    96410  6.4G    197    71     23        12      19         8731

== Largest repos (.git)
NAME             .GIT    FILES  STATE      LAST  PATH
data-platform    2.1G    4401   ?11 !20    2h    ~/src/acme/data-platform
superset         838.8M  5604   ok         3mo   ~/src/oss/superset
kernel-tools     612.0M  9310   stale      1y    ~/src/oss/kernel-tools
api-gateway      301.4M  1210   *2         4d    ~/src/globex/api-gateway
dotfiles         12.1M   210    ↑1         1d    ~/src/personal/dotfiles

== Most of your commits
NAME             MINE  TOTAL  GROUP     LAST  PATH
data-platform    3812  3845   acme      2h    ~/src/acme/data-platform
api-gateway      1120  2301   globex    4d    ~/src/globex/api-gateway
blog             640   652    personal  2w    ~/src/personal/blog
infra-modules    433   502    acme      1mo   ~/src/acme/infra-modules
dotfiles         210   210    personal  1d    ~/src/personal/dotfiles

== Cloned more than once (6)
github.com/apache/superset        ~/src/oss/superset  ~/tmp/superset
github.com/alex-dev/databrowser   ~/src/personal/databrowser  ~/src/tools/databrowser
...
```

Plain `repoidx stats` prints the group table only; `--by provider|host|namespace|root`
pivots it differently.

### Finding things

```console
$ repoidx search terraform eks
infra-modules          acme      ok     ~/src/acme/infra-modules
    # [Terraform] modules for the [EKS] platform. Provides cluster…
eks-blueprints         oss       stale  ~/src/oss/eks-blueprints
    …example [Terraform] configuration for an [EKS] cluster with…

$ repoidx search kafka -connect          # "-word" excludes
$ repoidx search api --paths             # just the paths, for scripting
$ cd "$(repoidx path api-gateway)"
```

Search covers the repo name, the remote (`host/org/project`), the group, the
path and the README. Every word is a prefix match, results are ranked by
relevance (BM25), and all list filters apply (`repoidx search -g acme vpc`).

### Inspecting one repo

```console
$ repoidx show databrowser
path           /home/alex/src/tools/databrowser
group          personal (config)
branch         feature/parquet
default        origin/main
state          *1 ?1 s1 !1 stale
remote origin  github.com/alex-dev/databrowser  [github]  git@github.com:alex-dev/databrowser.git
last local     2024-10-27 10:54 (2y ago)
last remote    2024-10-27 10:54 (2y ago)
last fetch     2024-10-27 11:45 (2y ago)
last activity  2024-10-27 10:58 (2y ago)
files          17 tracked, .git 6.1M
commits        110 local, 112 on origin/main
mine           91 local, 93 remote
user.email     alex@example.com
scanned        2026-10-07 16:08 (42m ago)

top authors (local / remote):
  59  60  Alex <alex@example.com>
  32  33  Alex Doe <alex.doe@acme.example>
  19  19  dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>

related:
  clone    ~/src/personal/databrowser

README.md:
  # databrowser
  An easy file browser for parquet, json and csv files.
  …
```

`show`, `path`, `tag`, `group` and `update` accept an exact path, a repo name,
a project name or an `org/project` slug. If a name is ambiguous, repoidx lists
the candidates.

### Cleaning up

`repoidx` does not delete anything (see the [roadmap](#roadmap)), but it tells you what
is safe to delete:

```console
$ repoidx list --stale --safe --sort size -n 4
GROUP  NAME          BRANCH      STATE  LAST  FILES  MINE  PATH
oss    langfuse      main        stale  4mo   3463   0     ~/src/oss/langfuse
oss    gobang        main        stale  3y    60     0     ~/src/oss/gobang
oss    lv_demos      release/v7  stale  4y    420    0     ~/src/oss/lv_demos
oss    pydantic-ai   main        stale  14mo  730    0     ~/src/oss/pydantic-ai

$ repoidx list --unsafe -g personal          # before wiping a laptop
$ repoidx dupes                               # same remote cloned twice
```

*Safe* means: clean working tree, no untracked files, no stashes, every
commit on every local branch exists on some remote, the repo has a remote,
and git reported no errors while collecting. Run `repoidx update --fetch`
first if you want "on some remote" to reflect the server instead of your
last fetch.

## What is recorded

| | |
|---|---|
| location | path, root it was found under, bare / worktree / submodule, tooling (plugin managers) |
| remote | all remotes; host, provider (github, gitlab, bitbucket, azure, gitea, sourcehut), namespace (GitLab subgroups kept), project. Credentials stripped. |
| branch | current branch (or detached `@sha`), upstream, default branch (`origin/HEAD`), ahead / behind |
| safety | changed files, untracked files, stashes, commits on no remote |
| time | last local commit, last remote-tracking commit, last fetch, last activity (checkout / add / commit) |
| size | tracked file count (`git ls-files`) and `.git` size (`git count-objects`) |
| commits | per author email, on `HEAD` and on the upstream / default branch |
| identity | effective `user.email` of the repo, expected identity of its group |
| readme | `README*` (first 256 KB), full-text indexed |
| group & tags | see [Groups](#groups-personal-customer-a-customer-b) |

## The state column

```
*N   changed files            ?N  untracked files       sN  stashes
↑N   ahead of upstream        ↓N  behind upstream
!N   commits on any local branch that are on no remote (can be set while ↑ is 0)
local  no remote configured   empty  no commits yet
stale  no activity for stale_days
id!    user.email differs from the group's identity
err    some git calls failed (see `show`); state may be incomplete, never --safe
ok     none of the above
```

`!N` differs from `↑N`: `↑` compares the current branch with its upstream,
while `!` counts commits on *any* local branch that no remote-tracking ref
contains, such as a local-only feature branch you forgot about.

## Commands

| Command | |
|---|---|
| `scan [path...]` | Walk folders (default: configured roots, `~`), index every repo, and remove indexed repos below those folders that no longer exist. `--nested` also indexes repos inside repos, `--force` recollects everything, `-v` lists all errors. |
| `update [repo...]` | Refresh indexed repos without walking the disk; removes repos that are gone. `--fetch` runs `git fetch --all --prune` first (`--fetch-timeout`, default 60s). |
| `list` / `ls` | Table of repos with [filters](#filters). `--sort path\|name\|group\|last\|oldest\|size\|files\|commits\|mine`. |
| `search <words>` | Full-text search, ranked, with snippets. `--paths` prints only paths. |
| `show <repo>` | Everything known about one repo, including top authors, related clones/worktrees and the README. |
| `path <repo>` | Print the path: `cd "$(repoidx path api)"`. |
| `stats` | Totals per group; `--by provider\|host\|namespace\|root`; `-s/--summary` for the overview, `--top N` rows per table. |
| `dupes` | Remotes that are cloned in more than one place (worktrees excluded). |
| `tag <repo> <tags...>` | Free-form tags (`--rm` to remove); filter with `-t`. |
| `group <repo> [name]` | Pin a repo to a group, overriding all rules; `--clear` removes the pin. |
| `config show\|init\|path` | Effective configuration, write a commented starter file, print file locations. |

Global flags: `--json`, `--config <file>`, `--db <file>`.

## Filters

`list`, `search`, `stats` and `dupes` share these filters, and they combine:

| Flag | |
|---|---|
| `-g, --group <glob>` | group, e.g. `-g 'customer*'` |
| `--provider <name>` | github, gitlab, bitbucket, azure, gitea |
| `--host <glob>` | remote host |
| `-t, --tag <tag>` | tagged repos |
| `-p, --path <text>` | path or name contains text |
| `--stale` / `--days N` | stale, optionally with another threshold |
| `--dirty` | changed or untracked files |
| `--ahead` / `--behind` | relative to upstream |
| `--unpushed` | commits on no remote |
| `--no-remote` | no remote at all |
| `--unsafe` / `--safe` | deleting would / would not lose work |
| `--mine` | repos with commits by you |
| `--wrong-identity` | `user.email` differs from the group's identity |
| `-a, --all` / `--tooling` | include / only tooling repos (oh-my-zsh, vim plugins, …) |
| `-n, --limit N` | max rows |

```sh
repoidx list -g acme --dirty --sort last
repoidx list --mine --stale --days 365         # my own projects I abandoned a year ago
repoidx list --provider gitlab --behind
repoidx stats -s -g globex                     # overview for one customer
```

## Groups: personal, customer A, customer B

Each repo gets exactly one group. The first match wins:

1. **Manual pin**: `repoidx group <repo> acme` (`--clear` to remove).
2. **Config rules** (`groups:` in the config file), which match on remote,
   path or SSH alias.
3. **Your git `includeIf` setup.** If you already separate customers in
   `~/.gitconfig`, repoidx reuses that:
   ```ini
   [includeIf "gitdir:~/src/acme/"]
       path = ~/.gitconfig-acme              # → group "acme"
   [includeIf "hasconfig:remote.*.url:git@github.com:globex-corp/**"]
       path = ~/.config/git/globex.inc       # → group "globex"
   ```
   The group name comes from the included file's name. Its `user.email`
   becomes the group's **expected identity**.
4. **Fallback**: `host/org` of the primary remote (e.g. `github.com/alex-dev`),
   or `local` when there is no remote.

`show` prints where a repo's group came from: `(manual)`, `(config)`,
`(includeIf:~/.gitconfig-acme)` or `(remote)`.

Groups are re-evaluated after every `scan`/`update`, so editing the rules
is enough. No rescan is needed.

## Identities and aliases

**"Mine"** is matched against the union of:

- `identities:` from the config file,
- your global `git config user.email`,
- the effective `user.email` of every indexed repo, which picks up per-customer emails set via `includeIf` or a local config.

Add old addresses or GitHub noreply emails (`12345+alex@users.noreply.github.com`)
to `identities:` to count them too.

**Wrong identity**: when a group has an expected identity, `list --wrong-identity`
shows repos whose `user.email` differs. These are repos where you'd commit to
customer A with your personal address.

**SSH host aliases** such as `git@github-work:globex-corp/api.git` (from a
`Host github-work` block in `~/.ssh/config`) are resolved with `ssh -G`, so they
are grouped and de-duplicated like `github.com`. For aliases `ssh -G` cannot
resolve, add them to `hosts:` in the config file. Self-hosted servers are
recognised by name (`gitlab.*`, `github.*`, …); map others with `providers:`.

## Zero config defaults

Without a config file, repoidx uses your git config and sane defaults:

| | |
|---|---|
| roots | `~` |
| skipped directory names (anywhere) | `node_modules`, `bower_components`, `.terraform`, `.terragrunt-cache`, `.venv`, `venv`, `.cache`, `__pycache__`, `.tox`, `.mypy_cache`, `.pytest_cache`, `.gradle`, `Pods`, `DerivedData`, `site-packages`, trash folders |
| skipped paths | `~/Library`, `~/Applications` (macOS), `~/AppData` (Windows), `~/.cache`, `~/.npm`, `~/.pnpm-store`, `~/.yarn`, `~/.cargo`, `~/.rustup`, `~/go/pkg/mod`, `~/.m2`, `~/.gradle`, `~/.terraform.d`, `~/.docker`, `~/.vscode/extensions`, `~/.local/share/virtualenvs`, `~/snap`, … |
| tooling (indexed, hidden unless `--all`) | `~/.oh-my-zsh`, `~/.tmux/plugins`, vim / neovim / emacs plugin dirs, `~/.asdf`, `~/.pyenv`, `~/.nvm`, `~/.rbenv`, `~/.sdkman`, `~/.tfenv`, Homebrew taps, AI agent dirs (`~/.claude/plugins`, `~/.codex`, `~/.cursor`, `~/.gemini`, …) |
| identities | global `user.email` + every repo's effective `user.email` |
| groups | global `includeIf` sections, else `host/org` |
| stale | 60 days |
| concurrency | 16 parallel repos, 60s timeout per git call |

Discovery stops at the first repo in a directory tree, so Terraform module
clones and vendored repos inside a project are not indexed. `scan --nested`
indexes them too.

`repoidx config show` prints the effective configuration.

## Config file

The config file is optional. It lives at `~/.config/repoidx/config.yaml`, honours
`$XDG_CONFIG_HOME`, and is at `%AppData%\repoidx\config.yaml` on Windows.
`repoidx config init` writes a commented starter file.

Lists **extend** the defaults. Set `no_default_skips: true` or
`no_default_tooling: true` to replace them.

```yaml
roots: [~/src, ~/work]
stale_days: 60

identities:                       # extra emails that count as "mine"
  - 12345+alex@users.noreply.github.com
  - alex@old-employer.example

hosts:                            # SSH aliases that ssh -G cannot resolve
  github-globex: github.com
providers:                        # self-hosted servers with unusual names
  code.acme.example: gitlab

skip_dirs: [build, dist]          # names, globs allowed
skip_paths: [~/Downloads, ~/tmp]
tooling_paths: [~/.config/emacs]

groups:
  - name: acme
    identity: alex.doe@acme.example   # enables --wrong-identity
    match:
      - remote: "gitlab.acme.example/**"
      - path: "~/src/acme/**"
  - name: globex
    identity: alex@globex.example
    match:
      - remote: "github.com/globex-corp/**"
      - ssh_alias: github-globex
  - name: personal
    match:
      - remote: "github.com/alex-dev/*"
      - path: "~/src/personal/**"

concurrency: 16
git_timeout_seconds: 60
```

Remote patterns match `host/namespace/project` (lower case, `**` spans
subgroups). Path patterns match the absolute repo path, and a trailing `/` means
"everything below".

The index lives at `~/.local/share/repoidx/index.db`. It honours
`$XDG_DATA_HOME`, and is at `%LocalAppData%\repoidx\index.db` on Windows. Override
it with `--db`, e.g. to keep a separate index per machine or to experiment.

## Scripting and JSON

Every command supports `--json`. A few recipes:

```sh
# open all dirty acme repos in your editor
repoidx list -g acme --dirty --json | jq -r '.[].path' | xargs code

# pull every clean, behind repo
repoidx list --behind --safe --json | jq -r '.[].path' |
  while read -r p; do git -C "$p" pull --ff-only; done

# total .git size per provider
repoidx stats --by provider --json | jq '.[] | {key, gb: (.git_size_kb/1048576)}'

# fzf jump
cd "$(repoidx search --paths "$@" | fzf)"
```

The SQLite database is a stable place to query from, too:

```sh
sqlite3 ~/.local/share/repoidx/index.db \
  "select grp, count(*) from repos where unpushed > 0 group by grp"
```

| Table | |
|---|---|
| `repos` | one row per repo: paths, branch, sync state, dates, sizes, primary remote, group |
| `remotes` | every remote of every repo, parsed |
| `authors` | commit counts per author email (local and remote) |
| `tags` | free-form tags |
| `group_overrides` | manual group pins (keyed by path, survive re-indexing) |
| `roots` | scanned folders and when |
| `search` | FTS5 index over name, slug, group, path, README |

## Safety: what repoidx will never do

- **No writes to your repos.** Git runs with `GIT_OPTIONAL_LOCKS=0`, so even
  `git status` does not refresh the index file. fsmonitor hooks are disabled.
- **No network**, except `update --fetch`, which only updates remote-tracking
  refs. SSH runs with `BatchMode=yes` and git with `GIT_TERMINAL_PROMPT=0`, so it
  never hangs on a password prompt.
- **No lazy fetching** in partial clones (`GIT_NO_LAZY_FETCH=1`).
- **No symlink following** during discovery.
- **No credentials stored**: user:token parts of remote URLs are stripped.
- **Every git call has a timeout**, so one broken repo cannot stall a scan.
- **A repo is dropped from the index only when it is definitely gone.** A
  "permission denied" (for example a macOS privacy-protected folder) keeps it.

## Performance

Rough numbers on a laptop with an SSD:

| | repos | time |
|---|---|---|
| first `scan ~` | ~580 | ~2 min |
| `scan` of a project folder | ~60 | ~7 s |
| `update` (nothing changed) | ~490 | ~30 s |
| `list`, `search`, `stats` | any | instant |

A rescan or update re-reads only cheap data (status, refs) for repos whose
refs did not move. Commit counts, README, file count and `.git` size are
collected again only when something changed, or with `--force`.

The remaining cost is `git status` in very large working trees. See the
stale-index note under [troubleshooting](#troubleshooting).

## Troubleshooting

**"N directories could not be read"**: usually macOS privacy protection
(Desktop, Documents, Downloads, iCloud) or permission-denied folders. Run with
`-v` to list them. Repos already indexed there are kept. To scan these folders,
give your terminal *Full Disk Access* (System Settings → Privacy & Security), or
add them to `skip_paths` to silence the message.

**"repo(s) have stale file timestamps in their index"**: the repo was copied
or restored (a new laptop, backup or sync tool), so every file's timestamps
differ from what git cached. Git then re-reads every file on each `status`,
which can take minutes in big repos. repoidx never writes the index, so fix it
once yourself:

```sh
git -C <repo> update-index -q --refresh     # or just: git status
```

**A repo shows `err`**: one of the git calls failed or timed out. `repoidx
show <repo>` prints the error. Raise `git_timeout_seconds` for very large repos.
These repos are never counted as safe.

**Wrong or missing group**: `repoidx show <repo>` shows the group and its
source; `repoidx config show` shows the rules (including those derived from
`includeIf`). Pin it with `repoidx group <repo> <name>`.

**My commits are not counted**: add the missing email to `identities:`.
`repoidx show <repo>` lists the top author emails.

## How it works

```
             ┌────────────┐   paths    ┌──────────────┐  Info   ┌───────────┐
 roots ────▶ │  discover  │ ─────────▶ │   gitinfo    │ ──────▶ │   store   │
             │ parallel   │            │ N workers,   │         │  SQLite   │
             │ ReadDir,   │            │ git + timeout│         │  + FTS5   │
             │ skip rules │            │ refs-hash →  │         └─────┬─────┘
             └────────────┘            │ light / full │               │
                                       └──────────────┘        ┌──────▼──────┐
                                                               │    group    │
               ┌──────────┐  ssh -G, host map                  │ pin → rules │
               │  remote  │ ───────────────────────────────▶   │ → includeIf │
               │  parser  │                                    │ → host/org  │
               └──────────┘                                    └─────────────┘
```

1. **discover** walks the roots with a bounded pool of `ReadDir` calls. When
   it finds `.git` (directory or file) or a bare repo, it reports the repo and
   stops descending (unless `--nested`).
2. **gitinfo** runs a fixed set of read-only git commands per repo. It hashes
   `HEAD` plus all branch, remote and stash refs. If the hash matches the stored
   one, it stops after the cheap part (status, stashes) and the store keeps the
   expensive data.
3. **remote** parses URLs, resolves SSH aliases (cached `ssh -G`) and maps hosts
   to providers.
4. **store** upserts everything in one transaction per repo through a single
   writer. Repos under the scanned roots that weren't found and no longer exist
   are removed.
5. **group** assigns groups for all repos at the end, so rule changes apply
   without rescanning.

```
cmd/                 cobra commands and table output
internal/discover    parallel directory walk, repo detection
internal/gitinfo     read-only git metadata (shells out to git)
internal/remote      remote URL parsing, SSH alias resolution
internal/config      defaults, YAML config, includeIf → groups
internal/group       group assignment
internal/store       SQLite schema, queries, FTS5 search
internal/indexer     scan / update orchestration
```

## Development

```sh
make test      # go test ./...
make vet
make build     # CGO_ENABLED=0, version from git describe
make dist      # darwin, linux, windows × amd64, arm64
```

Releases are built by [GoReleaser](https://goreleaser.com) when a tag is
pushed:

```sh
git tag v0.2.0 && git push origin v0.2.0
```

The release workflow builds all platforms, publishes the GitHub release and
updates the cask in [jverhoeks/homebrew-tap](https://github.com/jverhoeks/homebrew-tap).
Try it locally with `goreleaser release --snapshot --clean`.

Dependencies: [cobra](https://github.com/spf13/cobra),
[modernc.org/sqlite](https://gitlab.com/cznic/sqlite) (pure Go SQLite with
FTS5), [doublestar](https://github.com/bmatcuk/doublestar) and
[yaml.v3](https://github.com/go-yaml/yaml). Tests create real git repos in
temp folders, so `git` must be installed.

## Roadmap

- **`clean`**, in tiers from safest to most destructive: `git gc`, remove
  ignored build output (`node_modules`, `target/`, `.terraform`), then delete
  clones that are `--safe` and `--stale`. Always `--dry-run` first.
- **Provider API enrichment**: archived or deleted upstream, description,
  stars, open PRs.
- **Fork detection**: `upstream` remotes, same project in another namespace.
- **TUI** for browsing and acting on the index.
