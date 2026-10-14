package deps

import (
	"strings"
	"testing"

	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

const (
	renovate   = `{prefer: renovate, extends: ["github>temikus/renovate-config"], forbid_both: true}`
	dependabot = `{prefer: dependabot, forbid_both: true}`
	goodJSON   = `{"extends": ["github>temikus/renovate-config"]}`
)

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
		{"renovate ok", renovate, map[string]string{"renovate.json": goodJSON}, nil},
		{"pinned preset counts", renovate, map[string]string{".github/renovate.json": `{"extends":["github>temikus/renovate-config#v2"]}`}, nil},
		{"named preset counts", renovate, map[string]string{".renovaterc": `{"extends":["github>temikus/renovate-config:go"]}`}, nil},
		{
			"neither", renovate, nil,
			[]policy.Finding{f("file:renovate.json", "no dependency updater configured; want Renovate", policy.FixCreate)},
		},
		{
			"neither, prefer dependabot", dependabot, nil,
			[]policy.Finding{f("file:.github/dependabot.yml", "no dependency updater configured; want Dependabot", policy.FixCreate)},
		},
		{
			"both forbidden", renovate,
			map[string]string{"renovate.json": goodJSON, ".github/dependabot.yml": "version: 2\n"},
			[]policy.Finding{f("file:.github/dependabot.yml", "both Renovate and Dependabot are configured; remove .github/dependabot.yml", policy.FixDelete)},
		},
		{"both allowed", `{prefer: renovate, extends: ["github>temikus/renovate-config"]}`, map[string]string{"renovate.json": goodJSON, ".github/dependabot.yml": "version: 2\n"}, nil},
		{
			"only the other updater", renovate,
			map[string]string{".github/dependabot.yaml": "version: 2\n"},
			[]policy.Finding{f("file:.github/dependabot.yaml", "Dependabot is configured but Renovate is preferred; migrate by hand", policy.FixNone)},
		},
		{
			"missing preset", renovate,
			map[string]string{".github/renovate.json": `{"extends": ["config:recommended"]}`},
			[]policy.Finding{f("file:.github/renovate.json", "renovate config does not extend github>temikus/renovate-config", policy.FixUpdate)},
		},
		{
			"json5 is check-only", renovate,
			map[string]string{"renovate.json5": `{extends: []}`},
			[]policy.Finding{f("file:renovate.json5", "renovate config does not extend github>temikus/renovate-config (JSON5: edit by hand)", policy.FixNone)},
		},
		{
			"invalid json is check-only", renovate,
			map[string]string{"renovate.json": `{"extends": [`},
			[]policy.Finding{f("file:renovate.json", "renovate.json is not valid JSON", policy.FixNone)},
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

func TestRemediateCreate(t *testing.T) {
	c := remediateOne(t, renovate, nil)
	want := "{\n  \"$schema\": \"https://docs.renovatebot.com/renovate-schema.json\",\n  \"extends\": [\n    \"github>temikus/renovate-config\"\n  ]\n}\n"
	if c.Path != "renovate.json" || string(c.Content) != want {
		t.Errorf("got %s:\n%s\nwant:\n%s", c.Path, c.Content, want)
	}

	c = remediateOne(t, `{prefer: renovate}`, nil)
	if !strings.Contains(string(c.Content), `"config:recommended"`) {
		t.Errorf("no extends configured should fall back to config:recommended:\n%s", c.Content)
	}

	c = remediateOne(t, dependabot, map[string]string{"go.mod": "", ".github/workflows/ci.yml": ""})
	want = "version: 2\nupdates:\n" +
		"  - package-ecosystem: github-actions\n    directory: /\n    schedule:\n      interval: weekly\n" +
		"  - package-ecosystem: gomod\n    directory: /\n    schedule:\n      interval: weekly\n"
	if c.Path != ".github/dependabot.yml" || string(c.Content) != want {
		t.Errorf("got %s:\n%s\nwant:\n%s", c.Path, c.Content, want)
	}
}

func TestRemediateDelete(t *testing.T) {
	c := remediateOne(t, renovate, map[string]string{"renovate.json": goodJSON, ".github/dependabot.yml": ""})
	if c.Path != ".github/dependabot.yml" || !c.Delete {
		t.Errorf("got %+v, want delete of .github/dependabot.yml", c)
	}
}

func TestRemediateExtendsKeepsEverythingElse(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{
			"single line",
			"{\n  \"$schema\": \"x\",\n  \"extends\": [\"config:recommended\"],\n  \"labels\":   [\"deps\"]\n}\n",
			"{\n  \"$schema\": \"x\",\n  \"extends\": [\"config:recommended\", \"github>temikus/renovate-config\"],\n  \"labels\":   [\"deps\"]\n}\n",
		},
		{
			"multi line",
			"{\n\t\"extends\": [\n\t\t\"config:recommended\"\n\t],\n\t\"automerge\": true\n}",
			"{\n\t\"extends\": [\n\t\t\"config:recommended\",\n\t\t\"github>temikus/renovate-config\"\n\t],\n\t\"automerge\": true\n}",
		},
		{
			"no extends key",
			"{\n  \"automerge\": true\n}\n",
			"{\n  \"extends\": [\"github>temikus/renovate-config\"],\n  \"automerge\": true\n}\n",
		},
		{"empty object", "{}", "{\n  \"extends\": [\"github>temikus/renovate-config\"]\n}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := remediateOne(t, renovate, map[string]string{"renovate.json": tc.in})
			if string(c.Content) != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", c.Content, tc.want)
			}
		})
	}
}

func TestConverges(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"from nothing":   nil,
		"from both":      {"renovate.json": `{"extends":[]}`, ".github/dependabot.yml": ""},
		"missing preset": {".github/renovate.json": `{"automerge": true}`},
	} {
		t.Run(name, func(t *testing.T) {
			left := policytest.Converge(t, newPolicy(t, renovate), policytest.Repo(files), nil)
			if len(left) != 0 {
				t.Errorf("not converged: %+v", left)
			}
		})
	}
}

func TestFactoryErrors(t *testing.T) {
	for _, w := range []string{`{prefer: npm}`, `{prefer: renovate, extend: []}`} {
		if _, err := New(policytest.With(t, w), nil); err == nil {
			t.Errorf("%s: want error", w)
		}
	}
}

func remediateOne(t *testing.T, w string, files map[string]string) policy.FileChange {
	t.Helper()
	p := newPolicy(t, w)
	r := policytest.Repo(files)
	fs := p.Evaluate(r)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	c, ok := p.Remediate(r, fs[0])
	if !ok {
		t.Fatalf("check-only: %+v", fs[0])
	}
	return c.(policy.FileChange)
}

func newPolicy(t *testing.T, w string) policy.Policy {
	t.Helper()
	p, err := New(policytest.With(t, w), nil)
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
