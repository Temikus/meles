package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/temikus/meles/internal/apply"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/plan"
)

// writePause spaces repos apart: GitHub's secondary rate limit punishes
// bursts of content-creating requests.
var writePause = time.Second

func runApply(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("apply", stderr)
	f := addPipelineFlags(fs)
	if err := parse(fs, args); err != nil {
		return err
	}
	r, err := pipeline(ctx, f, facts.Needs{Branch: true}, stderr)
	if err != nil {
		return err
	}
	if err := plan.WriteTable(stdout, r.plan); err != nil {
		return err
	}
	if n := r.plan.Summary.ByAction[plan.ActionApply]; n > 0 {
		fmt.Fprintf(stderr, "warning: %d setting changes are not applied yet: this version of apply only opens PRs\n", n)
	}

	opts := apply.Options{
		Branch: r.cfg.Defaults.PR.Branch, Labels: r.cfg.Defaults.PR.Labels,
		ControlRef: r.plan.ControlRef, Today: time.Now(),
	}
	byRepo := map[string]plan.RepoPlan{}
	for _, rp := range r.plan.Repos {
		byRepo[rp.Repo] = rp
	}
	var ds []apply.Decision
	for _, repo := range r.repos {
		ds = append(ds, apply.Decide(byRepo[repo.FullName()], repo, opts))
	}
	ds = apply.Limit(ds, r.cfg.Defaults.PR.MaxPerRun)

	ex := &apply.Executor{GH: r.gh, Opts: opts}
	var changed, blocked, deferred, failed int
	fmt.Fprintln(stdout)
	for _, d := range ds {
		name := d.Repo.FullName()
		switch {
		case d.Kind == apply.Blocked:
			blocked++
			fmt.Fprintf(stdout, "%s: blocked: %s\n", name, d.Reason)
			continue
		case d.Kind == apply.Deferred:
			deferred++
			fmt.Fprintf(stdout, "%s: deferred: %s\n", name, d.Reason)
			continue
		case !d.Writes():
			continue
		}
		if changed+failed > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(writePause):
			}
		}
		done, err := ex.Execute(ctx, d)
		for _, l := range done {
			fmt.Fprintf(stdout, "%s: %s\n", name, l)
		}
		if err != nil {
			failed++
			fmt.Fprintf(stdout, "%s: error: %v\n", name, err)
			continue
		}
		changed++
	}
	if *f.verbose {
		r.printStats(stderr)
	}
	fmt.Fprintf(stdout, "\nApplied: %d repos changed, %d blocked, %d deferred, %d failed.\n", changed, blocked, deferred, failed)
	if failed > 0 {
		return fmt.Errorf("%d repos failed", failed)
	}
	return nil
}
