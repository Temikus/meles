package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v92/github"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/ghclient"
	"github.com/temikus/meles/internal/inventory"
	"github.com/temikus/meles/internal/plan"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// pipelineFlags are shared by plan and apply.
type pipelineFlags struct {
	cfgPath  *string
	owners   stringList
	repos    stringList
	cacheDir *string
	verbose  *bool
}

func addPipelineFlags(fs *flag.FlagSet) *pipelineFlags {
	f := &pipelineFlags{
		cfgPath:  fs.String("c", "meles.yml", "path to meles.yml in the control repo"),
		cacheDir: fs.String("cache-dir", ghclient.DefaultCacheDir(), "HTTP and blob cache (empty disables)"),
		verbose:  fs.Bool("v", false, "print API and cache counts to stderr"),
	}
	fs.Var(&f.owners, "owner", "user or org to plan for (repeatable; default: the token's user)")
	fs.Var(&f.repos, "repo", "only repos whose owner/name matches this glob (repeatable)")
	return f
}

// run is one pass of the pipeline, everything short of writing.
type pipelineRun struct {
	cfg   *config.Config
	gh    *github.Client
	tr    *ghclient.Transport
	blobs *ghclient.BlobStore
	plan  plan.Plan
	repos []*facts.Repo
}

func pipeline(ctx context.Context, f *pipelineFlags, needs facts.Needs, stderr io.Writer) (*pipelineRun, error) {
	cfg, insts, warnings, err := load(*f.cfgPath)
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	token, err := ghclient.Token(ctx)
	if err != nil {
		return nil, err
	}
	bare, _, err := ghclient.New(token, ghclient.Options{})
	if err != nil {
		return nil, err
	}
	login, err := inventory.Login(ctx, bare)
	if err != nil {
		return nil, err
	}
	gh, tr, err := ghclient.New(token, ghclient.Options{CacheDir: *f.cacheDir, Identity: "user:" + login})
	if err != nil {
		return nil, err
	}
	owners := f.owners
	if len(owners) == 0 {
		owners = stringList{login}
	}
	metas, err := inventory.List(ctx, gh, login, owners)
	if err != nil {
		return nil, err
	}
	metas = filterRepos(metas, f.repos)

	blobs := &ghclient.BlobStore{Dir: *f.cacheDir}
	col := &facts.Collector{GH: gh, Blobs: blobs, Branch: cfg.Defaults.PR.Branch}
	p, repos := plan.Run(ctx, insts, metas, col, plan.RunOptions{Workers: 4, Today: time.Now(), Needs: needs})
	p.ControlRef = controlRef(ctx, filepath.Dir(*f.cfgPath))
	p.Warnings = warnings
	if v, ok := plan.RenovateValidator(); ok {
		p.Validate(ctx, []plan.Validator{v})
	} else {
		fmt.Fprintln(stderr, "warning: renovate-config-validator not found; generated Renovate configs are unchecked")
		if needs.Branch {
			p.Block(plan.IsRenovateConfig, "renovate-config-validator is not installed; apply refuses unvalidated Renovate configs")
		}
	}
	if scopes, known := tr.Scopes(); known && !slices.Contains(scopes, "workflow") {
		p.Block(plan.IsWorkflow, "the token lacks the workflow scope that changes under .github/workflows/ need")
	}
	return &pipelineRun{cfg: cfg, gh: gh, tr: tr, blobs: blobs, plan: p, repos: repos}, ctx.Err()
}

func (r *pipelineRun) printStats(stderr io.Writer) {
	s := r.tr.Stats()
	fmt.Fprintf(stderr, "api: %d requests, %d not modified (free), %d uncacheable, %d GraphQL, %d writes; blobs: %d from cache\n",
		s.Requests, s.NotModified, s.Uncacheable, s.GraphQL, s.Writes, r.blobs.Hits())
}

func runPlan(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("plan", stderr)
	f := addPipelineFlags(fs)
	asJSON := fs.Bool("json", false, "print the plan as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	r, err := pipeline(ctx, f, facts.Needs{}, stderr)
	if err != nil {
		return err
	}
	if *f.verbose {
		r.printStats(stderr)
	}
	if *asJSON {
		return plan.WriteJSON(stdout, r.plan)
	}
	if err := plan.WriteTable(stdout, r.plan); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "Plan only, nothing changed. Run meles apply to make these changes.")
	return err
}

// filterRepos keeps repos matching any glob, case-insensitively. No globs
// keeps all.
func filterRepos(metas []facts.Meta, globs []string) []facts.Meta {
	if len(globs) == 0 {
		return metas
	}
	var out []facts.Meta
	for _, m := range metas {
		for _, g := range globs {
			if ok, _ := path.Match(strings.ToLower(g), strings.ToLower(m.FullName())); ok {
				out = append(out, m)
				break
			}
		}
	}
	return out
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
