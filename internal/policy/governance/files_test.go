package governance

import (
	"testing"
	"testing/fstest"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

var ctl = fstest.MapFS{
	"templates/SECURITY.md":  {Data: []byte("Report issues in {{.FullName}} to security@example.com\n")},
	"templates/CODEOWNERS":   {Data: []byte("* @{{.Owner}}\n")},
	"templates/LICENSE":      {Data: []byte("Apache License\n")},
	"templates/editorconfig": {Data: []byte("root = true\n")},
	"templates/bad":          {Data: []byte("{{.Nope}}")},
}

const filesExample = `
.editorconfig:      { template: templates/editorconfig }
.github/CODEOWNERS: { template: templates/CODEOWNERS }
`

const goodReadme = "# widget\n" +
	"[![CI](https://github.com/acme/widget/actions/workflows/ci.yml/badge.svg)](x)\n" +
	"[![License](https://img.shields.io/github/license/acme/widget)](LICENSE)\n"

func f(subject, msg string) policy.Finding {
	return policy.Finding{Kind: FilesKind, Subject: subject, Message: msg, Fix: policy.FixCreate}
}

func TestFilesEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []policy.Finding
	}{
		{"present", map[string]string{".editorconfig": "x", ".github/CODEOWNERS": "x"}, nil},
		{
			"missing",
			map[string]string{".editorconfig": "x"},
			[]policy.Finding{f("file:.github/CODEOWNERS", ".github/CODEOWNERS is missing")},
		},
		{
			// The key is the path: other locations do not count.
			"elsewhere does not count",
			map[string]string{".editorconfig": "x", "CODEOWNERS": "x"},
			[]policy.Finding{f("file:.github/CODEOWNERS", ".github/CODEOWNERS is missing")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newFiles(t, filesExample).Evaluate(policytest.Repo(tc.files))
			if !equal(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestFilesOnClosePerEntry(t *testing.T) {
	got := newFiles(t, "{.editorconfig: {template: templates/editorconfig, onClose: reopen}}").Evaluate(policytest.Repo(nil))
	if len(got) != 1 || got[0].OnClose != config.Reopen {
		t.Errorf("findings = %+v", got)
	}
}

func TestFilesNeedNoContent(t *testing.T) {
	if n := newFiles(t, filesExample).Needs(); len(n.Paths) != 0 || n.Standards || n.Readme {
		t.Errorf("existence-only files must not load anything: %+v", n)
	}
}

func TestFilesConverge(t *testing.T) {
	p := newFiles(t, filesExample)
	left := policytest.Converge(t, p, policytest.Repo(nil), nil)
	if len(left) != 0 {
		t.Errorf("not converged: %+v", left)
	}
	r := policytest.Repo(nil)
	c, _ := p.Remediate(r, p.Evaluate(r)[1])
	if fc := c.(policy.FileChange); fc.Path != ".github/CODEOWNERS" || string(fc.Content) != "* @acme\n" {
		t.Errorf("change = {%s %q}", fc.Path, fc.Content)
	}
}

func TestFilesFactoryErrors(t *testing.T) {
	for _, tc := range []struct{ name, with string }{
		{"template required", "{.editorconfig: {}}"},
		{"missing template", "{.editorconfig: {template: templates/nope}}"},
		{"template references unknown field", "{.editorconfig: {template: templates/bad}}"},
		{"unknown key", "{.editorconfig: {templte: x}}"},
		{"badges belong to governance.standards", "{README.md: {template: templates/LICENSE, badges: [ci]}}"},
		{"absolute path", "{/etc/passwd: {template: templates/LICENSE}}"},
		{"bad onClose", "{.editorconfig: {template: templates/editorconfig, onClose: never}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewFiles(policytest.With(t, tc.with), ctl); err == nil {
				t.Error("want error")
			}
		})
	}
}

func newFiles(t *testing.T, w string) policy.Policy {
	t.Helper()
	p, err := NewFiles(policytest.With(t, w), ctl)
	if err != nil {
		t.Fatalf("NewFiles: %v", err)
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
