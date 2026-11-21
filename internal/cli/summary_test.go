package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temikus/meles/internal/plan"
	"github.com/temikus/meles/internal/policy"
)

func onePlan(message string) plan.Plan {
	return plan.Plan{
		Summary: plan.Summary{Repos: 2, ReposWithFindings: 1, Findings: 1, ByAction: map[plan.Action]int{plan.ActionPR: 1}},
		Repos: []plan.RepoPlan{{Repo: "acme/widget", Items: []plan.Item{{
			Policy: "governance.files", Action: plan.ActionPR,
			Finding: policy.Finding{Subject: "file:SECURITY.md", Message: message},
		}}}},
	}
}

func TestJobSummaryAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(path, []byte("earlier step\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_STEP_SUMMARY", path)
	var errOut bytes.Buffer
	writeJobSummary(&errOut, "meles apply", onePlan("SECURITY.md is missing"), []string{"acme/widget: opened #7"})
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "earlier step\n" +
		"## meles apply\n\n" +
		"Checked 2 repos: 1 finding in 1 repo (1 pr).\n\n" +
		"| Repo | Policy | Subject | Action | Message |\n|---|---|---|---|---|\n" +
		"| acme/widget | governance.files | file:SECURITY.md | pr | SECURITY.md is missing |\n\n" +
		"**Outcome**\n\n- acme/widget: opened #7\n\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if errOut.Len() > 0 {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestJobSummaryTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", path)
	writeJobSummary(&bytes.Buffer{}, "meles plan", onePlan(strings.Repeat("x", 1<<20)), nil)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "## meles plan\n\nChecked 2 repos: 1 finding in 1 repo (1 pr).\n\nThe full report is too large for a job summary. See the job log.\n\n"
	if string(got) != want {
		t.Errorf("got %d bytes starting %q", len(got), string(got[:min(len(got), 200)]))
	}
}

func TestJobSummaryOutsideActions(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	var errOut bytes.Buffer
	writeJobSummary(&errOut, "meles plan", onePlan("x"), nil)
	if errOut.Len() > 0 {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestJobSummaryFailureIsAWarning(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", filepath.Join(t.TempDir(), "missing", "summary.md"))
	var errOut bytes.Buffer
	writeJobSummary(&errOut, "meles plan", onePlan("x"), nil)
	if !strings.Contains(errOut.String(), "warning: job summary:") {
		t.Errorf("stderr = %q", errOut.String())
	}
}
