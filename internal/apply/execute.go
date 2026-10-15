package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-github/v92/github"

	"github.com/temikus/meles/internal/policy"
)

// Executor makes a Decision's writes, in order, one repo at a time.
type Executor struct {
	GH   *github.Client
	Opts Options
}

// Execute returns one line per write made. On error, the lines say how far
// it got; a rerun picks up from GitHub's state, not from here.
func (e *Executor) Execute(ctx context.Context, d Decision) ([]string, error) {
	if !d.Writes() {
		return nil, nil
	}
	r := d.Repo
	var done []string
	for _, s := range d.Settings {
		if err := e.set(ctx, r.Owner, r.Name, s); err != nil {
			return done, fmt.Errorf("set %s: %w", s.Setting, err)
		}
		done = append(done, fmt.Sprintf("set %s to %v", s.Setting, s.To))
	}
	if !d.prWrites() {
		return done, nil
	}
	if d.Comment != nil {
		if _, _, err := e.GH.Issues.CreateComment(ctx, r.Owner, r.Name, d.Comment.PR, github.IssueCommentRequest{Body: d.Comment.Body}); err != nil {
			return done, fmt.Errorf("comment on #%d: %w", d.Comment.PR, err)
		}
		done = append(done, fmt.Sprintf("noted the accepted close on #%d", d.Comment.PR))
	}

	switch d.Kind {
	case Close:
		closed := "closed"
		if _, _, err := e.GH.PullRequests.Edit(ctx, r.Owner, r.Name, d.PR, &github.PullRequest{State: &closed, Body: &d.Body}); err != nil {
			return done, fmt.Errorf("close #%d: %w", d.PR, err)
		}
		done = append(done, fmt.Sprintf("closed #%d: nothing left to change", d.PR))
		if d.DeleteBranch {
			if _, err := e.GH.Git.DeleteRef(ctx, r.Owner, r.Name, "heads/"+e.Opts.Branch); err != nil {
				return done, fmt.Errorf("delete branch: %w", err)
			}
		}
		return done, nil
	case Write:
	default:
		return done, nil
	}

	if d.Rewrite {
		if err := e.commit(ctx, d); err != nil {
			return done, workflowHint(err)
		}
		done = append(done, fmt.Sprintf("pushed %d file(s) to %s", len(d.Files), e.Opts.Branch))
	}
	pr := d.PR
	switch {
	case d.CreatePR:
		p, _, err := e.GH.PullRequests.Create(ctx, r.Owner, r.Name, github.CreatePullRequest{
			Title: &d.Title, Head: e.Opts.Branch, Base: r.DefaultBranch, Body: &d.Body,
		})
		if err != nil {
			return done, fmt.Errorf("open PR: %w", err)
		}
		pr = p.GetNumber()
		done = append(done, fmt.Sprintf("opened #%d", pr))
	case d.EditPR:
		if _, _, err := e.GH.PullRequests.Edit(ctx, r.Owner, r.Name, pr, &github.PullRequest{Title: &d.Title, Body: &d.Body}); err != nil {
			return done, fmt.Errorf("update #%d: %w", pr, err)
		}
		done = append(done, fmt.Sprintf("updated #%d", pr))
	case d.Rewrite:
		done = append(done, fmt.Sprintf("updated #%d", pr))
	}
	if len(d.AddLabels) > 0 {
		if _, _, err := e.GH.Issues.AddLabelsToIssue(ctx, r.Owner, r.Name, pr, d.AddLabels); err != nil {
			return done, fmt.Errorf("label #%d: %w", pr, err)
		}
	}
	return done, nil
}

// set makes one setting change with the one call that owns it.
func (e *Executor) set(ctx context.Context, owner, name string, s policy.SettingChange) error {
	on, ok := s.To.(bool)
	if !ok {
		return fmt.Errorf("want a bool, got %T", s.To)
	}
	status := "disabled"
	if on {
		status = "enabled"
	}
	var err error
	switch s.Setting {
	case "security.secret_scanning":
		_, _, err = e.GH.Repositories.Edit(ctx, owner, name, &github.Repository{
			SecurityAndAnalysis: &github.SecurityAndAnalysis{SecretScanning: &github.SecretScanning{Status: &status}},
		})
	case "security.push_protection":
		_, _, err = e.GH.Repositories.Edit(ctx, owner, name, &github.Repository{
			SecurityAndAnalysis: &github.SecurityAndAnalysis{SecretScanningPushProtection: &github.SecretScanningPushProtection{Status: &status}},
		})
	case "security.dependabot_alerts":
		if on {
			_, err = e.GH.Repositories.EnableVulnerabilityAlerts(ctx, owner, name)
		} else {
			_, err = e.GH.Repositories.DisableVulnerabilityAlerts(ctx, owner, name)
		}
	case "security.private_vuln_reporting":
		if on {
			_, err = e.GH.Repositories.EnablePrivateReporting(ctx, owner, name)
		} else {
			_, err = e.GH.Repositories.DisablePrivateReporting(ctx, owner, name)
		}
	default:
		return errors.New("meles cannot change this setting yet")
	}
	return err
}

// commit writes the files as one commit on the base and points the branch at
// it. File content goes inline in the tree, so no blob calls.
func (e *Executor) commit(ctx context.Context, d Decision) error {
	r, hk := d.Repo, d.Repo.Housekeeping
	entries := make([]*github.TreeEntry, 0, len(d.Files))
	for _, f := range d.Files {
		te := &github.TreeEntry{Path: new(f.Path), Mode: new("100644"), Type: new("blob")}
		if !f.Delete {
			te.Content = new(string(f.Content))
		}
		entries = append(entries, te)
	}
	tree, _, err := e.GH.Git.CreateTree(ctx, r.Owner, r.Name, hk.BaseTree, entries)
	if err != nil {
		return fmt.Errorf("create tree: %w", err)
	}
	c, _, err := e.GH.Git.CreateCommit(ctx, r.Owner, r.Name, github.Commit{
		Message: &d.Message, Tree: &github.Tree{SHA: tree.SHA}, Parents: []*github.Commit{{SHA: &hk.BaseSHA}},
	}, nil)
	if err != nil {
		return fmt.Errorf("create commit: %w", err)
	}
	if hk.Branch == nil {
		_, _, err = e.GH.Git.CreateRef(ctx, r.Owner, r.Name, github.CreateRef{Ref: "refs/heads/" + e.Opts.Branch, SHA: c.GetSHA()})
	} else {
		_, _, err = e.GH.Git.UpdateRef(ctx, r.Owner, r.Name, "heads/"+e.Opts.Branch, github.UpdateRef{SHA: c.GetSHA(), Force: new(true)})
	}
	if err != nil {
		return fmt.Errorf("move branch: %w", err)
	}
	return nil
}

// workflowHint names the permission GitHub wants when it refuses a commit
// touching .github/workflows/.
func workflowHint(err error) error {
	var er *github.ErrorResponse
	if errors.As(err, &er) && strings.Contains(strings.ToLower(er.Message), "workflow") {
		return fmt.Errorf("%w (changes under .github/workflows/ need a PAT with the workflow scope, or an App with workflows: write)", err)
	}
	return err
}
