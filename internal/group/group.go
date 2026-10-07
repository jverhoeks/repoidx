// Package group assigns each repo to a group. Precedence:
//
//  1. manual override (`repoidx group <repo> <name>`)
//  2. rules from the config file, in order
//  3. rules derived from the user's git includeIf sections
//  4. fallback: host/namespace of the primary remote, or "local"
package group

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/remote"
	"github.com/jverhoeks/repoidx/internal/store"
)

// Assigner holds the compiled rules.
type Assigner struct {
	Rules []config.GroupRule // config rules first, then includeIf rules
}

// New builds an Assigner from config rules plus includeIf-derived rules.
func New(cfg []config.GroupRule, includeIf []config.GroupRule) *Assigner {
	rules := make([]config.GroupRule, 0, len(cfg)+len(includeIf))
	for _, r := range append(append([]config.GroupRule(nil), cfg...), includeIf...) {
		r.Match = normalize(r.Match)
		rules = append(rules, r)
	}
	return &Assigner{Rules: rules}
}

func normalize(ms []config.Matcher) []config.Matcher {
	out := make([]config.Matcher, len(ms))
	for i, m := range ms {
		if m.Path != "" {
			dir := strings.HasSuffix(m.Path, "/") // "~/work/x/" means everything below
			m.Path = filepath.ToSlash(config.Expand(m.Path))
			if dir {
				m.Path += "/**"
			}
		}
		m.Remote = strings.ToLower(strings.TrimSuffix(m.Remote, ".git"))
		out[i] = m
	}
	return out
}

// Assign computes the group for one repo.
func (a *Assigner) Assign(in store.GroupInput, overrides map[string]string) store.GroupResult {
	res := store.GroupResult{ID: in.ID}
	if g, ok := overrides[in.Path]; ok {
		res.Group, res.Source = g, "manual"
		// still pick up an expected identity from a rule with the same name
		for _, r := range a.Rules {
			if r.Name == g {
				res.Identity = r.Identity
				break
			}
		}
		return res
	}
	p := filepath.ToSlash(in.Path)
	for _, r := range a.Rules {
		if matchRule(r, p, in.Remotes) {
			res.Group, res.Source, res.Identity = r.Name, r.Source, strings.ToLower(r.Identity)
			return res
		}
	}
	res.Group, res.Source = Fallback(in.Remotes), "remote"
	if res.Group == "local" {
		res.Source = "none"
	}
	return res
}

func matchRule(r config.GroupRule, path string, remotes []remote.Remote) bool {
	for _, m := range r.Match {
		if m.Path != "" && globMatch(m.Path, path) {
			return true
		}
		for _, rm := range remotes {
			if m.Remote != "" && globMatch(m.Remote, strings.ToLower(rm.Slug())) {
				return true
			}
			if m.SSHAlias != "" && rm.Alias != "" && globMatch(m.SSHAlias, rm.Alias) {
				return true
			}
		}
	}
	return false
}

// globMatch matches a doublestar glob; a pattern ending in "/**" also
// matches the directory itself.
func globMatch(pattern, s string) bool {
	if ok, _ := doublestar.Match(pattern, s); ok {
		return true
	}
	if base, ok := strings.CutSuffix(pattern, "/**"); ok {
		if ok, _ := doublestar.Match(base, s); ok {
			return true
		}
	}
	return false
}

// Fallback groups by host and top-level namespace of the primary remote
// (origin, else the first), e.g. "github.com/alex-dev".
func Fallback(remotes []remote.Remote) string {
	var prim *remote.Remote
	for i := range remotes {
		if prim == nil || remotes[i].Name == "origin" {
			prim = &remotes[i]
		}
	}
	if prim == nil || prim.Host == "" {
		return "local"
	}
	ns, _, _ := strings.Cut(prim.Namespace, "/")
	if ns == "" {
		return prim.Host
	}
	return prim.Host + "/" + ns
}
