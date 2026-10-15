package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v92/github"

	"github.com/temikus/meles/internal/ghclient"
)

// Runs meles apply against a real repo, because the executor relies on PR and
// ref behaviour that is poorly documented. Each run uses its own branch, so
// closes from earlier runs cannot count as accepted here.
func TestIntegrationApply(t *testing.T) {
	full := os.Getenv("MELES_IT_REPO")
	if os.Getenv("MELES_INTEGRATION") == "" || full == "" {
		t.Skip("set MELES_INTEGRATION=1 and MELES_IT_REPO=owner/name")
	}
	owner, name, _ := strings.Cut(full, "/")
	ctx := context.Background()
	token, err := ghclient.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gh, _, err := ghclient.New(token, ghclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	old := writePause
	writePause = 0
	t.Cleanup(func() { writePause = old })

	branch := "meles-it/" + time.Now().UTC().Format("20060102-150405")
	ctl, cache := t.TempDir(), t.TempDir()
	t.Cleanup(func() { cleanup(ctx, t, gh, owner, name, branch) })
	if err := os.MkdirAll(filepath.Join(ctl, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctl, "templates", "hello.txt"), []byte("hello from meles to {{.FullName}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := func(onClose string, paths ...string) {
		t.Helper()
		var entries strings.Builder
		for _, p := range paths {
			fmt.Fprintf(&entries, "      %s: { template: templates/hello.txt%s }\n", p, onClose)
		}
		yml := fmt.Sprintf("version: 1\ndefaults:\n  pr:\n    branch: %s\n    labels: [meles-it]\nselectors:\n  sandbox: { name: %q }\npolicies:\n  - id: governance.files\n    on: sandbox\n    with:\n%s",
			branch, name, entries.String())
		if err := os.WriteFile(filepath.Join(ctl, "meles.yml"), []byte(yml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writes := regexp.MustCompile(`(\d+) writes`)
	opened := regexp.MustCompile(`opened #(\d+)`)
	apply := func(step string) (string, int) {
		t.Helper()
		code, out, errOut := run(t, "apply", "-c", filepath.Join(ctl, "meles.yml"), "--owner", owner, "--repo", full, "--cache-dir", cache, "-v", "--yes")
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s\n%s", step, code, out, errOut)
		}
		m := writes.FindStringSubmatch(errOut)
		if m == nil {
			t.Fatalf("%s: no write count in %q", step, errOut)
		}
		n, _ := strconv.Atoi(m[1])
		t.Logf("%s: %d writes\n%s", step, n, out)
		return out, n
	}
	prNumber := func(step, out string) int {
		t.Helper()
		m := opened.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("%s: no PR opened:\n%s", step, out)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}

	config("", ".meles-it/hello.txt")
	out, _ := apply("first run")
	first := prNumber("first run", out)

	if _, n := apply("no drift"); n != 0 {
		t.Errorf("no drift: %d writes, want 0", n)
	}

	config("", ".meles-it/hello.txt", ".meles-it/second.txt")
	out, _ = apply("second file")
	if !strings.Contains(out, "pushed 2 file(s)") || !strings.Contains(out, fmt.Sprintf("updated #%d", first)) || opened.MatchString(out) {
		t.Errorf("second file: want the same PR updated in place:\n%s", out)
	}

	// A human closes the PR. Both findings are onClose: accept (the default).
	if _, _, err := gh.PullRequests.Edit(ctx, owner, name, first, &github.PullRequest{State: new("closed")}); err != nil {
		t.Fatal(err)
	}
	out, n := apply("after accept close")
	if !strings.Contains(out, fmt.Sprintf("noted the accepted close on #%d", first)) || opened.MatchString(out) || n != 1 {
		t.Errorf("after accept close: want one comment and no new PR (%d writes):\n%s", n, out)
	}
	if _, n := apply("accepted, no drift"); n != 0 {
		t.Errorf("accepted, no drift: %d writes, want 0", n)
	}

	// The policy now says reopen: the findings come back in a fresh PR.
	config(", onClose: reopen", ".meles-it/hello.txt", ".meles-it/second.txt")
	out, _ = apply("reopen")
	if second := prNumber("reopen", out); second == first {
		t.Errorf("reopen: PR #%d reused; want a fresh PR", second)
	}
	if _, n := apply("reopened, no drift"); n != 0 {
		t.Errorf("reopened, no drift: %d writes, want 0", n)
	}
}

// Turns Dependabot alerts off, then checks that enforce mode turns them back
// on and that a second run makes no writes. Needs admin on the repo.
func TestIntegrationApplySettings(t *testing.T) {
	full := os.Getenv("MELES_IT_REPO")
	if os.Getenv("MELES_INTEGRATION") == "" || full == "" {
		t.Skip("set MELES_INTEGRATION=1 and MELES_IT_REPO=owner/name")
	}
	owner, name, _ := strings.Cut(full, "/")
	ctx := context.Background()
	token, err := ghclient.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gh, _, err := ghclient.New(token, ghclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := gh.Repositories.EnableVulnerabilityAlerts(ctx, owner, name); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})
	ctl, cache := t.TempDir(), t.TempDir()
	branch := "meles-it/" + time.Now().UTC().Format("20060102-150405")
	yml := fmt.Sprintf("version: 1\ndefaults:\n  pr:\n    branch: %s\nselectors:\n  sandbox: { name: %q }\npolicies:\n  - id: security.features\n    on: sandbox\n    mode: enforce\n    with:\n      dependabotAlerts: true\n", branch, name)
	if err := os.WriteFile(filepath.Join(ctl, "meles.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	writes := regexp.MustCompile(`(\d+) writes`)
	apply := func(step string) (string, int) {
		t.Helper()
		code, out, errOut := run(t, "apply", "-c", filepath.Join(ctl, "meles.yml"), "--owner", owner, "--repo", full, "--cache-dir", cache, "-v", "--yes")
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s\n%s", step, code, out, errOut)
		}
		m := writes.FindStringSubmatch(errOut)
		if m == nil {
			t.Fatalf("%s: no write count in %q", step, errOut)
		}
		n, _ := strconv.Atoi(m[1])
		t.Logf("%s: %d writes\n%s", step, n, out)
		return out, n
	}
	// Reads of this setting flap between old and new for some seconds after
	// a write, so wait for five matching reads in a row.
	settle := func(want bool) {
		t.Helper()
		streak := 0
		for range 60 {
			if on, _, err := gh.Repositories.GetVulnerabilityAlerts(ctx, owner, name); err == nil && on == want {
				if streak++; streak == 5 {
					return
				}
			} else {
				streak = 0
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("alerts never settled as %v", want)
	}
	if _, err := gh.Repositories.EnableVulnerabilityAlerts(ctx, owner, name); err != nil {
		t.Fatal(err)
	}
	settle(true)
	if _, n := apply("already on"); n != 0 {
		t.Errorf("already on: %d writes, want 0", n)
	}
	if _, err := gh.Repositories.DisableVulnerabilityAlerts(ctx, owner, name); err != nil {
		t.Fatal(err)
	}
	settle(false)
	if out, n := apply("switched off"); n != 1 || !strings.Contains(out, "set security.dependabotAlerts to true") {
		t.Errorf("switched off: want one write that turns alerts on (%d writes)", n)
	}
	settle(true)
	if _, n := apply("no drift"); n != 0 {
		t.Errorf("no drift: %d writes, want 0", n)
	}
}

// cleanup closes any PR left open on the test branch and deletes the branch.
func cleanup(ctx context.Context, t *testing.T, gh *github.Client, owner, name, branch string) {
	prs, _, err := gh.PullRequests.List(ctx, owner, name, &github.PullRequestListOptions{State: "open", Head: owner + ":" + branch})
	if err != nil {
		t.Logf("cleanup: %v", err)
	}
	for _, pr := range prs {
		if _, _, err := gh.PullRequests.Edit(ctx, owner, name, pr.GetNumber(), &github.PullRequest{State: new("closed")}); err != nil {
			t.Logf("cleanup: close #%d: %v", pr.GetNumber(), err)
		}
	}
	if _, err := gh.Git.DeleteRef(ctx, owner, name, "heads/"+branch); err != nil {
		t.Logf("cleanup: delete %s: %v", branch, err)
	}
}
