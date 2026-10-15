// Package governance implements the governance.files and governance.standards
// policies.
package governance

import (
	"bytes"
	"fmt"
	"io/fs"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
)

// FilesKind is the policy id for files required at an exact path.
const FilesKind = "governance.files"

// TemplateData is what templates can reference. Nothing time-dependent, so
// renders are stable across runs.
type TemplateData struct {
	Owner, Name, FullName, DefaultBranch string
}

type fileEntry struct {
	path    string
	tmpl    *template.Template
	onClose config.OnClose
}

type files struct{ entries []fileEntry }

// NewFiles builds governance.files. Keys are repo paths, in config order.
func NewFiles(n *yaml.Node, ctl fs.FS) (policy.Policy, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: with: must be a map of repo paths", n.Line)
	}
	p := &files{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if !fs.ValidPath(key.Value) || key.Value == "." {
			return nil, fmt.Errorf("line %d: %q is not a relative repo path", key.Line, key.Value)
		}
		var spec struct {
			Template string         `yaml:"template"`
			OnClose  config.OnClose `yaml:"onClose"`
		}
		if err := config.DecodeStrict(n.Content[i+1], &spec); err != nil {
			return nil, fmt.Errorf("%s: %w", key.Value, err)
		}
		if err := checkOnClose(spec.OnClose); err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", key.Line, key.Value, err)
		}
		if spec.Template == "" {
			return nil, fmt.Errorf("line %d: %s: template is required", key.Line, key.Value)
		}
		t, err := loadTemplate(ctl, spec.Template)
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", key.Line, key.Value, err)
		}
		p.entries = append(p.entries, fileEntry{path: key.Value, tmpl: t, onClose: spec.OnClose})
	}
	return p, nil
}

func (*files) Kind() string { return FilesKind }

func (*files) Needs() facts.Needs { return facts.Needs{} }

func (p *files) Evaluate(r *facts.Repo) []policy.Finding {
	var out []policy.Finding
	for _, e := range p.entries {
		if !r.Has(e.path) {
			out = append(out, policy.Finding{Kind: FilesKind, Subject: "file:" + e.path, Message: e.path + " is missing", Fix: policy.FixCreate, OnClose: e.onClose})
		}
	}
	return out
}

func (p *files) Remediate(r *facts.Repo, f policy.Finding) (policy.Change, bool) {
	for _, e := range p.entries {
		if "file:"+e.path == f.Subject && f.Fix == policy.FixCreate {
			return render(r, e.path, e.tmpl), true
		}
	}
	return nil, false
}

func checkOnClose(o config.OnClose) error {
	if o != "" && !o.Valid() {
		return fmt.Errorf("onClose %q must be accept or reopen", o)
	}
	return nil
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

func render(r *facts.Repo, path string, t *template.Template) policy.FileChange {
	var buf bytes.Buffer
	data := TemplateData{Owner: r.Owner, Name: r.Name, FullName: r.FullName(), DefaultBranch: r.DefaultBranch}
	_ = t.Execute(&buf, data) // cannot fail: loadTemplate executed it once with the same type
	return policy.FileChange{Path: path, Content: buf.Bytes()}
}
