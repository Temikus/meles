package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
		{"no command", nil, "Usage:"},
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
