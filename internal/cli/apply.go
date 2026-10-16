package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/temikus/meles/internal/apply"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/plan"
)

// writePause spaces repos apart: GitHub's secondary rate limit punishes
// bursts of content-creating requests.
var writePause = time.Second

func runApply(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := newFlagSet("apply", stderr)
	f := addPipelineFlags(fs)
	yes := fs.Bool("yes", false, "make the changes without asking for approval")
	if err := parse(fs, args); err != nil {
		return err
	}
	r, err := pipeline(ctx, f, facts.Needs{Branch: true}, stderr)
	if err != nil {
		return err
	}
	if *f.verbose {
		defer r.printStats(stderr)
	}
	if err := plan.WriteTable(stdout, r.plan); err != nil {
		return err
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

	var blocked, deferred, writing int
	fmt.Fprintln(stdout)
	for _, d := range ds {
		name := d.Repo.FullName()
		switch d.Kind {
		case apply.Blocked:
			blocked++
			fmt.Fprintf(stdout, "%s: blocked: %s\n", name, d.Reason)
		case apply.Deferred:
			deferred++
			fmt.Fprintf(stdout, "%s: deferred: %s\n", name, d.Reason)
		}
		if d.SettingsDeferred {
			deferred++
			fmt.Fprintf(stdout, "%s: settings deferred: maxPerRun reached\n", name)
		}
		if d.Writes() {
			writing++
			for _, s := range d.Steps() {
				fmt.Fprintf(stdout, "%s: will %s\n", name, s)
			}
		}
	}
	if writing == 0 {
		fmt.Fprintf(stdout, "No changes. GitHub matches the config (%d blocked, %d deferred).\n", blocked, deferred)
		return nil
	}
	if !*yes {
		ok, err := approve(stdin, stdout)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("apply cancelled")
		}
	}

	ex := &apply.Executor{GH: r.gh, Opts: opts}
	var changed, failed int
	fmt.Fprintln(stdout)
	for _, d := range ds {
		if !d.Writes() {
			continue
		}
		if changed+failed > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(writePause):
			}
		}
		name := d.Repo.FullName()
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
	fmt.Fprintf(stdout, "\nApplied: %d repos changed, %d blocked, %d deferred, %d failed.\n", changed, blocked, deferred, failed)
	if failed > 0 {
		return fmt.Errorf("%d repos failed", failed)
	}
	return nil
}

// approve asks for approval the way tofu apply does: only "yes" counts.
func approve(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "\nDo you want to perform these actions?\n  meles will make the changes described above.\n  Only 'yes' will be accepted to approve.\n\n  Enter a value: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false, errors.New("no answer to the approval prompt; pass --yes to apply without asking")
	}
	return strings.TrimSpace(line) == "yes", nil
}
