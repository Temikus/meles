// Package plan runs the read-only pipeline: inventory, facts, evaluate, plan.
package plan

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/exempt"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/housekeeping"
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/registry"
	"github.com/temikus/meles/internal/selector"
)

// Instance is one configured policy entry, ready to evaluate.
type Instance struct {
	Name     string // kind, or kind[selector] when the kind repeats
	Spec     config.PolicySpec
	Selector config.Selector
	Policy   policy.Policy
}

// Build turns config entries into instances. Planned-but-unimplemented kinds
// become warnings; unknown kinds and bad with: blocks are errors.
func Build(cfg *config.Config, ctl fs.FS) (insts []Instance, warnings []string, err error) {
	var errs config.Errors
	for _, spec := range cfg.Policies {
		name := spec.Instance(cfg.Policies)
		factory, ok := registry.Lookup(spec.ID)
		if !ok {
			if registry.Planned(spec.ID) {
				warnings = append(warnings, fmt.Sprintf("%s:%d: policy %s is not implemented yet, skipped", cfg.File, spec.Line, name))
			} else {
				errs = append(errs, config.Error{File: cfg.File, Line: spec.Line, Msg: fmt.Sprintf("unknown policy kind %q", spec.ID)})
			}
			continue
		}
		p, ferr := factory(&spec.With, ctl)
		if ferr != nil {
			errs = append(errs, config.Error{File: cfg.File, Line: spec.Line, Msg: fmt.Sprintf("policy %s: %v", name, ferr)})
			continue
		}
		insts = append(insts, Instance{Name: name, Spec: spec.Resolved(cfg.Defaults), Selector: cfg.Selectors[spec.On], Policy: p})
	}
	if len(errs) > 0 {
		return nil, warnings, errs
	}
	return insts, warnings, nil
}

// Action is what apply would do with an item.
type Action string

// Actions.
const (
	ActionPR     Action = "pr"     // file change, goes into the housekeeping PR
	ActionApply  Action = "apply"  // setting change, applied directly
	ActionReport Action = "report" // has a fix, but mode does not allow it
	ActionCheck  Action = "check"  // no automatic fix
	ActionSkip   Action = "skip"   // exempt, or a human closed its PR; apply leaves it alone
)

// Item is one finding with what meles would do about it.
type Item struct {
	Policy  string         `json:"policy"`
	Finding policy.Finding `json:"finding"`
	Key     string         `json:"key"`
	Shape   string         `json:"shape"`
	Mode    config.Mode    `json:"mode"`
	OnClose config.OnClose `json:"on_close"`
	Action  Action         `json:"action"`
	Note    string         `json:"note,omitempty"`
	Invalid string         `json:"invalid,omitempty"` // validator output; apply refuses the change
	Change  policy.Change  `json:"-"`
	Before  []byte         `json:"-"` // current content when Change updates a file
}

// MarshalJSON adds the change with its type, and file content as text.
func (it Item) MarshalJSON() ([]byte, error) {
	type plain Item
	var change any
	switch c := it.Change.(type) {
	case policy.FileChange:
		change = struct {
			Type    string `json:"type"`
			Path    string `json:"path"`
			Delete  bool   `json:"delete,omitempty"`
			Content string `json:"content,omitempty"`
		}{"file", c.Path, c.Delete, string(c.Content)}
	case policy.SettingChange:
		change = struct {
			Type string `json:"type"`
			policy.SettingChange
		}{"setting", c}
	}
	return json.Marshal(struct {
		plain
		Change any `json:"change,omitempty"`
	}{plain(it), change})
}

// RepoPlan is the plan for one repo.
type RepoPlan struct {
	Repo   string   `json:"repo"`
	Items  []Item   `json:"items,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// Summary counts the plan.
type Summary struct {
	Repos             int            `json:"repos"`
	ReposWithFindings int            `json:"repos_with_findings"`
	Findings          int            `json:"findings"`
	ByAction          map[Action]int `json:"by_action"`
}

// Plan is the output of `meles plan`.
type Plan struct {
	Version    int        `json:"version"`
	ControlRef string     `json:"control_ref,omitempty"`
	Summary    Summary    `json:"summary"`
	Repos      []RepoPlan `json:"repos"`
	Warnings   []string   `json:"warnings,omitempty"`
}

// Evaluate runs every matching instance against every repo. Pure: today
// decides which exemptions have expired.
func Evaluate(insts []Instance, repos []*facts.Repo, today time.Time) Plan {
	p := Plan{Version: 1, Summary: Summary{Repos: len(repos), ByAction: map[Action]int{}}}
	for _, r := range repos {
		rp := evaluateRepo(insts, r, today)
		if len(rp.Items) > 0 || len(rp.Errors) > 0 {
			p.Repos = append(p.Repos, rp)
		}
	}
	slices.SortFunc(p.Repos, func(a, b RepoPlan) int { return cmp.Compare(a.Repo, b.Repo) })
	p.recount()
	return p
}

func (p *Plan) recount() {
	p.Summary.ReposWithFindings, p.Summary.Findings = 0, 0
	clear(p.Summary.ByAction)
	for _, rp := range p.Repos {
		if len(rp.Items) > 0 {
			p.Summary.ReposWithFindings++
		}
		p.Summary.Findings += len(rp.Items)
		for _, it := range rp.Items {
			p.Summary.ByAction[it.Action]++
		}
	}
}

func evaluateRepo(insts []Instance, r *facts.Repo, today time.Time) RepoPlan {
	rp := RepoPlan{Repo: r.FullName()}
	exemptions, err := exempt.Parse(r.Files[exempt.Path])
	if err != nil {
		rp.Errors = append(rp.Errors, fmt.Sprintf("%s: %v", exempt.Path, err))
	}
	accepted := acceptedCloses(r.Housekeeping.PRs)
	byKind := map[string][]Instance{}
	var kinds []string
	for _, in := range insts {
		if !selector.Match(in.Selector, r) {
			continue
		}
		if _, seen := byKind[in.Spec.ID]; !seen {
			kinds = append(kinds, in.Spec.ID)
		}
		byKind[in.Spec.ID] = append(byKind[in.Spec.ID], in)
	}
	for _, kind := range kinds {
		matched := byKind[kind]
		if len(matched) > 1 {
			names := make([]string, len(matched))
			for i, m := range matched {
				names[i] = m.Name
			}
			rp.Errors = append(rp.Errors, fmt.Sprintf("matches %s; selectors for one kind must not overlap, skipped", strings.Join(names, ", ")))
			continue
		}
		in := matched[0]
		for _, f := range in.Policy.Evaluate(r) {
			it := item(in, r, f)
			switch active, expired := exempt.Find(exemptions, f.Kind, f.Subject, today); {
			case active != nil:
				it.Action = ActionSkip
				it.Note = fmt.Sprintf("exempt until %s: %s", active.Until.Format(time.DateOnly), active.Reason)
			case expired != nil:
				it.Note = joinNote(it.Note, "exemption expired "+expired.Until.Format(time.DateOnly))
			}
			if n, ok := accepted[it.Key+"\x00"+it.Shape]; ok && it.Action != ActionSkip && it.OnClose == config.Accept {
				it.Action = ActionSkip
				it.Note = fmt.Sprintf("closed in #%d (accept)", n)
			}
			rp.Items = append(rp.Items, it)
		}
	}
	slices.SortStableFunc(rp.Items, func(a, b Item) int {
		return cmp.Or(cmp.Compare(a.Policy, b.Policy), cmp.Compare(a.Finding.Subject, b.Finding.Subject))
	})
	return rp
}

// acceptedCloses maps key and shape to the newest meles PR a human closed
// without merging while it carried that finding.
func acceptedCloses(prs []facts.PR) map[string]int {
	out := map[string]int{}
	for _, pr := range prs {
		if pr.Open || pr.Merged {
			continue
		}
		m, ok := housekeeping.Decode(pr.Body)
		if !ok || m.ClosedBy == housekeeping.ClosedByMeles {
			continue
		}
		for _, it := range m.Items {
			k := it.Key + "\x00" + it.Shape
			if _, seen := out[k]; !seen {
				out[k] = pr.Number
			}
		}
	}
	return out
}

func item(in Instance, r *facts.Repo, f policy.Finding) Item {
	it := Item{
		Policy: in.Name, Finding: f, Key: f.Key(), Shape: f.Shape(),
		Mode: in.Spec.Mode, OnClose: cmp.Or(f.OnClose, in.Spec.OnClose), Action: ActionCheck,
	}
	c, ok := in.Policy.Remediate(r, f)
	if !ok {
		return it
	}
	it.Change = c
	if fc, ok := c.(policy.FileChange); ok && !fc.Delete {
		it.Before = r.Files[fc.Path]
	}
	switch {
	case in.Spec.Mode == config.Observe:
		it.Action = ActionReport
	case isFile(c):
		it.Action = ActionPR
	case in.Spec.Mode == config.Enforce:
		it.Action = ActionApply
	default:
		it.Action = ActionReport
		it.Note = "settings apply only with mode: enforce"
	}
	return it
}

func isFile(c policy.Change) bool {
	_, ok := c.(policy.FileChange)
	return ok
}
