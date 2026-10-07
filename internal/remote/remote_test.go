package remote

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		url                         string
		host, ns, project, provider string
	}{
		{"git@github.com:alex-dev/repoidx.git", "github.com", "alex-dev", "repoidx", "github"},
		{"https://github.com/alex-dev/repoidx", "github.com", "alex-dev", "repoidx", "github"},
		{"https://github.com/alex-dev/repoidx.git/", "github.com", "alex-dev", "repoidx", "github"},
		{"ssh://git@gitlab.example.com:2222/group/sub/sub2/proj.git", "gitlab.example.com", "group/sub/sub2", "proj", "gitlab"},
		{"git@gitlab.com:group/sub/proj.git", "gitlab.com", "group/sub", "proj", "gitlab"},
		{"git@github-work:customera/infra.git", "github-work", "customera", "infra", "github"},
		{"git@custb:team/infra.git", "custb", "team", "infra", "unknown"},
		{"https://dev.azure.com/org/proj/_git/repo", "dev.azure.com", "org/proj", "repo", "azure"},
		{"git@ssh.dev.azure.com:v3/org/proj/repo", "ssh.dev.azure.com", "org/proj", "repo", "azure"},
		{"https://user:token@bitbucket.org/team/thing.git", "bitbucket.org", "team", "thing", "bitbucket"},
		{"file:///srv/git/thing.git", "", "", "thing", "local"},
		{"/srv/git/thing.git", "", "", "thing", "local"},
		{"../other", "", "", "other", "local"},
		{`C:\repos\thing`, "", "", "thing", "local"},
		{"", "", "", "", "local"},
		{"::::", "", "", "::::", "local"}, // junk must not panic
	}
	for _, tt := range tests {
		r := Parse("origin", tt.url)
		if r.Host != tt.host || r.Namespace != tt.ns || r.Project != tt.project || r.Provider != tt.provider {
			t.Errorf("Parse(%q) = host=%q ns=%q project=%q provider=%q; want %q %q %q %q",
				tt.url, r.Host, r.Namespace, r.Project, r.Provider, tt.host, tt.ns, tt.project, tt.provider)
		}
	}
}

func TestResolverAlias(t *testing.T) {
	res := &Resolver{
		Hosts:     map[string]string{"github-work": "github.com"},
		Providers: map[string]string{"git.customer.nl": "gitlab"},
	}
	r := res.Resolve(Parse("origin", "git@github-work:customera/infra.git"))
	if r.Host != "github.com" || r.Alias != "github-work" || r.Provider != "github" {
		t.Errorf("alias not resolved: %+v", r)
	}
	r = res.Resolve(Parse("origin", "git@git.customer.nl:team/x.git"))
	if r.Provider != "gitlab" {
		t.Errorf("provider override not applied: %+v", r)
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:secret@github.com/a/b.git": "https://user@github.com/a/b.git",
		"https://ghp_token@github.com/a/b.git":   "https://github.com/a/b.git",
		"ssh://git@gitlab.com/a/b.git":           "ssh://git@gitlab.com/a/b.git",
		"git@github.com:a/b.git":                 "git@github.com:a/b.git",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}
