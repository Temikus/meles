// Package policytest helps table-test policies.
package policytest

import (
	"maps"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
)

// With parses a `with:` block from YAML.
func With(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("parse with: %v", err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{}
	}
	return doc.Content[0]
}

// Repo builds a repo whose tree and files hold the given contents.
func Repo(files map[string]string) *facts.Repo {
	r := &facts.Repo{
		Meta:  facts.Meta{Owner: "acme", Name: "widget", DefaultBranch: "main", Visibility: "public", Admin: true},
		Tree:  map[string]string{},
		Files: map[string][]byte{},
	}
	for p, c := range files {
		r.Tree[p] = "sha-" + p
		r.Files[p] = []byte(c)
	}
	return r
}

// ApplyFile returns a copy of r with c applied, as if the PR had merged.
func ApplyFile(r *facts.Repo, c policy.FileChange) *facts.Repo {
	out := *r
	out.Tree = maps.Clone(r.Tree)
	out.Files = maps.Clone(r.Files)
	if c.Delete {
		delete(out.Tree, c.Path)
		delete(out.Files, c.Path)
	} else {
		out.Tree[c.Path] = "sha-new-" + c.Path
		out.Files[c.Path] = c.Content
	}
	return &out
}

// Converge remediates every finding, applies file changes, and returns the
// findings left on the result. A converged policy leaves none.
func Converge(t *testing.T, p policy.Policy, r *facts.Repo, applySetting func(*facts.Repo, policy.SettingChange)) []policy.Finding {
	t.Helper()
	for range 3 {
		fs := p.Evaluate(r)
		changed := false
		for _, f := range fs {
			c, ok := p.Remediate(r, f)
			if !ok {
				continue
			}
			changed = true
			switch c := c.(type) {
			case policy.FileChange:
				r = ApplyFile(r, c)
			case policy.SettingChange:
				cp := *r
				applySetting(&cp, c)
				r = &cp
			}
		}
		if !changed {
			return fs
		}
	}
	return p.Evaluate(r)
}
