package plan

import (
	"strings"
	"testing"
	"time"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/housekeeping"
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

var today = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

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
			p := Evaluate([]Instance{inst("k", "k", tc.mode, config.Selector{}, stub{"k", tc.finds, tc.change})}, []*facts.Repo{policytest.Repo(nil)}, today)
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
	p := Evaluate(insts, []*facts.Repo{both}, today)
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

	p := Evaluate(insts, []*facts.Repo{pub, priv, clean}, today)
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

func TestExemptions(t *testing.T) {
	finds := []policy.Finding{
		{Kind: "k", Subject: "a", Message: "m", Fix: policy.FixSet},
		{Kind: "k", Subject: "b", Message: "m", Fix: policy.FixSet},
	}
	insts := []Instance{inst("k", "k", config.Enforce, config.Selector{}, stub{"k", finds, policy.SettingChange{Setting: "s", To: true}})}
	for _, tc := range []struct {
		name, meles   string
		wantA, wantB  Action
		noteA, errors string
	}{
		{"none", "", ActionApply, ActionApply, "", ""},
		{
			"one subject", "exempt: [{policy: k, subject: a, reason: owned elsewhere, until: 2027-01-01}]",
			ActionSkip, ActionApply, "exempt until 2027-01-01: owned elsewhere", "",
		},
		{"whole policy", "exempt: [{policy: k, reason: r, until: 2027-01-01}]", ActionSkip, ActionSkip, "exempt until 2027-01-01: r", ""},
		{
			"expired", "exempt: [{policy: k, subject: a, reason: r, until: 2026-01-01}]",
			ActionApply, ActionApply, "exemption expired 2026-01-01", "",
		},
		{
			// A broken .meles.yml must not hide drift: findings stay, the repo gets an error.
			"invalid", "exempt: [{policy: k}]",
			ActionApply, ActionApply, "", ".meles.yml: line 1: reason is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			if tc.meles != "" {
				files[".meles.yml"] = tc.meles
			}
			rp := Evaluate(insts, []*facts.Repo{policytest.Repo(files)}, today).Repos[0]
			if len(rp.Items) != 2 || rp.Items[0].Action != tc.wantA || rp.Items[1].Action != tc.wantB || rp.Items[0].Note != tc.noteA {
				t.Errorf("items = %+v", rp.Items)
			}
			if got := strings.Join(rp.Errors, "|"); !strings.Contains(got, tc.errors) || (tc.errors == "" && got != "") {
				t.Errorf("errors = %q, want %q", got, tc.errors)
			}
		})
	}
}

func TestFindingOnCloseOverridesInstance(t *testing.T) {
	finds := []policy.Finding{
		{Kind: "k", Subject: "a", Message: "m", Fix: policy.FixCreate, OnClose: config.Reopen},
		{Kind: "k", Subject: "b", Message: "m", Fix: policy.FixCreate},
	}
	insts := []Instance{inst("k", "k", config.PR, config.Selector{}, stub{"k", finds, policy.FileChange{Path: "x"}})}
	items := Evaluate(insts, []*facts.Repo{policytest.Repo(nil)}, today).Repos[0].Items
	if items[0].OnClose != config.Reopen || items[1].OnClose != config.Accept {
		t.Errorf("on_close = %s, %s; want reopen from the finding, accept from the instance", items[0].OnClose, items[1].OnClose)
	}
}

func TestAcceptedCloses(t *testing.T) {
	a := policy.Finding{Kind: "k", Subject: "a", Message: "m", Fix: policy.FixCreate}
	b := policy.Finding{Kind: "k", Subject: "b", Message: "m", Fix: policy.FixCreate}
	marker := func(closedBy string, items ...housekeeping.Item) string {
		return "## meles\n\n" + housekeeping.Marker{Items: items, ClosedBy: closedBy}.Encode()
	}
	carried := housekeeping.Item{Key: a.Key(), Shape: a.Shape(), OnClose: config.Accept}
	closed := today.AddDate(0, -1, 0)
	for _, tc := range []struct {
		name    string
		onClose config.OnClose
		pr      facts.PR
		skip    bool
		note    string
	}{
		{"closed by a human", config.Accept, facts.PR{Number: 7, ClosedAt: closed, Body: marker("", carried)}, true, "closed in #7 (accept until 2027-09-15)"},
		{"accept ends after a year, inclusive", config.Accept, facts.PR{Number: 7, ClosedAt: today.AddDate(-1, 0, 0), Body: marker("", carried)}, true, "closed in #7 (accept until 2026-10-15)"},
		{"accept expired", config.Accept, facts.PR{Number: 7, ClosedAt: today.AddDate(-1, 0, -1), Body: marker("", carried)}, false, "accept from #7 expired 2026-10-14"},
		{"drift changed shape", config.Accept, facts.PR{Number: 7, ClosedAt: closed, Body: marker("", housekeeping.Item{Key: a.Key(), Shape: "other"})}, false, ""},
		{"policy now says reopen", config.Reopen, facts.PR{Number: 7, ClosedAt: closed, Body: marker("", carried)}, false, ""},
		{"merged", config.Accept, facts.PR{Number: 7, ClosedAt: closed, Merged: true, Body: marker("", carried)}, false, ""},
		{"still open", config.Accept, facts.PR{Number: 7, Open: true, Body: marker("", carried)}, false, ""},
		{"closed by meles", config.Accept, facts.PR{Number: 7, ClosedAt: closed, Body: marker(housekeeping.ClosedByMeles, carried)}, false, ""},
		{"not a meles PR", config.Accept, facts.PR{Number: 7, ClosedAt: closed, Body: "hand-written " + a.Key()}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := inst("k", "k", config.PR, config.Selector{}, stub{"k", []policy.Finding{a, b}, policy.FileChange{Path: "x"}})
			in.Spec.OnClose = tc.onClose
			r := policytest.Repo(nil)
			r.Housekeeping.PRs = []facts.PR{tc.pr}
			items := Evaluate([]Instance{in}, []*facts.Repo{r}, today).Repos[0].Items
			if got := items[0].Action == ActionSkip; got != tc.skip {
				t.Errorf("a: action %s note %q, want skip=%v", items[0].Action, items[0].Note, tc.skip)
			}
			if items[0].Note != tc.note {
				t.Errorf("note = %q, want %q", items[0].Note, tc.note)
			}
			if items[1].Action != ActionPR {
				t.Errorf("b was not in the PR and must stay: %+v", items[1])
			}
		})
	}
}

func TestBlock(t *testing.T) {
	wf := policy.FileChange{Path: ".github/workflows/ci.yml", Content: []byte("x")}
	finds := []policy.Finding{{Kind: "k", Subject: "s", Message: "m", Fix: policy.FixCreate}}
	insts := []Instance{
		inst("a", "a", config.PR, config.Selector{}, stub{"a", finds, wf}),
		inst("b", "b", config.PR, config.Selector{}, stub{"b", finds, policy.FileChange{Path: "README.md"}}),
	}
	p := Evaluate(insts, []*facts.Repo{policytest.Repo(nil)}, today)
	p.Block(IsWorkflow, "token lacks the workflow scope")
	items := p.Repos[0].Items
	if items[0].Invalid != "token lacks the workflow scope" || items[1].Invalid != "" {
		t.Errorf("items = %+v", items)
	}
	if !IsRenovateConfig(".github/renovate.json5") || !IsRenovateConfig(".renovaterc") || IsRenovateConfig("package.json") {
		t.Error("IsRenovateConfig")
	}
}
