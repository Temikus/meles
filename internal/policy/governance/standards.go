package governance

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
)

// StandardsKind is the policy id for GitHub's community standards.
const StandardsKind = "governance.standards"

type standard struct {
	missing string // finding message
	file    string // created here unless create_at says otherwise
	met     func(facts.Standards) bool
}

// GitHub decides whether each is met, so a file in the wrong place or with an
// unusual name counts exactly when GitHub shows it.
var standards = map[string]standard{
	"license":         {"no license", "LICENSE", func(s facts.Standards) bool { return s.License }},
	"security_policy": {"no security policy", "SECURITY.md", func(s facts.Standards) bool { return s.SecurityPolicy }},
	"code_of_conduct": {"no code of conduct", "CODE_OF_CONDUCT.md", func(s facts.Standards) bool { return s.CodeOfConduct }},
	"contributing":    {"no contributing guidelines", "CONTRIBUTING.md", func(s facts.Standards) bool { return s.Contributing }},
	"codeowners":      {"no CODEOWNERS file", "CODEOWNERS", func(s facts.Standards) bool { return s.Codeowners }},
	"readme":          {"no README", "README.md", func(s facts.Standards) bool { return s.Readme != "" }},
}

var badgePatterns = map[string]*regexp.Regexp{
	"ci":       regexp.MustCompile(`/actions/workflows/[^/\s)]+/badge\.svg`),
	"license":  regexp.MustCompile(`img\.shields\.io/[^\s)]*licen[cs]e`),
	"coverage": regexp.MustCompile(`codecov\.io/[^\s)]*(badge|graph)|coveralls\.io/repos/[^\s)]*badge`),
}

type standardEntry struct {
	name     string
	standard standard
	createAt string
	badges   []string
	tmpl     *template.Template
	onClose  config.OnClose
}

type standardsPolicy struct{ entries []standardEntry }

// NewStandards builds governance.standards. Keys name standards, in config
// order.
func NewStandards(n *yaml.Node, ctl fs.FS) (policy.Policy, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: with: must be a map of standards", n.Line)
	}
	p := &standardsPolicy{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		st, ok := standards[key.Value]
		if !ok {
			return nil, fmt.Errorf("line %d: unknown standard %q (want %s); use governance.files for exact paths",
				key.Line, key.Value, strings.Join(standardNames(), ", "))
		}
		var spec struct {
			Template string         `yaml:"template"`
			CreateAt string         `yaml:"create_at"`
			Badges   []string       `yaml:"badges"`
			OnClose  config.OnClose `yaml:"on_close"`
		}
		if err := config.DecodeStrict(n.Content[i+1], &spec); err != nil {
			return nil, fmt.Errorf("%s: %w", key.Value, err)
		}
		if err := checkOnClose(spec.OnClose); err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", key.Line, key.Value, err)
		}
		if len(spec.Badges) > 0 && key.Value != "readme" {
			return nil, fmt.Errorf("line %d: %s: badges apply only to readme", key.Line, key.Value)
		}
		for _, b := range spec.Badges {
			if badgePatterns[b] == nil {
				return nil, fmt.Errorf("line %d: readme: unknown badge %q (want ci, license or coverage)", key.Line, b)
			}
		}
		if spec.CreateAt != "" && !fs.ValidPath(spec.CreateAt) {
			return nil, fmt.Errorf("line %d: %s: create_at %q is not a relative repo path", key.Line, key.Value, spec.CreateAt)
		}
		e := standardEntry{name: key.Value, standard: st, createAt: spec.CreateAt, badges: spec.Badges, onClose: spec.OnClose}
		if e.createAt == "" {
			e.createAt = st.file
		}
		if spec.Template != "" {
			t, err := loadTemplate(ctl, spec.Template)
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", key.Line, key.Value, err)
			}
			e.tmpl = t
		}
		p.entries = append(p.entries, e)
	}
	return p, nil
}

func standardNames() []string {
	return []string{"license", "security_policy", "code_of_conduct", "contributing", "codeowners", "readme"}
}

func (*standardsPolicy) Kind() string { return StandardsKind }

func (p *standardsPolicy) Needs() facts.Needs {
	n := facts.Needs{Standards: true}
	for _, e := range p.entries {
		if e.name == "readme" {
			n.Readme = true
		}
	}
	return n
}

func (p *standardsPolicy) Evaluate(r *facts.Repo) []policy.Finding {
	var out []policy.Finding
	for _, e := range p.entries {
		if !e.standard.met(r.Standards) {
			f := policy.Finding{Kind: StandardsKind, Subject: e.name, Message: e.standard.missing, Fix: policy.FixCreate, OnClose: e.onClose}
			if e.tmpl == nil {
				f.Message += " (no template to create it from)"
				f.Fix = policy.FixNone
			}
			out = append(out, f)
			continue
		}
		readme := r.Standards.Readme
		content, loaded := r.Files[readme]
		for _, b := range e.badges {
			f := policy.Finding{Kind: StandardsKind, Subject: "readme#badge:" + b}
			switch {
			case !loaded:
				f.Message = fmt.Sprintf("cannot check %s badge: %s content not loaded", b, readme)
			case !badgePatterns[b].Match(content):
				f.Message = fmt.Sprintf("%s has no %s badge", readme, b)
			default:
				continue
			}
			out = append(out, f)
		}
	}
	return out
}

func (p *standardsPolicy) Remediate(r *facts.Repo, f policy.Finding) (policy.Change, bool) {
	for _, e := range p.entries {
		if e.name == f.Subject && f.Fix == policy.FixCreate && e.tmpl != nil {
			return render(r, e.createAt, e.tmpl), true
		}
	}
	return nil, false
}
