package cli

import (
	"strings"
	"testing"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/ghclient"
	"github.com/temikus/meles/internal/plan"
	"github.com/temikus/meles/internal/policy"
)

func accessPlan() plan.Plan {
	p := plan.Plan{Summary: plan.Summary{ByAction: map[plan.Action]int{plan.ActionApply: 1, plan.ActionPR: 1}}, Repos: []plan.RepoPlan{{Repo: "acme/widget", Items: []plan.Item{
		{Action: plan.ActionApply, Change: policy.SettingChange{Setting: "security.secretScanning", To: true}},
		{Action: plan.ActionPR, Change: policy.FileChange{Path: ".github/workflows/claude.yml"}},
	}}}}
	return p
}

func TestAppInstances(t *testing.T) {
	insts := []plan.Instance{
		{Name: "security.features", Spec: config.PolicySpec{ID: "security.features"}},
		{Name: "governance.files", Spec: config.PolicySpec{ID: "governance.files"}},
	}
	got, warn := appInstances(ghclient.Installation{}, insts)
	if len(got) != 1 || got[0].Name != "governance.files" {
		t.Errorf("without Administration, security.features must be dropped: %+v", got)
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "security.features skipped") {
		t.Errorf("warnings = %q", warn)
	}
	got, warn = appInstances(ghclient.Installation{Administration: "read"}, insts)
	if len(got) != 2 || len(warn) != 0 {
		t.Errorf("with Administration: read, keep every instance: %+v %q", got, warn)
	}
}

func TestRestrictToApp(t *testing.T) {
	for _, tc := range []struct {
		name            string
		in              ghclient.Installation
		setting         plan.Action
		workflowBlocked bool
	}{
		{"read admin, no workflows", ghclient.Installation{Administration: "read"}, plan.ActionReport, true},
		{"write everything", ghclient.Installation{Administration: "write", Workflows: "write"}, plan.ActionApply, false},
		{"read workflows", ghclient.Installation{Administration: "write", Workflows: "read"}, plan.ActionApply, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := accessPlan()
			restrictToApp(tc.in, &p)
			items := p.Repos[0].Items
			if items[0].Action != tc.setting {
				t.Errorf("setting action = %s, want %s", items[0].Action, tc.setting)
			}
			if tc.setting == plan.ActionReport && !strings.Contains(items[0].Note, "Administration: read") {
				t.Errorf("note = %q", items[0].Note)
			}
			if blocked := items[1].Invalid != ""; blocked != tc.workflowBlocked {
				t.Errorf("workflow blocked = %v (%q), want %v", blocked, items[1].Invalid, tc.workflowBlocked)
			}
		})
	}
}
