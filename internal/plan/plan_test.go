package plan

import (
	"strings"
	"testing"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

// stub is a policy with canned findings. Plan logic is under test here, not policies.
type stub struct {
	kind   string
	finds  []policy.Finding
	change policy.Change
}

func (s stub) Kind() string                          { return s.kind }
func (s stub) Needs() facts.Needs                    { return facts.Needs{} }
func (s stub) Evaluate(*facts.Repo) []policy.Finding { return s.finds }
func (s stub) Remediate(_ *facts.Repo, f policy.Finding) (policy.Change, bool) {
	if f.Fix == policy.FixNone || s.change == nil {
		return nil, false
	}
	return s.change, true
}

func inst(name, kind string, mode config.Mode, sel config.Selector, p policy.Policy) Instance {
	return Instance{Name: name, Spec: config.PolicySpec{ID: kind, Mode: mode, OnClose: config.Accept}, Selector: sel, Policy: p}
}

func TestActions(t *testing.T) {
	file := policy.FileChange{Path: "x", Content: []byte("y")}
	setting := policy.SettingChange{Setting: "s", From: false, To: true}
	fix := []policy.Finding{{Kind: "k", Subject: "s", Message: "m", Fix: policy.FixSet}}
	check := []policy.Finding{{Kind: "k", Subject: "s", Message: "m"}}
	for _, tc := range []struct {
		name   string
		mode   config.Mode
		finds  []policy.Finding
		change policy.Change
		want   Action
		note   string
	}{
		{"observe reports", config.Observe, fix, file, ActionReport, ""},
		{"pr file", config.PR, fix, file, ActionPR, ""},
		{"enforce file still PRs", config.Enforce, fix, file, ActionPR, ""},
		{"enforce setting applies", config.Enforce, fix, setting, ActionApply, ""},
		{"pr setting needs enforce", config.PR, fix, setting, ActionReport, "settings apply only with mode: enforce"},
		{"check-only", config.Enforce, check, setting, ActionCheck, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Evaluate([]Instance{inst("k", "k", tc.mode, config.Selector{}, stub{"k", tc.finds, tc.change})}, []*facts.Repo{policytest.Repo(nil)})
			items := p.Repos[0].Items
			if len(items) != 1 || items[0].Action != tc.want || items[0].Note != tc.note {
				t.Fatalf("items = %+v, want action %s note %q", items, tc.want, tc.note)
			}
		})
	}
}

func TestAmbiguousInstances(t *testing.T) {
	f := []policy.Finding{{Kind: "ci.shape", Subject: "s", Message: "m"}}
	other := []policy.Finding{{Kind: "deps.updater", Subject: "d", Message: "m"}}
	insts := []Instance{
		inst("ci.shape[go]", "ci.shape", config.PR, config.Selector{Files: []string{"go.mod"}}, stub{"ci.shape", f, nil}),
		inst("ci.shape[python]", "ci.shape", config.PR, config.Selector{Files: []string{"pyproject.toml"}}, stub{"ci.shape", f, nil}),
		inst("deps.updater", "deps.updater", config.PR, config.Selector{}, stub{"deps.updater", other, nil}),
	}
	both := policytest.Repo(map[string]string{"go.mod": "", "pyproject.toml": ""})
	p := Evaluate(insts, []*facts.Repo{both})
	rp := p.Repos[0]
	if len(rp.Errors) != 1 || !strings.Contains(rp.Errors[0], "ci.shape[go], ci.shape[python]") {
		t.Errorf("errors = %v", rp.Errors)
	}
	if len(rp.Items) != 1 || rp.Items[0].Policy != "deps.updater" {
		t.Errorf("other policies must still run: %+v", rp.Items)
	}
}

func TestSelectorsAndOrder(t *testing.T) {
	f := func(s string) []policy.Finding { return []policy.Finding{{Kind: "k", Subject: s, Message: "m"}} }
	insts := []Instance{
		inst("b", "b", config.PR, config.Selector{}, stub{"b", f("z"), nil}),
		inst("a", "a", config.PR, config.Selector{Visibility: "private"}, stub{"a", f("y"), nil}),
	}
	pub := policytest.Repo(nil)
	pub.Name = "zeta"
	priv := policytest.Repo(nil)
	priv.Name, priv.Visibility = "alpha", "private"
	clean := policytest.Repo(nil)
	clean.Name, clean.Archived = "archived", true

	p := Evaluate(insts, []*facts.Repo{pub, priv, clean})
	var got []string
	for _, r := range p.Repos {
		for _, it := range r.Items {
			got = append(got, r.Repo+" "+it.Policy)
		}
	}
	want := []string{"acme/alpha a", "acme/alpha b", "acme/zeta b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v, want %v", got, want)
	}
	if p.Summary.Repos != 3 || p.Summary.ReposWithFindings != 2 || p.Summary.Findings != 3 {
		t.Errorf("summary = %+v", p.Summary)
	}
}
