package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseIncludeIf(t *testing.T) {
	home := filepath.ToSlash(Home())
	out := `includeif.gitdir:~/work/customerA/.path ~/.gitconfig-customerA
includeif.gitdir/i:~/work/customerA-legacy/.path ~/.gitconfig-customerA
includeif.hasconfig:remote.*.url:git@github.com:customerb/**.path ~/.config/git/customerb.inc
includeif.onbranch:main.path ~/.gitconfig-main
includeif.gitdir:oss/.path ~/.gitconfig
`
	rules := parseIncludeIf(out, func(f string) string { return filepath.Base(f) + "@x" })
	if len(rules) != 3 {
		t.Fatalf("got %d rules: %+v", len(rules), rules)
	}
	a := rules[0]
	if a.Name != "customerA" || len(a.Match) != 2 || a.Match[0].Path != home+"/work/customerA/**" || a.Identity != ".gitconfig-customerA@x" {
		t.Errorf("customerA rule wrong: %+v", a)
	}
	b := rules[1]
	if b.Name != "customerb" || b.Match[0].Remote != "github.com/customerb/**" {
		t.Errorf("customerb rule wrong: %+v", b)
	}
	// ~/.gitconfig yields no name from the file; fall back to the pattern
	if rules[2].Name != "oss" || rules[2].Match[0].Path != "**/oss/**" {
		t.Errorf("oss rule wrong: %+v", rules[2])
	}
}

func TestURLPatternToSlug(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/customera/**":           "github.com/customera/**",
		"https://user@gitlab.example.com:8443/g/**": "gitlab.example.com/g/**",
		"git@github.com:customerb/**":               "github.com/customerb/**",
	} {
		if got := urlPatternToSlug(in); got != want {
			t.Errorf("urlPatternToSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadMergesWithDefaults(t *testing.T) {
	f := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(f, []byte(`
roots: [~/src]
stale_days: 30
identities: [Me@Example.com]
skip_dirs: [build]
groups:
  - name: customerA
    match: [{remote: "github.com/customera/**"}]
`), 0o644)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.StaleDays != 30 || len(c.Roots) != 1 || c.Identities[0] != "me@example.com" {
		t.Errorf("scalar merge wrong: %+v", c)
	}
	if len(c.SkipDirs) != len(DefaultSkipDirs)+1 {
		t.Errorf("skip_dirs should extend defaults, got %v", c.SkipDirs)
	}
	if len(c.Groups) != 1 || c.Groups[0].Source != "config" {
		t.Errorf("groups wrong: %+v", c.Groups)
	}
}

func TestLoadMissingDefaultIsOK(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load("")
	if err != nil || c.FilePath != "" || c.StaleDays != 60 {
		t.Fatalf("zero-config load failed: %v %+v", err, c)
	}
}
