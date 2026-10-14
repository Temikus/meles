// Package governance implements the governance.files policy.
package governance

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
)

// Kind is the policy id.
const Kind = "governance.files"

// GitHub reads these from the root, .github/ or docs/, preferring .github/.
var communityDirs = []string{"", ".github", "docs"}

var communityFiles = map[string]bool{
	"README": true, "SECURITY": true, "CODE_OF_CONDUCT": true, "CONTRIBUTING": true,
	"SUPPORT": true, "CODEOWNERS": true, "GOVERNANCE": true,
}

var badgePatterns = map[string]*regexp.Regexp{
	"ci":       regexp.MustCompile(`/actions/workflows/[^/\s)]+/badge\.svg`),
	"license":  regexp.MustCompile(`img\.shields\.io/[^\s)]*licen[cs]e`),
	"coverage": regexp.MustCompile(`codecov\.io/[^\s)]*(badge|graph)|coveralls\.io/repos/[^\s)]*badge`),
}

type fileSpec struct {
	Template string   `yaml:"template"`
	Path     string   `yaml:"path"`
	Badges   []string `yaml:"badges"`
}

type entry struct {
	name string
	spec fileSpec
	tmpl *template.Template
}

type files struct{ entries []entry }

// TemplateData is what templates can reference. Nothing time-dependent, so
// renders are stable across runs.
type TemplateData struct {
	Owner, Name, FullName, DefaultBranch string
}

// New builds the policy. Keys are file names, in config order.
func New(n *yaml.Node, ctl fs.FS) (policy.Policy, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: with: must be a map of file names", n.Line)
	}
	p := &files{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		e := entry{name: n.Content[i].Value}
		if err := config.DecodeStrict(n.Content[i+1], &e.spec); err != nil {
			return nil, fmt.Errorf("%s: %w", e.name, err)
		}
		for _, b := range e.spec.Badges {
			if badgePatterns[b] == nil {
				return nil, fmt.Errorf("line %d: %s: unknown badge %q (want ci, license or coverage)", n.Content[i].Line, e.name, b)
			}
		}
		if e.spec.Template != "" {
			t, err := loadTemplate(ctl, e.spec.Template)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", n.Content[i].Line, e.name, err)
			}
			e.tmpl = t
		}
		p.entries = append(p.entries, e)
	}
	return p, nil
}

func loadTemplate(ctl fs.FS, name string) (*template.Template, error) {
	b, err := fs.ReadFile(ctl, name)
	if err != nil {
		return nil, err
	}
	t, err := template.New(name).Option("missingkey=error").Parse(string(b))
	if err != nil {
		return nil, err
	}
	// Field errors only surface on execute; fail now, not mid-plan.
	if err := t.Execute(&bytes.Buffer{}, TemplateData{}); err != nil {
		return nil, err
	}
	return t, nil
}

func (*files) Kind() string { return Kind }

func (p *files) Needs() facts.Needs {
	var n facts.Needs
	for _, e := range p.entries {
		if len(e.spec.Badges) > 0 {
			n.Paths = append(n.Paths, contentPaths(e.name)...)
		}
	}
	return n
}

func contentPaths(name string) []string {
	if !communityFiles[stem(name)] {
		return []string{name}
	}
	out := make([]string, 0, len(communityDirs))
	for _, d := range communityDirs {
		out = append(out, path.Join(d, name))
	}
	return out
}

func (p *files) Evaluate(r *facts.Repo) []policy.Finding {
	var out []policy.Finding
	for _, e := range p.entries {
		subject := "file:" + e.name
		found, ok := locate(r, e)
		if !ok {
			f := policy.Finding{Kind: Kind, Subject: subject, Message: e.name + " is missing"}
			if e.tmpl != nil {
				f.Fix = policy.FixCreate
			} else {
				f.Message += " (no template to create it from)"
			}
			out = append(out, f)
			continue
		}
		if len(e.spec.Badges) == 0 {
			continue
		}
		content, loaded := r.Files[found]
		for _, b := range e.spec.Badges {
			f := policy.Finding{Kind: Kind, Subject: subject + "#badge:" + b}
			switch {
			case !loaded:
				f.Message = fmt.Sprintf("cannot check %s badge: %s content not loaded", b, found)
			case !badgePatterns[b].Match(content):
				f.Message = fmt.Sprintf("%s has no %s badge", e.name, b)
			default:
				continue
			}
			out = append(out, f)
		}
	}
	return out
}

// locate finds the file wherever GitHub would read it from.
func locate(r *facts.Repo, e entry) (string, bool) {
	s := stem(e.name)
	switch {
	case s == "LICENSE":
		for p := range r.Tree {
			if !strings.Contains(p, "/") && isLicense(p) {
				return p, true
			}
		}
	case communityFiles[s]:
		if p, ok := r.Find(contentPaths(e.name)...); ok {
			return p, true
		}
		for p := range r.Tree {
			dir, base := path.Split(p)
			if isCommunityDir(strings.TrimSuffix(dir, "/")) && stem(base) == s {
				return p, true
			}
		}
	default:
		return r.Find(target(e))
	}
	return "", false
}

func isLicense(name string) bool {
	u := strings.ToUpper(name)
	return strings.HasPrefix(u, "LICENSE") || strings.HasPrefix(u, "LICENCE") || strings.HasPrefix(u, "COPYING")
}

func isCommunityDir(d string) bool {
	for _, c := range communityDirs {
		if d == c {
			return true
		}
	}
	return false
}

// stem upper-cases the name up to its first dot: "Security.md" -> "SECURITY".
func stem(name string) string {
	s, _, _ := strings.Cut(name, ".")
	return strings.ToUpper(s)
}

func target(e entry) string {
	if e.spec.Path != "" {
		return e.spec.Path
	}
	return e.name
}

func (p *files) Remediate(r *facts.Repo, f policy.Finding) (policy.Change, bool) {
	if f.Fix != policy.FixCreate {
		return nil, false
	}
	for _, e := range p.entries {
		if "file:"+e.name != f.Subject || e.tmpl == nil {
			continue
		}
		var buf bytes.Buffer
		data := TemplateData{Owner: r.Owner, Name: r.Name, FullName: r.FullName(), DefaultBranch: r.DefaultBranch}
		if err := e.tmpl.Execute(&buf, data); err != nil {
			return nil, false // unreachable: New executed the template once already
		}
		return policy.FileChange{Path: target(e), Content: buf.Bytes()}, true
	}
	return nil, false
}
