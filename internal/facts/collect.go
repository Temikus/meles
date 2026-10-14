package facts

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-github/v92/github"

	"github.com/temikus/meles/internal/ghclient"
)

// Collector fetches a repo snapshot.
type Collector struct {
	GH    *github.Client
	Blobs *ghclient.BlobStore
}

// Collect fetches the tree, the content of needed paths, and security
// toggles if asked. Calls: tree, blobs not yet cached, and with Security:
// repo, vulnerability-alerts, private-vulnerability-reporting.
func (c *Collector) Collect(ctx context.Context, m Meta, n Needs) (*Repo, error) {
	r := &Repo{Meta: m, Tree: map[string]string{}, Files: map[string][]byte{}}
	if err := c.tree(ctx, r); err != nil {
		return nil, err
	}
	for _, p := range n.Paths {
		sha, ok := r.Tree[p]
		if !ok {
			continue
		}
		b, err := c.blob(ctx, m, sha)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		r.Files[p] = b
	}
	if n.Security {
		if err := c.security(ctx, r); err != nil {
			return nil, fmt.Errorf("security: %w", err)
		}
	}
	return r, nil
}

func (c *Collector) tree(ctx context.Context, r *Repo) error {
	if r.DefaultBranch == "" {
		return nil
	}
	t, resp, err := c.GH.Git.GetTree(ctx, r.Owner, r.Name, r.DefaultBranch, true)
	if status(err) == http.StatusConflict || status(err) == http.StatusNotFound {
		return nil // empty repo: GitHub answers 409 when there are no commits
	}
	if err != nil {
		return fmt.Errorf("tree: %w", err)
	}
	if err := ghclient.CheckRate(resp); err != nil {
		return err
	}
	r.TreeTruncated = t.GetTruncated()
	for _, e := range t.Entries {
		if e.GetType() == "blob" {
			r.Tree[e.GetPath()] = e.GetSHA()
		}
	}
	return nil
}

func (c *Collector) blob(ctx context.Context, m Meta, sha string) ([]byte, error) {
	if b, ok := c.Blobs.Get(sha); ok {
		return b, nil
	}
	b, _, err := c.GH.Git.GetBlobRaw(ctx, m.Owner, m.Name, sha)
	if err != nil {
		return nil, err
	}
	_ = c.Blobs.Put(sha, b)
	return b, nil
}

func (c *Collector) security(ctx context.Context, r *Repo) error {
	if !r.Admin {
		return nil // all Unknown: these endpoints need admin
	}
	repo, _, err := c.GH.Repositories.Get(ctx, r.Owner, r.Name)
	if err != nil {
		return err
	}
	sa := repo.GetSecurityAndAnalysis()
	// An admin seeing no status means the feature is not offered (no GHAS).
	r.Security.SecretScanning = statusTri(sa.GetSecretScanning().GetStatus())
	r.Security.PushProtection = statusTri(sa.GetSecretScanningPushProtection().GetStatus())

	on, _, err := c.GH.Repositories.GetVulnerabilityAlerts(ctx, r.Owner, r.Name)
	if err != nil {
		return err
	}
	r.Security.DependabotAlerts = TriOf(on)

	if r.Visibility != "public" {
		r.Security.PrivateVulnReporting = Unavailable
		return nil
	}
	on, _, err = c.GH.Repositories.IsPrivateReportingEnabled(ctx, r.Owner, r.Name)
	if err != nil {
		return err
	}
	r.Security.PrivateVulnReporting = TriOf(on)
	return nil
}

func statusTri(s string) Tri {
	switch s {
	case "enabled":
		return On
	case "disabled":
		return Off
	}
	return Unavailable
}

func status(err error) int {
	var er *github.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		return er.Response.StatusCode
	}
	return 0
}
