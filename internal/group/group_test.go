package group

import (
	"testing"

	"github.com/jverhoeks/repoidx/internal/config"
	"github.com/jverhoeks/repoidx/internal/remote"
	"github.com/jverhoeks/repoidx/internal/store"
)

func TestAssign(t *testing.T) {
	a := New(
		[]config.GroupRule{
			{Name: "customerA", Source: "config", Identity: "Me@CustA.com", Match: []config.Matcher{
				{Remote: "gitlab.customera.com/**"},
				{SSHAlias: "github-custa"},
			}},
			{Name: "personal", Source: "config", Match: []config.Matcher{{Remote: "github.com/alex-dev/*"}}},
		},
		[]config.GroupRule{
			{Name: "work", Source: "includeIf:~/.gitconfig-work", Match: []config.Matcher{{Path: "/home/me/work/"}}},
		},
	)
	gh := func(alias, ns, proj string) []remote.Remote {
		return []remote.Remote{{Name: "origin", Host: "github.com", Alias: alias, Namespace: ns, Project: proj}}
	}
	cases := []struct {
		name    string
		in      store.GroupInput
		ov      map[string]string
		want    string
		wantSrc string
	}{
		{"remote glob with subgroups", store.GroupInput{Path: "/x", Remotes: []remote.Remote{{Name: "origin", Host: "gitlab.customera.com", Namespace: "a/b", Project: "c"}}}, nil, "customerA", "config"},
		{"ssh alias", store.GroupInput{Path: "/x", Remotes: gh("github-custa", "custa", "infra")}, nil, "customerA", "config"},
		{"personal", store.GroupInput{Path: "/x", Remotes: gh("", "alex-dev", "repoidx")}, nil, "personal", "config"},
		{"includeIf path", store.GroupInput{Path: "/home/me/work/proj"}, nil, "work", "includeIf:~/.gitconfig-work"},
		{"includeIf dir itself", store.GroupInput{Path: "/home/me/work"}, nil, "work", "includeIf:~/.gitconfig-work"},
		{"fallback", store.GroupInput{Path: "/x", Remotes: gh("", "hashicorp", "terraform")}, nil, "github.com/hashicorp", "remote"},
		{"no remote", store.GroupInput{Path: "/x"}, nil, "local", "none"},
		{"override wins", store.GroupInput{Path: "/x", Remotes: gh("", "alex-dev", "r")}, map[string]string{"/x": "customerA"}, "customerA", "manual"},
	}
	for _, c := range cases {
		got := a.Assign(c.in, c.ov)
		if got.Group != c.want || got.Source != c.wantSrc {
			t.Errorf("%s: got %q (%s), want %q (%s)", c.name, got.Group, got.Source, c.want, c.wantSrc)
		}
	}
	if got := a.Assign(store.GroupInput{Path: "/x", Remotes: gh("github-custa", "c", "d")}, nil); got.Identity != "me@custa.com" {
		t.Errorf("identity not carried: %+v", got)
	}
}
