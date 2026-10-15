package governance

import (
	"strings"
	"testing"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

const standardsExample = `
license:         { template: templates/LICENSE }
securityPolicy: { template: templates/SECURITY.md }
codeowners:      { template: templates/CODEOWNERS, createAt: .github/CODEOWNERS }
readme:          { badges: [ci, license] }
`

// met is a repo where GitHub reports every standard, with a README at path.
func met(path, readme string) *facts.Repo {
	r := policytest.Repo(map[string]string{path: readme})
	r.Standards = facts.Standards{License: true, SecurityPolicy: true, CodeOfConduct: true, Contributing: true, Codeowners: true, Readme: path}
	return r
}

func sf(subject, msg string, fix policy.Fix) policy.Finding {
	return policy.Finding{Kind: StandardsKind, Subject: subject, Message: msg, Fix: fix}
}

func TestStandardsEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name string
		with string
		repo func() *facts.Repo
		want []policy.Finding
	}{
		{"all met", standardsExample, func() *facts.Repo { return met("README.md", goodReadme) }, nil},
		{"readme GitHub found in docs/", standardsExample, func() *facts.Repo { return met("docs/README.md", goodReadme) }, nil},
		{
			"missing standards", standardsExample,
			func() *facts.Repo {
				r := met("README.md", goodReadme)
				r.Standards.SecurityPolicy, r.Standards.Codeowners = false, false
				return r
			},
			[]policy.Finding{
				sf("securityPolicy", "no security policy", policy.FixCreate),
				sf("codeowners", "no CODEOWNERS file", policy.FixCreate),
			},
		},
		{
			"a file in the tree does not count if GitHub does not see it", standardsExample,
			func() *facts.Repo {
				r := met("README.md", goodReadme)
				r.Tree["SECURITY.md"] = "sha"
				r.Standards.SecurityPolicy = false
				return r
			},
			[]policy.Finding{sf("securityPolicy", "no security policy", policy.FixCreate)},
		},
		{
			"missing without template is check-only", "{contributing: {}}",
			func() *facts.Repo {
				r := met("README.md", goodReadme)
				r.Standards.Contributing = false
				return r
			},
			[]policy.Finding{sf("contributing", "no contributing guidelines (no template to create it from)", policy.FixNone)},
		},
		{
			"missing badge", standardsExample,
			func() *facts.Repo {
				return met("README.md", "# widget\n[![CI](https://github.com/acme/widget/actions/workflows/ci.yml/badge.svg)](x)\n")
			},
			[]policy.Finding{sf("readme#badge:license", "README.md has no license badge", policy.FixNone)},
		},
		{
			"no readme", standardsExample,
			func() *facts.Repo {
				r := met("README.md", goodReadme)
				r.Standards.Readme = ""
				return r
			},
			[]policy.Finding{sf("readme", "no README (no template to create it from)", policy.FixNone)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newStandards(t, tc.with).Evaluate(tc.repo())
			if !equal(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestStandardsOnClosePerEntry(t *testing.T) {
	p := newStandards(t, "{securityPolicy: {template: templates/SECURITY.md, onClose: reopen}, codeowners: {template: templates/CODEOWNERS}}")
	got := p.Evaluate(policytest.Repo(nil))
	if len(got) != 2 || got[0].OnClose != config.Reopen || got[1].OnClose != "" {
		t.Errorf("findings = %+v; want reopen on securityPolicy only", got)
	}
}

func TestStandardsNeeds(t *testing.T) {
	n := newStandards(t, standardsExample).Needs()
	if !n.Standards || !n.Readme || len(n.Paths) != 0 {
		t.Errorf("Needs = %+v", n)
	}
	if n := newStandards(t, "{license: {}}").Needs(); n.Readme {
		t.Errorf("README content loaded without a readme entry: %+v", n)
	}
}

func TestStandardsRemediate(t *testing.T) {
	p := newStandards(t, standardsExample)
	r := met("README.md", goodReadme)
	r.Standards = facts.Standards{Readme: "README.md"}
	var got []string
	for _, fd := range p.Evaluate(r) {
		c, ok := p.Remediate(r, fd)
		if !ok {
			t.Fatalf("no change for %+v", fd)
		}
		fc := c.(policy.FileChange)
		got = append(got, fc.Path+"="+string(fc.Content))
	}
	want := []string{
		"LICENSE=Apache License\n",
		"SECURITY.md=Report issues in acme/widget to security@example.com\n",
		".github/CODEOWNERS=* @acme\n",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestStandardsFactoryErrors(t *testing.T) {
	for _, tc := range []struct{ name, with string }{
		{"file name instead of a standard", "{SECURITY.md: {template: templates/SECURITY.md}}"},
		{"badges on something other than readme", "{license: {badges: [ci]}}"},
		{"unknown badge", "{readme: {badges: [sparkles]}}"},
		{"missing template", "{securityPolicy: {template: templates/nope.md}}"},
		{"old path key", "{codeowners: {path: .github/CODEOWNERS}}"},
		{"bad onClose", "{license: {onClose: sometimes}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewStandards(policytest.With(t, tc.with), ctl); err == nil {
				t.Error("want error")
			}
		})
	}
}

func newStandards(t *testing.T, w string) policy.Policy {
	t.Helper()
	p, err := NewStandards(policytest.With(t, w), ctl)
	if err != nil {
		t.Fatalf("NewStandards: %v", err)
	}
	return p
}
