package facts_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/ghclient"
	"github.com/temikus/meles/internal/housekeeping"
	"github.com/temikus/meles/internal/inventory"
)

// Runs against a real repo because the cache relies on GitHub behaviour that
// is poorly documented: which endpoints send ETags, and that 304s are free.
// The repo must hold LICENSE, README.md and renovate.json, and no SECURITY.md
// or CODEOWNERS. Its owner's public .github repo must hold a SECURITY.md.
func TestIntegrationCollectAndRevalidate(t *testing.T) {
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
	bare, _, err := ghclient.New(token, ghclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	login, err := inventory.Login(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	needs := facts.Needs{Paths: []string{"LICENSE", "README.md", "renovate.json"}, Security: true, Standards: true, Readme: true, PRs: true, Branch: true}

	// Each pass is a fresh client over the same cache dir, like two meles runs.
	pass := func() (*facts.Repo, ghclient.Stats, int64) {
		gh, tr, err := ghclient.New(token, ghclient.Options{CacheDir: dir, Identity: "user:" + login})
		if err != nil {
			t.Fatal(err)
		}
		metas, err := inventory.List(ctx, gh, login, []string{owner})
		if err != nil {
			t.Fatal(err)
		}
		var meta *facts.Meta
		for i := range metas {
			if strings.EqualFold(metas[i].Name, name) {
				meta = &metas[i]
			}
		}
		if meta == nil {
			t.Fatalf("inventory of %s has no %s", owner, name)
		}
		blobs := &ghclient.BlobStore{Dir: dir}
		// Branch "main" exercises the branch path without writing to the repo.
		r, err := (&facts.Collector{GH: gh, Blobs: blobs, Branch: "main"}).Collect(ctx, *meta, needs)
		if err != nil {
			t.Fatal(err)
		}
		return r, tr.Stats(), blobs.Hits()
	}

	r, first, _ := pass()
	r2, second, blobHits := pass()

	for _, p := range needs.Paths {
		if !r.Has(p) || len(r.Files[p]) == 0 {
			t.Errorf("%s missing from tree or files", p)
		}
		if !bytes.Equal(r.Files[p], r2.Files[p]) {
			t.Errorf("%s differs between passes", p)
		}
	}
	if r.Has("SECURITY.md") {
		t.Error("sandbox must not have SECURITY.md")
	}
	if !bytes.Contains(r.Files["renovate.json"], []byte("github>temikus/renovate-config")) {
		t.Errorf("renovate.json = %s", r.Files["renovate.json"])
	}
	// GitHub's own view: SECURITY.md is inherited from <owner>/.github.
	want := facts.Standards{License: true, SecurityPolicy: true, Readme: "README.md"}
	if r.Standards != want || r2.Standards != want {
		t.Errorf("standards = %+v then %+v, want %+v", r.Standards, r2.Standards, want)
	}
	hk := r.Housekeeping
	if hk.BaseSHA == "" || hk.BaseTree == "" || len(hk.PRs) != 0 {
		t.Errorf("housekeeping = %+v", hk)
	}
	if hk.Branch == nil || hk.Branch.SHA != hk.BaseSHA {
		t.Fatalf("branch main: %+v, want head %s", hk.Branch, hk.BaseSHA)
	}
	// meles decides "no drift" by comparing these without fetching content.
	if got, want := hk.Branch.Files["LICENSE"], housekeeping.BlobSHA(r.Files["LICENSE"]); got != want {
		t.Errorf("GitHub blob SHA %s, local %s", got, want)
	}
	if r.Visibility == "private" && (r.Security.SecretScanning != facts.Unavailable || r.Security.PrivateVulnReporting != facts.Unavailable) {
		t.Errorf("private repo without GHAS: security = %+v", r.Security)
	}
	if r.Security.DependabotAlerts == facts.Unknown {
		t.Error("dependabot alerts should be readable as admin")
	}

	cost := second.RateUsed - first.RateUsed
	t.Logf("first pass: %+v; second pass: %+v, cost %d, blob hits %d", first, second, cost, blobHits)
	if second.Requests != second.NotModified+second.Uncacheable+second.GraphQL {
		t.Errorf("second pass made %d requests, only %d were 304s, %d uncacheable and %d GraphQL", second.Requests, second.NotModified, second.Uncacheable, second.GraphQL)
	}
	if second.GraphQL != 1 {
		t.Errorf("want exactly 1 GraphQL query (standards), got %d", second.GraphQL)
	}
	if second.Uncacheable != 1 {
		t.Errorf("want exactly 1 uncacheable request (vulnerability-alerts 204), got %d", second.Uncacheable)
	}
	if blobHits != int64(len(needs.Paths)) {
		t.Errorf("blob hits = %d, want %d", blobHits, len(needs.Paths))
	}
	// Other tools sharing the token can add to the count, never subtract.
	if cost < second.Uncacheable || cost >= second.Requests {
		t.Errorf("second pass cost %d of quota for %d requests (%d uncacheable): 304s are being counted", cost, second.Requests, second.Uncacheable)
	}
}
