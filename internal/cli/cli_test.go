package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temikus/meles/internal/buildinfo"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func setBuildinfo(t *testing.T, v, c, d string) {
	t.Helper()
	old := [3]string{buildinfo.Version, buildinfo.Commit, buildinfo.Date}
	buildinfo.Version, buildinfo.Commit, buildinfo.Date = v, c, d
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit, buildinfo.Date = old[0], old[1], old[2] })
}

func TestVersion(t *testing.T) {
	setBuildinfo(t, "v1.2.3", "abc123", "2026-10-14T00:00:00Z")

	code, out, _ := run(t, "version")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if want := "meles v1.2.3 (commit abc123, built 2026-10-14T00:00:00Z)\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestVersionJSON(t *testing.T) {
	setBuildinfo(t, "v1.2.3", "abc123", "2026-10-14T00:00:00Z")

	code, out, _ := run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got buildinfo.Info
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if want := (buildinfo.Info{Version: "v1.2.3", Commit: "abc123", Date: "2026-10-14T00:00:00Z"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown command", []string{"frobnicate"}, `unknown command "frobnicate"`},
		{"unknown flag", []string{"version", "--nope"}, "flag provided but not defined"},
		{"extra args", []string{"version", "extra"}, "unexpected arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := run(t, tc.args...)
			if code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("stderr %q does not contain %q", errOut, tc.want)
			}
		})
	}
}

func TestValidateExamples(t *testing.T) {
	code, out, errOut := run(t, "validate", "-c", "../../examples/meles.yml")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "ok (3 policies active, 5 skipped)") {
		t.Errorf("stdout = %q", out)
	}
	for _, kind := range []string{"governance.settings", "ci.shape[go]", "ci.shape[python]", "actions.pinning", "release.tooling"} {
		if !strings.Contains(errOut, "policy "+kind+" is not implemented yet") {
			t.Errorf("no skip warning for %s in %q", kind, errOut)
		}
	}
}

func TestValidateReportsErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "meles.yml")
	body := "version: 1\nselectors: {all: {}}\npolicies:\n  - id: governance.files\n    on: all\n    with: {SECURITY.md: {template: nope.md}}\n  - id: made.up\n    on: all\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := run(t, "validate", "-c", cfg)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	for _, want := range []string{"meles.yml:4: policy governance.files:", `meles.yml:7: unknown policy kind "made.up"`} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q missing %q", errOut, want)
		}
	}
}

func TestPlanIsDefault(t *testing.T) {
	code, _, errOut := run(t, "-c", "/nonexistent/meles.yml")
	if code != 1 || !strings.Contains(errOut, "/nonexistent/meles.yml") {
		t.Errorf("exit %d stderr %q: flags without a command should run plan", code, errOut)
	}
}
