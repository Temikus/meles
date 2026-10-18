package plan

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

var update = flag.Bool("update", false, "rewrite golden files")

func samplePlan() Plan {
	file := stub{"governance.files", []policy.Finding{{Kind: "governance.files", Subject: "file:SECURITY.md", Message: "SECURITY.md is missing", Fix: policy.FixCreate}}, policy.FileChange{Path: "SECURITY.md", Content: []byte("hi\n")}}
	set := stub{"security.features", []policy.Finding{{Kind: "security.features", Subject: "setting:security.secretScanning", Message: "secretScanning is off, want on", Fix: policy.FixSet}}, policy.SettingChange{Setting: "security.secretScanning", From: false, To: true}}
	insts := []Instance{
		inst("governance.files", "governance.files", config.PR, config.Selector{}, file),
		inst("security.features", "security.features", config.PR, config.Selector{}, set),
	}
	p := Evaluate(insts, []*facts.Repo{policytest.Repo(nil)}, today)
	p.Summary.Repos = 2
	p.Repos = append(p.Repos, RepoPlan{Repo: "acme/broken", Errors: []string{"tree: 500"}})
	p.ControlRef = "abc123"
	return p
}

func TestGolden(t *testing.T) {
	for name, write := range map[string]func(*bytes.Buffer, Plan) error{
		"plan.txt":  func(b *bytes.Buffer, p Plan) error { return WriteTable(b, p) },
		"plan.json": func(b *bytes.Buffer, p Plan) error { return WriteJSON(b, p) },
	} {
		t.Run(name, func(t *testing.T) {
			var got bytes.Buffer
			if err := write(&got, samplePlan()); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", name)
			if *update {
				if err := os.WriteFile(golden, got.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("output differs from %s (run with -update to accept):\n%s", golden, got.String())
			}
		})
	}
}

func TestValidateMarksInvalid(t *testing.T) {
	p := samplePlan()
	calls := 0
	v := Validator{
		Name:  "fake",
		Match: func(path string) bool { return path == "SECURITY.md" },
		Check: func(_ context.Context, _ string, _ []byte) error { calls++; return errors.New("bad\nmore") },
	}
	p.Repos = append(p.Repos, p.Repos[0]) // same content twice: validator runs once
	p.Validate(context.Background(), []Validator{v})
	if calls != 1 {
		t.Errorf("validator ran %d times, want 1", calls)
	}
	if p.Repos[0].Items[0].Invalid != "fake: bad\nmore" {
		t.Errorf("invalid = %q", p.Repos[0].Items[0].Invalid)
	}
}

func TestValidateIgnoresPreexistingErrors(t *testing.T) {
	p := samplePlan()
	p.Repos[0].Items[0].Before = []byte("old")
	v := Validator{
		Name:  "fake",
		Match: func(string) bool { return true },
		Check: func(context.Context, string, []byte) error { return errors.New("bad") },
	}
	p.Validate(context.Background(), []Validator{v})
	it := p.Repos[0].Items[0]
	if it.Invalid != "" || !strings.Contains(it.Note, "fake also rejects the current file") {
		t.Errorf("invalid %q note %q: errors already in the repo must not block the change", it.Invalid, it.Note)
	}
}

func TestSummaryLine(t *testing.T) {
	for _, tc := range []struct {
		s    Summary
		errs int
		want string
	}{
		{Summary{Repos: 1}, 0, "Checked 1 repo: no findings."},
		{Summary{Repos: 3}, 1, "Checked 2 of 3 repos: no findings, 1 failed."},
		{
			Summary{Repos: 5, Findings: 4, ReposWithFindings: 2, ByAction: map[Action]int{ActionPR: 3, ActionCheck: 1}},
			0,
			"Checked 5 repos: 4 findings in 2 repos (3 pr, 1 check).",
		},
		{
			Summary{Repos: 3, Findings: 1, ReposWithFindings: 1, ByAction: map[Action]int{ActionSkip: 1}},
			2,
			"Checked 1 of 3 repos: 1 finding in 1 repo (1 skip), 2 failed.",
		},
	} {
		if got := summaryLine(tc.s, tc.errs); got != tc.want {
			t.Errorf("summaryLine(%+v, %d) = %q, want %q", tc.s, tc.errs, got, tc.want)
		}
	}
}

func TestSummaryChanges(t *testing.T) {
	s := Summary{ByAction: map[Action]int{ActionReport: 2, ActionCheck: 1}}
	if s.Changes() != 0 {
		t.Errorf("report and check items are not changes, got %d", s.Changes())
	}
	s.ByAction[ActionPR], s.ByAction[ActionApply] = 1, 2
	if s.Changes() != 3 {
		t.Errorf("Changes() = %d, want 3", s.Changes())
	}
}
