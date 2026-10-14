// Package ghclient builds the GitHub client: token lookup, ETag cache, blob store.
package ghclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-github/v92/github"

	"github.com/temikus/meles/internal/buildinfo"
)

// Options configure New.
type Options struct {
	CacheDir string // empty disables the HTTP cache
	Identity string // cache scope, e.g. "user:temikus"
	BaseURL  string // for tests and GHES; empty means api.github.com
}

// DefaultCacheDir is <user cache dir>/meles.
func DefaultCacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "meles")
}

// New returns a client whose transport caches by ETag under go-github's auth.
func New(token string, o Options) (*github.Client, *Transport, error) {
	tr := &Transport{Base: http.DefaultTransport, Dir: o.CacheDir, Identity: o.Identity}
	opts := []github.ClientOptionsFunc{
		github.WithHTTPClient(&http.Client{Transport: tr, Timeout: 60 * time.Second}),
		github.WithAuthToken(token),
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

// Token returns GH_TOKEN, GITHUB_TOKEN, or the output of `gh auth token`.
func Token(ctx context.Context) (string, error) {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v, nil
		}
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("gh auth token: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("no GH_TOKEN or GITHUB_TOKEN, and gh auth token failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
