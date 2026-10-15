package exempt

import (
	"os"
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	layout := time.DateOnly
	if len(s) > len(layout) {
		layout = time.RFC3339
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		panic(err)
	}
	return t
}

const valid = `
exempt:
  - policy: ci.shape
    reason: "custom cross-compile matrix, see #42"
    until: 2027-01-01
  - policy: governance.standards
    subject: codeowners
    reason: owned by the platform team
    until: 2026-06-30
`

func TestParse(t *testing.T) {
	es, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	want := []Exemption{
		{Policy: "ci.shape", Reason: "custom cross-compile matrix, see #42", Until: day("2027-01-01"), Line: 3},
		{Policy: "governance.standards", Subject: "codeowners", Reason: "owned by the platform team", Until: day("2026-06-30"), Line: 6},
	}
	if len(es) != len(want) {
		t.Fatalf("got %d exemptions, want %d: %+v", len(es), len(want), es)
	}
	for i := range want {
		if es[i] != want[i] {
			t.Errorf("exemption %d = %+v, want %+v", i, es[i], want[i])
		}
	}
	if es, err := Parse(nil); err != nil || len(es) != 0 {
		t.Errorf("empty file: %v, %v", es, err)
	}
}

func TestParseExample(t *testing.T) {
	b, err := os.ReadFile("../../examples/.meles.yml")
	if err != nil {
		t.Fatal(err)
	}
	if es, err := Parse(b); err != nil || len(es) != 2 || es[1].Subject != "codeowners" {
		t.Errorf("examples/.meles.yml: %+v, %v", es, err)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"missing reason", "exempt: [{policy: ci.shape, until: 2027-01-01}]", "reason is required"},
		{"missing until", "exempt: [{policy: ci.shape, reason: x}]", "until is required"},
		{"missing policy", "exempt: [{reason: x, until: 2027-01-01}]", "policy is required"},
		{"bad date", "exempt: [{policy: ci.shape, reason: x, until: next year}]", "YYYY-MM-DD"},
		{"unknown field", "exempt: [{policy: ci.shape, reason: x, until: 2027-01-01, forever: true}]", "forever"},
		{"unknown top-level key", "exclude: []", "exclude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestFind(t *testing.T) {
	es, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kind, subject, today string
		active, expired            bool
	}{
		{"whole policy", "ci.shape", "file:.github/workflows/ci.yml", "2026-10-15", true, false},
		{"last day still counts", "ci.shape", "x", "2027-01-01", true, false},
		{"last day, late evening", "ci.shape", "x", "2027-01-01T23:59:00Z", true, false},
		{"day after expires", "ci.shape", "x", "2027-01-02", false, true},
		{"subject matches", "governance.standards", "codeowners", "2026-06-01", true, false},
		{"other subject", "governance.standards", "license", "2026-06-01", false, false},
		{"expired subject", "governance.standards", "codeowners", "2026-10-15", false, true},
		{"other policy", "deps.updater", "x", "2026-06-01", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			active, expired := Find(es, tc.kind, tc.subject, day(tc.today))
			if (active != nil) != tc.active || (expired != nil) != tc.expired {
				t.Errorf("active=%v expired=%v, want %v %v", active, expired, tc.active, tc.expired)
			}
		})
	}
}
