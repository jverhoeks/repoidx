// Package config holds repoidx settings. Everything has a sane default so the
// tool works without a config file; the optional YAML file only adds to or
// overrides those defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// GroupRule assigns repos to a named group. A repo matches when any matcher
// matches. Rules are evaluated in order; the first match wins.
type GroupRule struct {
	Name     string    `yaml:"name"`
	Match    []Matcher `yaml:"match"`
	Identity string    `yaml:"identity,omitempty"` // expected commit email
	Source   string    `yaml:"-"`                  // "config" or "includeIf:<file>"
}

// Matcher fields are globs (doublestar syntax, "**" allowed).
type Matcher struct {
	Remote   string `yaml:"remote,omitempty"`    // host/namespace/project, e.g. github.com/customera/**
	Path     string `yaml:"path,omitempty"`      // local path, e.g. ~/work/customerA/**
	SSHAlias string `yaml:"ssh_alias,omitempty"` // SSH host alias, e.g. github-custa
}

// Config is the effective configuration.
type Config struct {
	Roots        []string          `yaml:"roots"`
	StaleDays    int               `yaml:"stale_days"`
	Identities   []string          `yaml:"identities,omitempty"`
	Hosts        map[string]string `yaml:"hosts,omitempty"`     // ssh alias -> hostname
	Providers    map[string]string `yaml:"providers,omitempty"` // hostname -> github|gitlab|...
	SkipDirs     []string          `yaml:"skip_dirs"`           // directory names (globs) skipped anywhere
	SkipPaths    []string          `yaml:"skip_paths"`          // absolute paths skipped
	ToolingPaths []string          `yaml:"tooling_paths"`       // indexed but hidden by default
	Groups       []GroupRule       `yaml:"groups,omitempty"`
	Concurrency  int               `yaml:"concurrency,omitempty"`
	GitTimeout   int               `yaml:"git_timeout_seconds,omitempty"`
	DB           string            `yaml:"db,omitempty"`

	// Set NoDefault* to replace instead of extend the built-in lists.
	NoDefaultSkips   bool `yaml:"no_default_skips,omitempty"`
	NoDefaultTooling bool `yaml:"no_default_tooling,omitempty"`

	// FilePath is where the config was loaded from ("" if none).
	FilePath string `yaml:"-"`
}

// DefaultSkipDirs are directory names that never contain repos worth
// indexing (dependency caches, virtualenvs, terraform module clones, ...).
var DefaultSkipDirs = []string{
	"node_modules", "bower_components", ".terraform", ".terragrunt-cache",
	".venv", "venv", ".cache", "__pycache__", ".tox", ".mypy_cache", ".pytest_cache",
	".gradle", "Pods", "DerivedData", "site-packages", ".Trash", ".Trashes", "$RECYCLE.BIN",
}

// DefaultSkipPaths are big caches/system folders under the home directory.
func DefaultSkipPaths() []string {
	p := []string{
		"~/.cache", "~/.npm", "~/.pnpm-store", "~/.yarn", "~/.cargo", "~/.rustup",
		"~/go/pkg/mod", "~/.m2", "~/.gradle", "~/.terraform.d", "~/.docker",
		"~/.vscode/extensions", "~/.cursor/extensions", "~/.local/share/Trash",
		"~/.Trash", "~/snap", "~/.local/share/virtualenvs",
	}
	switch runtime.GOOS {
	case "darwin":
		p = append(p, "~/Library", "~/Applications")
	case "windows":
		p = append(p, "~/AppData", "~/scoop")
	}
	return p
}

// DefaultToolingPaths hold plugin managers' clones: indexed, but hidden from
// list/search unless --all or --tooling is given.
var DefaultToolingPaths = []string{
	"~/.oh-my-zsh", "~/.zprezto", "~/.tmux/plugins", "~/.vim/plugged", "~/.vim/bundle",
	"~/.vim/pack", "~/.local/share/nvim", "~/.config/nvim/pack", "~/.emacs.d",
	"~/.asdf", "~/.pyenv", "~/.nvm", "~/.rbenv", "~/.sdkman", "~/.tfenv",
	"~/.fzf", "~/.zinit", "~/.antigen", "~/.config/zsh/plugins",
	"/opt/homebrew/Library/Taps", "/usr/local/Homebrew",
	// AI agent plugin / marketplace caches
	"~/.claude/plugins", "~/.codex", "~/.grok", "~/.cursor", "~/.gemini",
	"~/.config/opencode", "~/.continue",
}

// Default returns the zero-config configuration.
func Default() *Config {
	return &Config{
		Roots:        []string{"~"},
		StaleDays:    60,
		Hosts:        map[string]string{},
		Providers:    map[string]string{},
		SkipDirs:     append([]string(nil), DefaultSkipDirs...),
		SkipPaths:    DefaultSkipPaths(),
		ToolingPaths: append([]string(nil), DefaultToolingPaths...),
		Concurrency:  min(2*runtime.NumCPU(), 16),
		GitTimeout:   60,
	}
}

// DefaultPath returns the config file location:
// $XDG_CONFIG_HOME/repoidx/config.yaml, ~/.config/repoidx/config.yaml, or
// %AppData%\repoidx\config.yaml on Windows.
func DefaultPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "repoidx", "config.yaml")
	}
	if runtime.GOOS == "windows" {
		if d, err := os.UserConfigDir(); err == nil {
			return filepath.Join(d, "repoidx", "config.yaml")
		}
	}
	return filepath.Join(Home(), ".config", "repoidx", "config.yaml")
}

// DefaultDB returns the database location:
// $XDG_DATA_HOME/repoidx/index.db, ~/.local/share/repoidx/index.db, or
// %LocalAppData%\repoidx\index.db on Windows.
func DefaultDB() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "repoidx", "index.db")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "repoidx", "index.db")
		}
	}
	return filepath.Join(Home(), ".local", "share", "repoidx", "index.db")
}

// Load reads the config file at path (or the default path when empty) on top
// of the defaults. A missing file is not an error.
func Load(path string) (*Config, error) {
	cfg := Default()
	explicit := path != ""
	if !explicit {
		path = DefaultPath()
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return cfg.finish(), nil
	}
	if err != nil {
		return nil, err
	}
	var file Config
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.merge(&file)
	cfg.FilePath = path
	return cfg.finish(), nil
}

func (c *Config) merge(f *Config) {
	if len(f.Roots) > 0 {
		c.Roots = f.Roots
	}
	if f.StaleDays > 0 {
		c.StaleDays = f.StaleDays
	}
	if f.Concurrency > 0 {
		c.Concurrency = f.Concurrency
	}
	if f.GitTimeout > 0 {
		c.GitTimeout = f.GitTimeout
	}
	if f.DB != "" {
		c.DB = f.DB
	}
	c.Identities = append(c.Identities, f.Identities...)
	for k, v := range f.Hosts {
		c.Hosts[k] = v
	}
	for k, v := range f.Providers {
		c.Providers[k] = v
	}
	if f.NoDefaultSkips {
		c.SkipDirs, c.SkipPaths = nil, nil
		c.NoDefaultSkips = true
	}
	if f.NoDefaultTooling {
		c.ToolingPaths = nil
		c.NoDefaultTooling = true
	}
	c.SkipDirs = append(c.SkipDirs, f.SkipDirs...)
	c.SkipPaths = append(c.SkipPaths, f.SkipPaths...)
	c.ToolingPaths = append(c.ToolingPaths, f.ToolingPaths...)
	for _, g := range f.Groups {
		g.Source = "config"
		c.Groups = append(c.Groups, g)
	}
}

func (c *Config) finish() *Config {
	if c.DB == "" {
		c.DB = DefaultDB()
	}
	c.DB = Expand(c.DB)
	for i := range c.Identities {
		c.Identities[i] = strings.ToLower(strings.TrimSpace(c.Identities[i]))
	}
	return c
}

// Home returns the user's home directory.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// Expand replaces a leading ~ with the home directory and cleans the path.
func Expand(p string) string {
	if p == "~" {
		return Home()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(Home(), p[2:])
	}
	return filepath.Clean(p)
}

// Shorten replaces the home directory prefix with ~ for display.
func Shorten(p string) string {
	h := Home()
	if p == h {
		return "~"
	}
	if strings.HasPrefix(p, h+string(filepath.Separator)) {
		return "~" + p[len(h):]
	}
	return p
}

// YAML renders the effective config.
func (c *Config) YAML() string {
	out, _ := yaml.Marshal(c)
	return string(out)
}
