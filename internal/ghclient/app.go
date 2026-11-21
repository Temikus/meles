package ghclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v92/github"

	"github.com/temikus/meles/internal/buildinfo"
)

// App is a GitHub App meles signs in as.
type App struct {
	ID  int64
	Key []byte // PEM private key
}

// AppFromEnv reads MELES_APP_ID and MELES_APP_PRIVATE_KEY. It returns nil
// when neither is set, so meles falls back to a user token.
func AppFromEnv() (*App, error) {
	id, key := os.Getenv("MELES_APP_ID"), os.Getenv("MELES_APP_PRIVATE_KEY")
	if id == "" && key == "" {
		return nil, nil
	}
	if id == "" || key == "" {
		return nil, errors.New("set both MELES_APP_ID and MELES_APP_PRIVATE_KEY to run as a GitHub App")
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("MELES_APP_ID: %q is not a number", id)
	}
	return &App{ID: n, Key: []byte(key)}, nil
}

// Installation is one account the App is installed on, with what it may do.
// Permission values are "", "read" or "write".
type Installation struct {
	ID             int64
	Account        string
	Administration string
	Workflows      string
}

// FindInstallation picks the installation on owner, or the only one when
// owner is empty. baseURL is empty for api.github.com.
func (a *App) FindInstallation(ctx context.Context, owner, baseURL string) (Installation, error) {
	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, a.ID, a.Key)
	if err != nil {
		return Installation{}, fmt.Errorf("app key: %w", err)
	}
	opts := []github.ClientOptionsFunc{github.WithHTTPClient(&http.Client{Transport: atr, Timeout: 60 * time.Second})}
	if baseURL != "" {
		opts = append(opts, github.WithURLs(&baseURL, &baseURL))
	}
	gh, err := github.NewClient(opts...)
	if err != nil {
		return Installation{}, err
	}
	var all []*github.Installation
	opt := &github.ListOptions{PerPage: 100}
	for {
		page, resp, err := gh.Apps.ListInstallations(ctx, opt)
		if err != nil {
			return Installation{}, fmt.Errorf("list app installations: %w", err)
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	var match []*github.Installation
	for _, in := range all {
		if owner == "" || strings.EqualFold(in.GetAccount().GetLogin(), owner) {
			match = append(match, in)
		}
	}
	switch {
	case len(all) == 0:
		return Installation{}, fmt.Errorf("app %d has no installations; install it on an account first", a.ID)
	case len(match) == 0:
		return Installation{}, fmt.Errorf("app %d is not installed on %s", a.ID, owner)
	case len(match) > 1:
		return Installation{}, fmt.Errorf("app %d is installed on %d accounts; pass --owner to pick one", a.ID, len(match))
	}
	in := match[0]
	p := in.GetPermissions()
	return Installation{ID: in.GetID(), Account: in.GetAccount().GetLogin(), Administration: p.GetAdministration(), Workflows: p.GetWorkflows()}, nil
}

// NewApp returns a client that signs in as the installation. The token
// renews itself, and the cache is keyed by installation, not token.
func NewApp(a *App, in Installation, o Options) (*github.Client, *Transport, error) {
	itr, err := ghinstallation.New(http.DefaultTransport, a.ID, in.ID, a.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("app key: %w", err)
	}
	if o.BaseURL != "" {
		itr.BaseURL = strings.TrimSuffix(o.BaseURL, "/")
	}
	tr := &Transport{Base: itr, Dir: o.CacheDir, Identity: fmt.Sprintf("app:%d", in.ID)}
	opts := []github.ClientOptionsFunc{
		github.WithHTTPClient(&http.Client{Transport: tr, Timeout: 60 * time.Second}),
		github.WithUserAgent("meles/" + buildinfo.Get().Version),
	}
	if o.BaseURL != "" {
		opts = append(opts, github.WithURLs(&o.BaseURL, &o.BaseURL))
	}
	c, err := github.NewClient(opts...)
	if err != nil {
		return nil, nil, err
	}
	return c, tr, nil
}
