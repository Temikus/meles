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
	// Branch is the housekeeping branch, for Needs.PRs and Needs.Branch.
	Branch string
}

// Collect fetches the tree, the content of needed paths, and what else n asks
// for. Calls: tree, blobs not yet cached; with Security: repo,
// vulnerability-alerts, private-vulnerability-reporting; with Standards: one
// GraphQL query; with Readme: readme; with PRs: pulls; with Branch: branches,
// matching-refs and, if the branch exists, its head commit.
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
	if n.Standards {
		if err := c.standards(ctx, r); err != nil {
			return nil, fmt.Errorf("standards: %w", err)
		}
	}
	if n.Readme {
		if err := c.readme(ctx, r); err != nil {
			return nil, fmt.Errorf("readme: %w", err)
		}
	}
	if n.PRs {
		if err := c.prs(ctx, r); err != nil {
			return nil, fmt.Errorf("pulls: %w", err)
		}
	}
	if n.Branch && r.DefaultBranch != "" {
		if err := c.branch(ctx, r); err != nil {
			return nil, fmt.Errorf("branch: %w", err)
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

// A license GitHub cannot identify is NOASSERTION, not null, so it still
// counts. codeowners without refName returned null for a fork that has one,
// so the ref is explicit.
const standardsQuery = `query($owner: String!, $name: String!, $ref: String) {
  repository(owner: $owner, name: $name) {
    isSecurityPolicyEnabled
    licenseInfo { spdxId }
    codeOfConduct { key }
    contributingGuidelines { resourcePath }
    codeowners(refName: $ref) { errors { kind } }
  }
}`

type present struct{}

func (c *Collector) standards(ctx context.Context, r *Repo) error {
	vars := map[string]any{"owner": r.Owner, "name": r.Name}
	if r.DefaultBranch != "" {
		vars["ref"] = "refs/heads/" + r.DefaultBranch
	}
	body := map[string]any{"query": standardsQuery, "variables": vars}
	req, err := c.GH.NewRequest(ctx, http.MethodPost, "graphql", body)
	if err != nil {
		return err
	}
	var out struct {
		Data struct {
			Repository *struct {
				IsSecurityPolicyEnabled *bool    `json:"isSecurityPolicyEnabled"`
				LicenseInfo             *present `json:"licenseInfo"`
				CodeOfConduct           *present `json:"codeOfConduct"`
				ContributingGuidelines  *present `json:"contributingGuidelines"`
				Codeowners              *present `json:"codeowners"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if _, err := c.GH.Do(req, &out); err != nil {
		return err
	}
	if len(out.Errors) > 0 {
		return errors.New(out.Errors[0].Message)
	}
	g := out.Data.Repository
	if g == nil {
		return errors.New("repository not found")
	}
	r.Standards.SecurityPolicy = g.IsSecurityPolicyEnabled != nil && *g.IsSecurityPolicyEnabled
	r.Standards.License = g.LicenseInfo != nil
	r.Standards.CodeOfConduct = g.CodeOfConduct != nil
	r.Standards.Contributing = g.ContributingGuidelines != nil
	r.Standards.Codeowners = g.Codeowners != nil
	return nil
}

// readme asks GitHub which README it displays, so its location rules stay
// GitHub's.
func (c *Collector) readme(ctx context.Context, r *Repo) error {
	rc, _, err := c.GH.Repositories.GetReadme(ctx, r.Owner, r.Name, nil)
	if status(err) == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	content, err := rc.GetContent()
	if err != nil {
		return err
	}
	r.Standards.Readme = rc.GetPath()
	r.Files[rc.GetPath()] = []byte(content)
	return nil
}

func (c *Collector) prs(ctx context.Context, r *Repo) error {
	opts := &github.PullRequestListOptions{State: "all", Head: r.Owner + ":" + c.Branch, ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, resp, err := c.GH.PullRequests.List(ctx, r.Owner, r.Name, opts)
		if err != nil {
			return err
		}
		for _, p := range page {
			pr := PR{Number: p.GetNumber(), Title: p.GetTitle(), Body: p.GetBody(), Open: p.GetState() == "open", Merged: p.MergedAt != nil}
			for _, l := range p.Labels {
				pr.Labels = append(pr.Labels, l.GetName())
			}
			r.Housekeeping.PRs = append(r.Housekeeping.PRs, pr)
		}
		if resp.NextPage == 0 {
			return nil
		}
		opts.Page = resp.NextPage
	}
}

// branch reads the base and the housekeeping branch head. matching-refs
// answers 200 [] with an ETag for a missing branch, so absence is free on
// later runs; a 404 from git/ref would cost a request every run.
func (c *Collector) branch(ctx context.Context, r *Repo) error {
	base, _, err := c.GH.Repositories.GetBranch(ctx, r.Owner, r.Name, r.DefaultBranch, 1)
	if err != nil {
		return err
	}
	r.Housekeeping.BaseSHA = base.GetCommit().GetSHA()
	r.Housekeeping.BaseTree = base.GetCommit().GetCommit().GetTree().GetSHA()

	refs, _, err := c.GH.Git.ListMatchingRefs(ctx, r.Owner, r.Name, "heads/"+c.Branch)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.GetRef() != "refs/heads/"+c.Branch {
			continue // matching-refs is a prefix match
		}
		commit, _, err := c.GH.Repositories.GetCommit(ctx, r.Owner, r.Name, ref.GetObject().GetSHA(), nil)
		if err != nil {
			return err
		}
		b := &Branch{SHA: commit.GetSHA(), Message: commit.GetCommit().GetMessage(), Files: map[string]string{}}
		for _, p := range commit.Parents {
			b.Parents = append(b.Parents, p.GetSHA())
		}
		for _, f := range commit.Files {
			switch f.GetStatus() {
			case "removed":
				b.Files[f.GetFilename()] = ""
			case "renamed":
				b.Files[f.GetPreviousFilename()] = ""
				b.Files[f.GetFilename()] = f.GetSHA()
			default:
				b.Files[f.GetFilename()] = f.GetSHA()
			}
		}
		r.Housekeeping.Branch = b
	}
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
