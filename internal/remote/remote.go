// Package remote parses git remote URLs into host / namespace / project and
// resolves SSH host aliases (e.g. "github-work") back to their real hostname.
package remote

import (
	"bufio"
	"bytes"
	"context"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Remote is a parsed git remote URL.
type Remote struct {
	Name      string // remote name, e.g. "origin"
	URL       string // raw URL as git reports it
	Host      string // resolved hostname, e.g. "github.com"
	Alias     string // original host if it was an SSH alias, e.g. "github-work"
	Provider  string // github, gitlab, bitbucket, azure, gitea, local, unknown
	Namespace string // org/user, may contain subgroups for GitLab ("group/sub")
	Project   string // repository name without .git
}

// Slug returns "host/namespace/project".
func (r Remote) Slug() string {
	parts := make([]string, 0, 3)
	for _, p := range []string{r.Host, r.Namespace, r.Project} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// Parse splits a remote URL. It never fails: unknown shapes end up with
// Provider "unknown" and as much information as could be extracted.
func Parse(name, raw string) Remote {
	r := Remote{Name: name, URL: Redact(raw), Provider: "unknown"}
	s := strings.TrimSpace(raw)
	var host, p string

	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return r
		}
		if u.Scheme == "file" {
			r.Provider = "local"
			p = u.Path
			r.Project = projectName(p)
			return r
		}
		host = u.Hostname()
		p = u.Path
	case isSCP(s):
		// [user@]host:path
		i := strings.Index(s, ":")
		host = s[:i]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		p = s[i+1:]
	default:
		// local path
		r.Provider = "local"
		r.Project = projectName(s)
		return r
	}

	r.Host = strings.ToLower(host)
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimSuffix(p, "/")
	segs := splitNonEmpty(p, "/")

	// Azure DevOps: ssh v3/org/project/repo, https org/project/_git/repo
	if r.Host == "ssh.dev.azure.com" && len(segs) > 0 && segs[0] == "v3" {
		segs = segs[1:]
	}
	segs = removeSeg(segs, "_git")
	// GitLab/others served under /scm or similar prefixes are left as-is.

	if len(segs) > 0 {
		r.Project = segs[len(segs)-1]
		r.Namespace = strings.Join(segs[:len(segs)-1], "/")
	}
	r.Provider = ProviderForHost(r.Host)
	return r
}

// Redact removes credentials from URLs like https://user:token@host/x so
// they never end up in the index.
func Redact(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, hasPw := u.User.Password(); hasPw {
		u.User = url.User(u.User.Username())
	} else if u.Scheme == "http" || u.Scheme == "https" {
		u.User = nil // a bare token as username is a credential too
	}
	return u.String()
}

// isSCP reports whether s is scp-like syntax: no "://" and the first ':'
// comes before the first '/'.
func isSCP(s string) bool {
	c := strings.Index(s, ":")
	if c <= 0 {
		return false
	}
	// Windows drive letter like C:\foo or C:/foo
	if c == 1 && len(s) > 2 && (s[2] == '\\' || s[2] == '/') {
		return false
	}
	sl := strings.Index(s, "/")
	return sl == -1 || c < sl
}

func projectName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(p, ".git")
}

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, x := range strings.Split(s, sep) {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func removeSeg(segs []string, drop string) []string {
	out := segs[:0:0]
	for _, s := range segs {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// ProviderForHost guesses the hosting provider from a hostname.
func ProviderForHost(host string) string {
	h := strings.ToLower(host)
	switch {
	case h == "":
		return "unknown"
	case strings.Contains(h, "github"):
		return "github"
	case strings.Contains(h, "gitlab"):
		return "gitlab"
	case strings.Contains(h, "bitbucket"):
		return "bitbucket"
	case strings.HasSuffix(h, "dev.azure.com"), strings.HasSuffix(h, "visualstudio.com"):
		return "azure"
	case strings.Contains(h, "codeberg"), strings.Contains(h, "gitea"), strings.Contains(h, "forgejo"):
		return "gitea"
	case strings.Contains(h, "sourcehut") || strings.HasSuffix(h, "sr.ht"):
		return "sourcehut"
	}
	return "unknown"
}

// Resolver maps SSH host aliases to real hostnames using an explicit map
// first and `ssh -G` as a fallback. Results are cached; safe for concurrent use.
type Resolver struct {
	Hosts     map[string]string // alias -> hostname (from config)
	Providers map[string]string // hostname -> provider (from config)
	UseSSH    bool

	mu    sync.Mutex
	cache map[string]string
}

// knownHosts never need an ssh -G lookup.
var knownHosts = map[string]bool{
	"github.com": true, "gitlab.com": true, "bitbucket.org": true,
	"ssh.dev.azure.com": true, "dev.azure.com": true, "codeberg.org": true,
}

// Resolve rewrites r.Host when it is an alias and fills in Provider.
func (res *Resolver) Resolve(r Remote) Remote {
	if r.Host == "" {
		return r
	}
	if real := res.lookup(r.Host); real != "" && real != r.Host {
		r.Alias = r.Host
		r.Host = real
	}
	r.Provider = ProviderForHost(r.Host)
	if p, ok := res.Providers[r.Host]; ok && p != "" {
		r.Provider = p
	}
	return r
}

func (res *Resolver) lookup(host string) string {
	if h, ok := res.Hosts[host]; ok {
		return strings.ToLower(h)
	}
	if knownHosts[host] || !res.UseSSH {
		return host
	}
	res.mu.Lock()
	defer res.mu.Unlock()
	if res.cache == nil {
		res.cache = map[string]string{}
	}
	if h, ok := res.cache[host]; ok {
		return h
	}
	h := sshHostname(host)
	if h == "" {
		h = host
	}
	res.cache[host] = h
	return h
}

// sshHostname asks ssh to evaluate its config for alias (no network access).
func sshHostname(alias string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-G", alias).Output()
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "hostname ") {
			return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "hostname ")))
		}
	}
	return ""
}
