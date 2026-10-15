package plan

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/temikus/meles/internal/exempt"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/selector"
)

// Collector fetches one repo's facts.
type Collector interface {
	Collect(ctx context.Context, m facts.Meta, n facts.Needs) (*facts.Repo, error)
}

// RunOptions tunes Run.
type RunOptions struct {
	Workers int
	Today   time.Time
	// Needs is added to every repo's needs, e.g. Branch for apply.
	Needs facts.Needs
}

// Run collects facts for every repo some instance could match, then evaluates.
// A repo that fails to collect is reported, not fatal. It also returns the
// collected repos, clean ones included.
func Run(ctx context.Context, insts []Instance, metas []facts.Meta, col Collector, o RunOptions) (Plan, []*facts.Repo) {
	type job struct {
		meta  facts.Meta
		needs facts.Needs
	}
	var jobs []job
	for _, m := range metas {
		// Branch facts are left out on purpose: only apply needs them, and
		// they cost 2-3 requests per repo.
		n := o.Needs.Merge(facts.Needs{Paths: []string{exempt.Path}, PRs: true})
		wanted := false
		for _, in := range insts {
			if selector.Prefilter(in.Selector, m) {
				n = n.Merge(in.Policy.Needs())
				wanted = true
			}
		}
		if wanted {
			jobs = append(jobs, job{m, n})
		}
	}

	var (
		mu     sync.Mutex
		repos  []*facts.Repo
		failed []RepoPlan
		wg     sync.WaitGroup
		ch     = make(chan job)
	)
	for range max(o.Workers, 1) {
		wg.Go(func() {
			for j := range ch {
				r, err := col.Collect(ctx, j.meta, j.needs)
				mu.Lock()
				if err != nil {
					failed = append(failed, RepoPlan{Repo: j.meta.FullName(), Errors: []string{err.Error()}})
				} else {
					repos = append(repos, r)
				}
				mu.Unlock()
			}
		})
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		ch <- j
	}
	close(ch)
	wg.Wait()

	slices.SortFunc(repos, func(a, b *facts.Repo) int { return cmp.Compare(a.FullName(), b.FullName()) })
	p := Evaluate(insts, repos, o.Today)
	p.Repos = append(p.Repos, failed...)
	slices.SortFunc(p.Repos, func(a, b RepoPlan) int { return cmp.Compare(a.Repo, b.Repo) })
	p.Summary.Repos = len(metas)
	p.recount()
	return p, repos
}
