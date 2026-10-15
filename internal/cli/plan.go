package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/ghclient"
	"github.com/temikus/meles/internal/inventory"
	"github.com/temikus/meles/internal/plan"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runPlan(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("plan", stderr)
	cfgPath := fs.String("c", "meles.yml", "path to meles.yml in the control repo")
	var owners stringList
	fs.Var(&owners, "owner", "user or org to plan for (repeatable; default: the token's user)")
	asJSON := fs.Bool("json", false, "print the plan as JSON")
	cacheDir := fs.String("cache-dir", ghclient.DefaultCacheDir(), "HTTP and blob cache (empty disables)")
	verbose := fs.Bool("v", false, "print API and cache counts to stderr")
	if err := parse(fs, args); err != nil {
		return err
	}

	_, insts, warnings, err := load(*cfgPath)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}

	token, err := ghclient.Token(ctx)
	if err != nil {
		return err
	}
	bare, _, err := ghclient.New(token, ghclient.Options{})
	if err != nil {
		return err
	}
	login, err := inventory.Login(ctx, bare)
	if err != nil {
		return err
	}
	gh, tr, err := ghclient.New(token, ghclient.Options{CacheDir: *cacheDir, Identity: "user:" + login})
	if err != nil {
		return err
	}
	if len(owners) == 0 {
		owners = stringList{login}
	}
	metas, err := inventory.List(ctx, gh, login, owners)
	if err != nil {
		return err
	}

	blobs := &ghclient.BlobStore{Dir: *cacheDir}
	p := plan.Run(ctx, insts, metas, &facts.Collector{GH: gh, Blobs: blobs}, 4, time.Now())
	p.ControlRef = controlRef(ctx, filepath.Dir(*cfgPath))
	p.Warnings = warnings
	if v, ok := plan.RenovateValidator(); ok {
		p.Validate(ctx, []plan.Validator{v})
	} else {
		fmt.Fprintln(stderr, "warning: renovate-config-validator not found; generated Renovate configs are unchecked")
	}
	if *verbose {
		s := tr.Stats()
		fmt.Fprintf(stderr, "api: %d requests, %d not modified (free), %d uncacheable, %d GraphQL; blobs: %d from cache\n",
			s.Requests, s.NotModified, s.Uncacheable, s.GraphQL, blobs.Hits())
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if *asJSON {
		return plan.WriteJSON(stdout, p)
	}
	return plan.WriteTable(stdout, p)
}

// load reads, validates and builds the config: everything short of the API.
func load(path string) (*config.Config, []plan.Instance, []string, error) {
	cfg, ctl, err := config.Load(path)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, nil, err
	}
	insts, warnings, err := plan.Build(cfg, ctl)
	if err != nil {
		return nil, nil, warnings, err
	}
	return cfg, insts, warnings, nil
}

// controlRef is the control repo commit templates were read from, if it is a git checkout.
func controlRef(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
