package apply

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/housekeeping"
	"github.com/temikus/meles/internal/plan"
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

var opts = Options{Branch: "meles/housekeeping", Labels: []string{"housekeeping"}, ControlRef: "c0ffee", Today: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}

func fileItem(subject, path, content string, onClose config.OnClose) plan.Item {
	f := policy.Finding{Kind: "governance.files", Subject: subject, Message: path + " is missing", Fix: policy.FixCreate}
	return plan.Item{
		Policy: "governance.files", Finding: f, Key: f.Key(), Shape: f.Shape(), Mode: config.PR, OnClose: onClose,
		Action: plan.ActionPR, Change: policy.FileChange{Path: path, Content: []byte(content)},
	}
}

var (
	hello = fileItem("file:hello.txt", "hello.txt", "hi\n", config.Accept)
	bye   = fileItem("file:bye.txt", "bye.txt", "bye\n", config.Reopen)
)

// current is a repo whose branch and open PR already carry exactly items.
func current(items ...plan.Item) *facts.Repo {
	r := policytest.Repo(nil)
	files := map[string]string{}
	for _, it := range items {
		fc := it.Change.(policy.FileChange)
		files[fc.Path] = housekeeping.BlobSHA(fc.Content)
	}
	r.Housekeeping = facts.Housekeeping{
		BaseSHA: "base", BaseTree: "tree",
		Branch: &facts.Branch{SHA: "head", Parents: []string{"base"}, Message: commitMessage(items), Files: files},
		PRs:    []facts.PR{{Number: 12, Open: true, Title: Title, Body: body(items, opts.ControlRef), Labels: []string{"housekeeping"}}},
	}
	return r
}

func rp(items ...plan.Item) plan.RepoPlan { return plan.RepoPlan{Repo: "acme/widget", Items: items} }

func TestDecide(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plan  plan.RepoPlan
		repo  func() *facts.Repo
		check func(t *testing.T, d Decision)
	}{
		{
			"nothing to do", rp(), func() *facts.Repo { return policytest.Repo(nil) },
			func(t *testing.T, d Decision) { wantNoWrites(t, d) },
		},
		{
			"first run opens a PR", rp(hello),
			func() *facts.Repo {
				r := policytest.Repo(nil)
				r.Housekeeping = facts.Housekeeping{BaseSHA: "base", BaseTree: "tree"}
				return r
			},
			func(t *testing.T, d Decision) {
				if d.Kind != Write || !d.Rewrite || !d.CreatePR || !slices.Equal(d.AddLabels, []string{"housekeeping"}) {
					t.Errorf("decision = %+v", d)
				}
				m, ok := housekeeping.Decode(d.Body)
				if !ok || len(m.Items) != 1 || m.Items[0].Key != hello.Key || m.Items[0].OnClose != config.Accept {
					t.Errorf("marker = %+v, %v", m, ok)
				}
				if !housekeeping.HasTrailer(d.Message) {
					t.Errorf("commit message lacks the trailer: %q", d.Message)
				}
			},
		},
		{
			// The idempotency invariant: a second run with no drift writes nothing.
			"no drift", rp(hello, bye), func() *facts.Repo { return current(hello, bye) },
			func(t *testing.T, d Decision) { wantNoWrites(t, d) },
		},
		{
			// A new control-repo commit alone must not touch every open PR.
			"only the control ref moved", rp(hello),
			func() *facts.Repo {
				r := current(hello)
				r.Housekeeping.PRs[0].Body = body([]plan.Item{hello}, "0ld")
				return r
			},
			func(t *testing.T, d Decision) { wantNoWrites(t, d) },
		},
		{
			"content drift rewrites the branch in place", rp(fileItem("file:hello.txt", "hello.txt", "hello again\n", config.Accept)),
			func() *facts.Repo { return current(hello) },
			func(t *testing.T, d Decision) {
				if !d.Rewrite || d.CreatePR || d.PR != 12 {
					t.Errorf("decision = %+v", d)
				}
			},
		},
		{
			"a new finding updates the same PR", rp(hello, bye), func() *facts.Repo { return current(hello) },
			func(t *testing.T, d Decision) {
				if !d.Rewrite || !d.EditPR || d.CreatePR || d.PR != 12 {
					t.Errorf("decision = %+v", d)
				}
			},
		},
		{
			"a missing label is added", rp(hello),
			func() *facts.Repo {
				r := current(hello)
				r.Housekeeping.PRs[0].Labels = nil
				return r
			},
			func(t *testing.T, d Decision) {
				if d.Rewrite || d.EditPR || !slices.Equal(d.AddLabels, []string{"housekeeping"}) {
					t.Errorf("decision = %+v", d)
				}
			},
		},
		{
			"branch current but its PR was closed: new PR, no new commit", rp(bye),
			func() *facts.Repo {
				r := current(bye)
				r.Housekeeping.PRs[0].Open = false
				return r
			},
			func(t *testing.T, d Decision) {
				if d.Rewrite || !d.CreatePR || d.PR != 0 {
					t.Errorf("decision = %+v", d)
				}
			},
		},
		{
			"a merged PR's old branch gets a fresh commit", rp(hello),
			func() *facts.Repo {
				r := current(hello)
				r.Housekeeping.PRs[0].Open, r.Housekeeping.PRs[0].Merged = false, true
				r.Housekeeping.BaseSHA = "newer"
				return r
			},
			func(t *testing.T, d Decision) {
				if !d.Rewrite || !d.CreatePR {
					t.Errorf("decision = %+v", d)
				}
			},
		},
		{
			"a human commit on the branch blocks", rp(hello),
			func() *facts.Repo {
				r := current(hello)
				r.Housekeeping.Branch.Message = "fix: tweak by hand"
				return r
			},
			func(t *testing.T, d Decision) { wantBlocked(t, d, "commit meles did not make") },
		},
		{
			"an open PR from the branch that is not meles's blocks", rp(hello),
			func() *facts.Repo {
				r := current(hello)
				r.Housekeeping.PRs[0].Body = "hand-written"
				return r
			},
			func(t *testing.T, d Decision) { wantBlocked(t, d, "#12 is not a meles PR") },
		},
		{
			"repo errors block writes",
			plan.RepoPlan{Repo: "acme/widget", Items: []plan.Item{hello, alerts}, Errors: []string{".meles.yml: line 1: reason is required"}},
			func() *facts.Repo { return current() },
			func(t *testing.T, d Decision) { wantBlocked(t, d, ".meles.yml") },
		},
		{
			"drift fixed elsewhere closes the PR as meles", rp(),
			func() *facts.Repo { return current(hello) },
			func(t *testing.T, d Decision) {
				m, ok := housekeeping.Decode(d.Body)
				if d.Kind != Close || d.PR != 12 || !d.DeleteBranch || !ok || m.ClosedBy != housekeeping.ClosedByMeles {
					t.Errorf("decision = %+v, marker %+v", d, m)
				}
			},
		},
		{
			"only pr items go in", rp(withAction(hello, plan.ActionSkip), withAction(bye, plan.ActionReport), invalid(hello)),
			func() *facts.Repo { return policytest.Repo(nil) },
			func(t *testing.T, d Decision) { wantNoWrites(t, d) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, Decide(tc.plan, tc.repo(), opts))
		})
	}
}

func TestDecideCommentsOnceOnAcceptedClose(t *testing.T) {
	closed := func(noted bool) *facts.Repo {
		r := current(hello, bye)
		r.Housekeeping.PRs[0].Open = false
		r.Housekeeping.PRs[0].Noted = noted
		return r
	}
	// The plan skips the accepted finding; the reopen one comes back.
	p := rp(withAction(hello, plan.ActionSkip), bye)
	d := Decide(p, closed(false), opts)
	if d.Comment == nil || d.Comment.PR != 12 {
		t.Fatalf("no comment: %+v", d)
	}
	for _, want := range []string{housekeeping.NoteMarker, "policy: governance.files", "subject: file:hello.txt", "until: 2027-10-15"} {
		if !strings.Contains(d.Comment.Body, want) {
			t.Errorf("comment lacks %q:\n%s", want, d.Comment.Body)
		}
	}
	if strings.Contains(d.Comment.Body, "bye.txt") {
		t.Errorf("reopen findings are not exemptions:\n%s", d.Comment.Body)
	}
	if !d.CreatePR {
		t.Errorf("the reopen finding needs a fresh PR: %+v", d)
	}
	if d := Decide(p, closed(true), opts); d.Comment != nil {
		t.Errorf("commented twice: %+v", d.Comment)
	}
}

var alerts = settingItem("dependabotAlerts", config.Enforce, plan.ActionApply)

func settingItem(name string, mode config.Mode, a plan.Action) plan.Item {
	f := policy.Finding{Kind: "security.features", Subject: "setting:security." + name, Message: name + " is off, want on", Fix: policy.FixSet}
	return plan.Item{
		Policy: "security.features", Finding: f, Key: f.Key(), Shape: f.Shape(), Mode: mode, Action: a,
		Change: policy.SettingChange{Setting: "security." + name, From: false, To: true},
	}
}

func TestDecideSettings(t *testing.T) {
	report := settingItem("secretScanning", config.PR, plan.ActionReport)
	d := Decide(rp(alerts, report), policytest.Repo(nil), opts)
	if len(d.Settings) != 1 || d.Settings[0].Setting != "security.dependabotAlerts" || !d.Writes() {
		t.Errorf("want only the enforce setting applied: %+v", d)
	}
	if want := []string{"set security.dependabotAlerts to true"}; !slices.Equal(d.Steps(), want) {
		t.Errorf("steps = %q, want %q", d.Steps(), want)
	}

	// A PR problem does not hold back settings, which never touch the branch.
	r := current(hello)
	r.Housekeeping.Branch.Message = "fix: tweak by hand"
	if d := Decide(rp(hello, alerts), r, opts); d.Kind != Blocked || len(d.Settings) != 1 || !d.Writes() {
		t.Errorf("blocked PR must still apply settings: %+v", d)
	}
}

func TestSteps(t *testing.T) {
	first := policytest.Repo(nil)
	first.Housekeeping = facts.Housekeeping{BaseSHA: "base", BaseTree: "tree"}
	closed := current(hello)
	closed.Housekeeping.PRs[0].Open = false
	for _, tc := range []struct {
		name string
		d    Decision
		want []string
	}{
		{"first run", Decide(rp(hello, bye), first, opts), []string{"push 2 file(s) to meles/housekeeping", "open a PR"}},
		{"second file", Decide(rp(hello, bye), current(hello), opts), []string{"push 2 file(s) to meles/housekeeping", "update #12"}},
		{"labels only", Decision{Kind: Write, PR: 12, AddLabels: []string{"x"}}, []string{"label #12 x"}},
		{"close", Decide(rp(), current(hello), opts), []string{"close #12: nothing left to change"}},
		{"accept note", Decide(rp(withAction(hello, plan.ActionSkip)), closed, opts), []string{"comment on #12 about the accepted close"}},
		{"nothing", Decide(rp(hello), current(hello), opts), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.Steps(); !slices.Equal(got, tc.want) {
				t.Errorf("steps = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLimit(t *testing.T) {
	first := Decision{Repo: policytest.Repo(nil), Kind: Write, Rewrite: true, CreatePR: true}
	labels := Decision{Repo: policytest.Repo(nil), Kind: Write, AddLabels: []string{"x"}}
	ds := Limit([]Decision{first, labels, first, first}, 2)
	got := []Kind{ds[0].Kind, ds[1].Kind, ds[2].Kind, ds[3].Kind}
	if !slices.Equal(got, []Kind{Write, Write, Write, Deferred}) {
		t.Errorf("kinds = %v; a label-only update does not count toward maxPerRun", got)
	}
}

func TestLimitSettings(t *testing.T) {
	settings := Decide(rp(alerts), policytest.Repo(nil), opts)
	both := Decide(rp(hello, alerts), policytest.Repo(nil), opts)
	labels := Decision{Repo: policytest.Repo(nil), Kind: Write, PR: 12, AddLabels: []string{"x"}, Settings: settings.Settings}
	r := current(hello)
	r.Housekeeping.Branch.Message = "fix: tweak by hand"
	blockedPR := Decide(rp(hello, alerts), r, opts)

	ds := Limit([]Decision{settings, both, settings, labels, blockedPR}, 1)
	if ds[0].Kind != Nothing || len(ds[0].Settings) != 1 || ds[0].SettingsDeferred {
		t.Errorf("settings within the cap must apply: %+v", ds[0])
	}
	if ds[1].Kind != Deferred || len(ds[1].Settings) != 0 || ds[1].Writes() {
		t.Errorf("a repo over the cap waits, settings and all: %+v", ds[1])
	}
	if ds[2].Kind != Deferred || ds[2].Writes() {
		t.Errorf("settings alone count toward the cap: %+v", ds[2])
	}
	if ds[3].Kind != Write || len(ds[3].Settings) != 0 || !ds[3].SettingsDeferred || !ds[3].Writes() {
		t.Errorf("over the cap, a cheap PR update goes ahead without its settings: %+v", ds[3])
	}
	if ds[4].Kind != Blocked || len(ds[4].Settings) != 0 || !ds[4].SettingsDeferred {
		t.Errorf("a blocked repo over the cap holds its settings too: %+v", ds[4])
	}
}

func withAction(it plan.Item, a plan.Action) plan.Item { it.Action = a; return it }

func invalid(it plan.Item) plan.Item { it.Invalid = "renovate-config-validator: nope"; return it }

func wantNoWrites(t *testing.T, d Decision) {
	t.Helper()
	if d.Writes() {
		t.Errorf("want no writes, got %+v", d)
	}
}

func wantBlocked(t *testing.T, d Decision, reason string) {
	t.Helper()
	if d.Kind != Blocked || !strings.Contains(d.Reason, reason) || d.Writes() {
		t.Errorf("want blocked for %q, got %+v", reason, d)
	}
}
