package governance

import (
	"testing"
	"testing/fstest"

	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

var ctl = fstest.MapFS{
	"templates/SECURITY.md": {Data: []byte("Report issues in {{.FullName}} to security@example.com\n")},
	"templates/CODEOWNERS":  {Data: []byte("* @{{.Owner}}\n")},
	"templates/LICENSE":     {Data: []byte("Apache License\n")},
	"templates/bad":         {Data: []byte("{{.Nope}}")},
}

const example = `
LICENSE:     { template: templates/LICENSE }
SECURITY.md: { template: templates/SECURITY.md }
CODEOWNERS:  { template: templates/CODEOWNERS, path: .github/CODEOWNERS }
README.md:   { badges: [ci, license] }
`

const goodReadme = "# widget\n" +
	"[![CI](https://github.com/acme/widget/actions/workflows/ci.yml/badge.svg)](x)\n" +
	"[![License](https://img.shields.io/github/license/acme/widget)](LICENSE)\n"

func compliant() map[string]string {
	return map[string]string{
		"LICENSE":     "Apache",
		"SECURITY.md": "x",
		"CODEOWNERS":  "* @acme",
		"README.md":   goodReadme,
	}
}

func without(m map[string]string, drop ...string) map[string]string {
	for _, d := range drop {
		delete(m, d)
	}
	return m
}

func with(m map[string]string, k, v string) map[string]string {
	m[k] = v
	return m
}

func f(subject, msg string, fix policy.Fix) policy.Finding {
	return policy.Finding{Kind: Kind, Subject: subject, Message: msg, Fix: fix}
}

func TestEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		with  string
		files map[string]string
		want  []policy.Finding
	}{
		{"compliant", example, compliant(), nil},
		{"security in .github", example, with(without(compliant(), "SECURITY.md"), ".github/SECURITY.md", "x"), nil},
		{"security in docs", example, with(without(compliant(), "SECURITY.md"), "docs/SECURITY.md", "x"), nil},
		{"codeowners at root satisfies .github path", example, compliant(), nil},
		{"LICENSE.md satisfies LICENSE", example, with(without(compliant(), "LICENSE"), "LICENSE.md", "x"), nil},
		{
			"license in docs does not count", example, with(without(compliant(), "LICENSE"), "docs/LICENSE", "x"),
			[]policy.Finding{f("file:LICENSE", "LICENSE is missing", policy.FixCreate)},
		},
		{"readme in .github", example, with(without(compliant(), "README.md"), ".github/README.md", goodReadme), nil},
		{
			"missing files", example, without(compliant(), "SECURITY.md", "CODEOWNERS"),
			[]policy.Finding{
				f("file:SECURITY.md", "SECURITY.md is missing", policy.FixCreate),
				f("file:CODEOWNERS", "CODEOWNERS is missing", policy.FixCreate),
			},
		},
		{
			"missing without template is check-only", "{CONTRIBUTING.md: {}}", compliant(),
			[]policy.Finding{f("file:CONTRIBUTING.md", "CONTRIBUTING.md is missing (no template to create it from)", policy.FixNone)},
		},
		{
			"missing badge", example, with(compliant(), "README.md", "# widget\n[![CI](https://github.com/acme/widget/actions/workflows/ci.yml/badge.svg)](x)\n"),
			[]policy.Finding{f("file:README.md#badge:license", "README.md has no license badge", policy.FixNone)},
		},
		{
			"coverage badge", "{README.md: {badges: [coverage]}}", with(compliant(), "README.md", "[![c](https://codecov.io/gh/acme/widget/graph/badge.svg)](x)"),
			nil,
		},
		{
			"readme missing with badges", example, without(compliant(), "README.md"),
			[]policy.Finding{f("file:README.md", "README.md is missing (no template to create it from)", policy.FixNone)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPolicy(t, tc.with)
			got := p.Evaluate(policytest.Repo(tc.files))
			if !equal(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestNeedsReadmeContent(t *testing.T) {
	n := newPolicy(t, example).Needs()
	for _, p := range []string{"README.md", ".github/README.md", "docs/README.md"} {
		if !contains(n.Paths, p) {
			t.Errorf("Needs.Paths %v missing %s", n.Paths, p)
		}
	}
	if contains(n.Paths, "SECURITY.md") {
		t.Errorf("existence-only files must not load content: %v", n.Paths)
	}
}

func TestRemediate(t *testing.T) {
	p := newPolicy(t, example)
	r := policytest.Repo(without(compliant(), "SECURITY.md", "CODEOWNERS"))
	var got []policy.FileChange
	for _, fd := range p.Evaluate(r) {
		c, ok := p.Remediate(r, fd)
		if !ok {
			t.Fatalf("no change for %+v", fd)
		}
		got = append(got, c.(policy.FileChange))
	}
	want := []policy.FileChange{
		{Path: "SECURITY.md", Content: []byte("Report issues in acme/widget to security@example.com\n")},
		{Path: ".github/CODEOWNERS", Content: []byte("* @acme\n")},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Path != want[i].Path || string(got[i].Content) != string(want[i].Content) || got[i].Delete {
			t.Errorf("change %d = {%s %q}, want {%s %q}", i, got[i].Path, got[i].Content, want[i].Path, want[i].Content)
		}
	}
}

func TestConverges(t *testing.T) {
	p := newPolicy(t, example)
	left := policytest.Converge(t, p, policytest.Repo(map[string]string{"README.md": goodReadme}), nil)
	if len(left) != 0 {
		t.Errorf("not converged: %+v", left)
	}
}

func TestFactoryErrors(t *testing.T) {
	for _, tc := range []struct{ name, with string }{
		{"unknown badge", "{README.md: {badges: [sparkles]}}"},
		{"missing template", "{SECURITY.md: {template: templates/nope.md}}"},
		{"template references unknown field", "{SECURITY.md: {template: templates/bad}}"},
		{"unknown key", "{SECURITY.md: {templte: x}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(policytest.With(t, tc.with), ctl); err == nil {
				t.Error("want error")
			}
		})
	}
}

func newPolicy(t *testing.T, w string) policy.Policy {
	t.Helper()
	p, err := New(policytest.With(t, w), ctl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func equal(a, b []policy.Finding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
