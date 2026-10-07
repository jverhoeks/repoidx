package config

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitGlobal returns a value from the user's global git config ("" if unset).
func GitGlobal(key string) string {
	out, err := gitConfig("--global", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// IncludeIfGroups turns the user's global git `includeIf` sections into group
// rules, so an existing per-customer git setup gives grouping for free:
//
//	[includeIf "gitdir:~/work/customerA/"]
//	    path = ~/.gitconfig-customerA
//
// becomes group "customerA" matching ~/work/customerA/** with the email from
// ~/.gitconfig-customerA as expected identity. hasconfig:remote.*.url:
// conditions become remote-URL matchers.
func IncludeIfGroups() []GroupRule {
	out, err := gitConfig("--global", "--get-regexp", `^includeif\..*\.path$`)
	if err != nil {
		return nil
	}
	return parseIncludeIf(out, func(file string) string {
		v, _ := gitConfig("-f", file, "--get", "user.email")
		return strings.ToLower(strings.TrimSpace(v))
	})
}

func parseIncludeIf(out string, emailOf func(string) string) []GroupRule {
	var rules []GroupRule
	byName := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		key, file, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		// key = includeif.<condition>.path (git lowercases only the section/key)
		cond := strings.TrimSuffix(strings.TrimPrefix(key, "includeif."), ".path")
		file = Expand(strings.TrimSpace(file))

		var m Matcher
		switch {
		case strings.HasPrefix(cond, "gitdir:"), strings.HasPrefix(cond, "gitdir/i:"):
			_, pat, _ := strings.Cut(cond, ":")
			m.Path = gitdirPattern(pat)
		case strings.HasPrefix(cond, "hasconfig:remote.*.url:"):
			m.Remote = urlPatternToSlug(strings.TrimPrefix(cond, "hasconfig:remote.*.url:"))
		default:
			continue // onbranch: etc. don't describe a repo set
		}
		if m.Path == "" && m.Remote == "" {
			continue
		}
		name := groupNameFromFile(file)
		if name == "" && m.Path != "" {
			name = lastLiteralSegment(m.Path)
		}
		if name == "" {
			continue
		}
		if i, ok := byName[name]; ok {
			rules[i].Match = append(rules[i].Match, m)
			continue
		}
		byName[name] = len(rules)
		rules = append(rules, GroupRule{
			Name:     name,
			Match:    []Matcher{m},
			Identity: emailOf(file),
			Source:   "includeIf:" + Shorten(file),
		})
	}
	return rules
}

// gitdirPattern converts a git includeIf gitdir pattern into a path glob that
// matches the repo's working directory.
func gitdirPattern(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		trailing := strings.HasSuffix(p, "/")
		p = filepath.ToSlash(Expand(p))
		if trailing {
			p += "/"
		}
	} else if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "./") && !(len(p) > 1 && p[1] == ':') {
		p = "**/" + p
	}
	if strings.HasSuffix(p, "/") {
		return p + "**"
	}
	// "gitdir:~/x/repo/.git" style: match the repo dir itself
	return strings.TrimSuffix(p, "/.git")
}

// urlPatternToSlug turns "https://github.com/customera/**" or
// "git@github.com:customera/**" into "github.com/customera/**".
func urlPatternToSlug(p string) string {
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+3:]
		if at := strings.Index(p, "@"); at >= 0 && at < strings.Index(p+"/", "/") {
			p = p[at+1:]
		}
		// drop :port
		if c := strings.Index(p, ":"); c >= 0 && c < strings.Index(p+"/", "/") {
			rest := p[c+1:]
			if s := strings.Index(rest, "/"); s >= 0 {
				p = p[:c] + rest[s:]
			}
		}
	} else if c := strings.Index(p, ":"); c >= 0 {
		host := p[:c]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		p = host + "/" + p[c+1:]
	}
	return strings.TrimSuffix(p, ".git")
}

func groupNameFromFile(file string) string {
	b := filepath.Base(file)
	b = strings.TrimPrefix(b, ".")
	b = strings.TrimSuffix(b, filepath.Ext(b))
	for _, pre := range []string{"gitconfig", "git"} {
		b = strings.TrimPrefix(b, pre)
	}
	b = strings.Trim(b, "-_.")
	return b
}

func lastLiteralSegment(glob string) string {
	segs := strings.Split(glob, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if s := segs[i]; s != "" && !strings.ContainsAny(s, "*?[") {
			return s
		}
	}
	return ""
}

func gitConfig(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"config"}, args...)...).Output()
	return string(out), err
}
