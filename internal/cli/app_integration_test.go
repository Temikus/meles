package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/temikus/meles/internal/plan"
)

// Runs plan as a real GitHub App, because installation tokens see a
// different API surface than user tokens (no /user, no /user/repos).
func TestIntegrationAppPlan(t *testing.T) {
	full, id, keyFile := os.Getenv("MELES_IT_REPO"), os.Getenv("MELES_IT_APP_ID"), os.Getenv("MELES_IT_APP_KEY_FILE")
	if os.Getenv("MELES_INTEGRATION") == "" || full == "" || id == "" || keyFile == "" {
		t.Skip("set MELES_INTEGRATION=1, MELES_IT_REPO, MELES_IT_APP_ID and MELES_IT_APP_KEY_FILE")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MELES_APP_ID", id)
	t.Setenv("MELES_APP_PRIVATE_KEY", string(key))
	owner, name, _ := strings.Cut(full, "/")

	ctl := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ctl, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ctl, "templates", "hello.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	yml := fmt.Sprintf(`version: 1
selectors:
  sandbox: { name: %q }
policies:
  - id: governance.files
    on: sandbox
    with:
      files:
        meles-it-never-created.txt: { template: templates/hello.txt }
  - id: security.features
    on: sandbox
    with: { secretScanning: true }
`, name)
	if err := os.WriteFile(filepath.Join(ctl, "meles.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, "plan", "--json", "-c", filepath.Join(ctl, "meles.yml"), "--owner", owner, "--repo", full, "--cache-dir", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var p plan.Plan
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	if p.Summary.Repos != 1 {
		t.Fatalf("the App saw %d repos matching %s, want 1; is it installed there?", p.Summary.Repos, full)
	}
	for _, rp := range p.Repos {
		if len(rp.Errors) > 0 {
			t.Errorf("%s: %v", rp.Repo, rp.Errors)
		}
	}
	found := false
	for _, rp := range p.Repos {
		for _, it := range rp.Items {
			found = found || it.Finding.Subject == "file:meles-it-never-created.txt"
		}
	}
	if !found {
		t.Errorf("missing the governance.files finding; plan: %s", out)
	}
	t.Logf("warnings: %q", p.Warnings)
}
