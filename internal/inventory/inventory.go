// Package inventory lists the repos meles looks at.
package inventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v92/github"

	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/ghclient"
)

// Login returns the authenticated user's login.
func Login(ctx context.Context, gh *github.Client) (string, error) {
	u, _, err := gh.Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("get authenticated user: %w", err)
	}
	return u.GetLogin(), nil
}

// List returns every repo of each owner. For the authenticated user that
// includes private repos; for other users only public ones.
func List(ctx context.Context, gh *github.Client, login string, owners []string) ([]facts.Meta, error) {
	var out []facts.Meta
	for _, owner := range owners {
		repos, err := listOwner(ctx, gh, login, owner)
		if err != nil {
			return nil, fmt.Errorf("list repos of %s: %w", owner, err)
		}
		for _, r := range repos {
			out = append(out, Meta(r))
		}
	}
	return out, nil
}

// ListInstallation returns the repos an App installation can see. admin says
// whether the App has the Administration permission, which decides whether
// security settings are readable.
func ListInstallation(ctx context.Context, gh *github.Client, admin bool) ([]facts.Meta, error) {
	opt := &github.ListOptions{PerPage: 100}
	repos, err := paginate(func(page int) ([]*github.Repository, *github.Response, error) {
		opt.Page = page
		l, resp, err := gh.Apps.ListRepos(ctx, opt)
		return l.GetRepositories(), resp, err
	})
	if err != nil {
		return nil, fmt.Errorf("list installation repos: %w", err)
	}
	out := make([]facts.Meta, 0, len(repos))
	for _, r := range repos {
		m := Meta(r)
		m.Admin = admin
		out = append(out, m)
	}
	return out, nil
}

func listOwner(ctx context.Context, gh *github.Client, login, owner string) ([]*github.Repository, error) {
	if strings.EqualFold(owner, login) {
		opt := &github.RepositoryListByAuthenticatedUserOptions{Affiliation: "owner", ListOptions: github.ListOptions{PerPage: 100}}
		return paginate(func(page int) ([]*github.Repository, *github.Response, error) {
			opt.Page = page
			return gh.Repositories.ListByAuthenticatedUser(ctx, opt)
		})
	}
	u, _, err := gh.Users.Get(ctx, owner)
	if err != nil {
		return nil, err
	}
	if u.GetType() == "Organization" {
		opt := &github.RepositoryListByOrgOptions{Type: "all", ListOptions: github.ListOptions{PerPage: 100}}
		return paginate(func(page int) ([]*github.Repository, *github.Response, error) {
			opt.Page = page
			return gh.Repositories.ListByOrg(ctx, owner, opt)
		})
	}
	opt := &github.RepositoryListByUserOptions{Type: "owner", ListOptions: github.ListOptions{PerPage: 100}}
	return paginate(func(page int) ([]*github.Repository, *github.Response, error) {
		opt.Page = page
		return gh.Repositories.ListByUser(ctx, owner, opt)
	})
}

func paginate(list func(page int) ([]*github.Repository, *github.Response, error)) ([]*github.Repository, error) {
	var all []*github.Repository
	page := 1
	for {
		repos, resp, err := list(page)
		if err != nil {
			return nil, err
		}
		if err := ghclient.CheckRate(resp); err != nil {
			return nil, err
		}
		all = append(all, repos...)
		if resp.NextPage == 0 {
			return all, nil
		}
		page = resp.NextPage
	}
}

// Meta converts an API repo into the facts listing view.
func Meta(r *github.Repository) facts.Meta {
	return facts.Meta{
		Owner:         r.GetOwner().GetLogin(),
		Name:          r.GetName(),
		DefaultBranch: r.GetDefaultBranch(),
		Visibility:    r.GetVisibility(),
		Language:      r.GetLanguage(),
		Topics:        r.Topics,
		Archived:      r.GetArchived(),
		Fork:          r.GetFork(),
		Admin:         r.GetPermissions().GetAdmin(),
	}
}
