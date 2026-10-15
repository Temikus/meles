// Package config loads and validates meles.yml from the control repo.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Mode says what meles does with a finding.
type Mode string

// Modes.
const (
	Observe Mode = "observe"
	PR      Mode = "pr"
	Enforce Mode = "enforce"
)

// OnClose says what a human closing a meles PR means for a finding.
type OnClose string

// OnClose values.
const (
	Accept OnClose = "accept"
	Reopen OnClose = "reopen"
)

// Config is a parsed meles.yml.
type Config struct {
	Version   int                 `yaml:"version"`
	Defaults  Defaults            `yaml:"defaults"`
	Selectors map[string]Selector `yaml:"selectors"`
	Policies  []PolicySpec        `yaml:"policies"`

	// File is the config file name, used in error positions.
	File string `yaml:"-"`
	line int
}

// Defaults apply to every policy that does not override them.
type Defaults struct {
	Mode Mode       `yaml:"mode"`
	PR   PRDefaults `yaml:"pr"`
}

// PRDefaults configure the housekeeping PR.
type PRDefaults struct {
	Branch    string   `yaml:"branch"`
	Labels    []string `yaml:"labels"`
	MaxPerRun int      `yaml:"maxPerRun"`
	OnClose   OnClose  `yaml:"onClose"`
}

// Selector picks repos. Fields combine with AND, list items with OR.
type Selector struct {
	Archived   *bool    `yaml:"archived"`
	Fork       *bool    `yaml:"fork"`
	Visibility string   `yaml:"visibility"`
	Files      []string `yaml:"files"`
	Topics     []string `yaml:"topics"`
	Language   string   `yaml:"language"`
	Name       string   `yaml:"name"`

	line int
}

// PolicySpec is one entry under policies.
type PolicySpec struct {
	ID      string    `yaml:"id"`
	On      string    `yaml:"on"`
	Mode    Mode      `yaml:"mode"`
	OnClose OnClose   `yaml:"onClose"`
	With    yaml.Node `yaml:"with"`

	Line int `yaml:"-"`
}

// UnmarshalYAML records the line of the policy entry.
func (p *PolicySpec) UnmarshalYAML(n *yaml.Node) error {
	type raw PolicySpec
	if err := DecodeStrict(n, (*raw)(p)); err != nil {
		return err
	}
	p.Line = n.Line
	return nil
}

// UnmarshalYAML records the line of the selector.
func (s *Selector) UnmarshalYAML(n *yaml.Node) error {
	type raw Selector
	if err := DecodeStrict(n, (*raw)(s)); err != nil {
		return err
	}
	s.line = n.Line
	return nil
}

// Resolved returns the spec with defaults filled in.
func (p PolicySpec) Resolved(d Defaults) PolicySpec {
	if p.Mode == "" {
		p.Mode = d.Mode
	}
	if p.OnClose == "" {
		p.OnClose = d.PR.OnClose
	}
	return p
}

// Instance names this policy entry: the kind, or kind[selector] when the kind repeats.
func (p PolicySpec) Instance(all []PolicySpec) string {
	n := 0
	for _, q := range all {
		if q.ID == p.ID {
			n++
		}
	}
	if n > 1 {
		return fmt.Sprintf("%s[%s]", p.ID, p.On)
	}
	return p.ID
}

// Load parses the config at path. The returned FS is rooted at the config's
// directory, which is the control repo; template paths resolve against it.
func Load(path string) (*Config, fs.FS, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	name := filepath.Base(path)
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	cfg := &Config{File: name}
	if len(doc.Content) > 0 {
		if err := DecodeStrict(doc.Content[0], cfg); err != nil {
			return nil, nil, prefixErr(name, err)
		}
		cfg.line = doc.Content[0].Line
	}
	applyDefaults(cfg)
	return cfg, os.DirFS(filepath.Dir(path)), nil
}

func applyDefaults(c *Config) {
	if c.Defaults.Mode == "" {
		c.Defaults.Mode = PR
	}
	if c.Defaults.PR.Branch == "" {
		c.Defaults.PR.Branch = "meles/housekeeping"
	}
	if c.Defaults.PR.OnClose == "" {
		c.Defaults.PR.OnClose = Accept
	}
	if c.Defaults.PR.MaxPerRun == 0 {
		c.Defaults.PR.MaxPerRun = 20
	}
}

// Validate checks structure that does not depend on policy kinds.
func (c *Config) Validate() error {
	var errs Errors
	add := func(line int, format string, a ...any) {
		errs = append(errs, Error{File: c.File, Line: line, Msg: fmt.Sprintf(format, a...)})
	}
	if c.Version != 1 {
		add(c.line, "unsupported version %d (want 1)", c.Version)
	}
	if !c.Defaults.Mode.valid() {
		add(c.line, "defaults: invalid mode %q", c.Defaults.Mode)
	}
	if !c.Defaults.PR.OnClose.Valid() {
		add(c.line, "defaults: invalid onClose %q", c.Defaults.PR.OnClose)
	}
	for name, s := range c.Selectors {
		switch s.Visibility {
		case "", "public", "private", "internal":
		default:
			add(s.line, "selector %s: invalid visibility %q", name, s.Visibility)
		}
		if s.Name != "" {
			if _, err := filepath.Match(s.Name, ""); err != nil {
				add(s.line, "selector %s: invalid name glob %q", name, s.Name)
			}
		}
	}
	seen := map[[2]string]int{}
	for _, p := range c.Policies {
		if p.ID == "" {
			add(p.Line, "policy has no id")
			continue
		}
		if p.On == "" {
			add(p.Line, "policy %s: missing on", p.ID)
		} else if _, ok := c.Selectors[p.On]; !ok {
			add(p.Line, "policy %s: unknown selector %q", p.ID, p.On)
		}
		if p.Mode != "" && !p.Mode.valid() {
			add(p.Line, "policy %s: invalid mode %q", p.ID, p.Mode)
		}
		if p.OnClose != "" && !p.OnClose.Valid() {
			add(p.Line, "policy %s: invalid onClose %q", p.ID, p.OnClose)
		}
		k := [2]string{p.ID, p.On}
		if first, dup := seen[k]; dup {
			add(p.Line, "policy %s: duplicate of line %d (same kind and selector)", p.ID, first)
		} else {
			seen[k] = p.Line
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func (m Mode) valid() bool { return m == Observe || m == PR || m == Enforce }

// Valid reports whether o is accept or reopen.
func (o OnClose) Valid() bool { return o == Accept || o == Reopen }

// Error is a config problem at a position.
type Error struct {
	File string
	Line int
	Msg  string
}

func (e Error) Error() string { return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg) }

// Errors collects every problem found, so one run reports them all.
type Errors []Error

func (es Errors) Error() string {
	s := ""
	for i, e := range es {
		if i > 0 {
			s += "\n"
		}
		s += e.Error()
	}
	return s
}

func prefixErr(file string, err error) error {
	var pe *posError
	if errors.As(err, &pe) {
		return Error{File: file, Line: pe.line, Msg: pe.msg}
	}
	return fmt.Errorf("%s: %w", file, err)
}
