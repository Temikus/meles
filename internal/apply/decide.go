// Package apply turns a plan into GitHub writes. Decide is pure and says what
// to do per repo; Execute does it.
package apply

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/housekeeping"
	"github.com/temikus/meles/internal/plan"
	"github.com/temikus/meles/internal/policy"
)

// Options carries config that shapes the PR.
type Options struct {
	Branch     string
	Labels     []string
	ControlRef string
	Today      time.Time
}

// Kind is the outcome for a repo's housekeeping PR.
type Kind string

// Kinds.
const (
	Nothing  Kind = "up to date"
	Write    Kind = "write"
	Close    Kind = "close"
	Blocked  Kind = "blocked"
	Deferred Kind = "deferred"
)

// Decision is what apply does to one repo. The zero value of each write
// field means "leave it".
type Decision struct {
	Repo   *facts.Repo
	Kind   Kind
	Reason string // for Blocked and Deferred

	Files    []policy.FileChange // the PR's content
	Rewrite  bool                // new commit on the base, force-pushed to the branch
	PR       int                 // the open meles PR, 0 if none
	CreatePR bool
	EditPR   bool // set Title and Body on PR
	Title    string
	Body     string
	Message  string // commit message
	// AddLabels are labels the PR lacks; all of them for a new PR.
	AddLabels    []string
	DeleteBranch bool     // with Close
	Comment      *Comment // independent of Kind
}

// Comment is the one note meles leaves on a PR a human closed.
type Comment struct {
	PR   int
	Body string
}

// Writes reports whether executing d calls a write endpoint.
func (d Decision) Writes() bool {
	if d.Kind == Blocked || d.Kind == Deferred {
		return false
	}
	return d.Rewrite || d.CreatePR || d.EditPR || len(d.AddLabels) > 0 || d.Kind == Close || d.Comment != nil
}

// Decide compares the plan's PR items with the branch and PR GitHub has now.
func Decide(rp plan.RepoPlan, r *facts.Repo, o Options) Decision {
	d := Decision{Repo: r, Kind: Nothing}
	hk := r.Housekeeping
	if len(rp.Errors) > 0 {
		return blocked(d, "repo has errors: "+strings.Join(rp.Errors, "; "))
	}

	var items []plan.Item
	for _, it := range rp.Items {
		if fc, ok := it.Change.(policy.FileChange); ok && it.Action == plan.ActionPR && it.Invalid == "" {
			items = append(items, it)
			d.Files = append(d.Files, fc)
		}
	}

	var open *facts.PR
	for i := range hk.PRs {
		if hk.PRs[i].Open {
			open = &hk.PRs[i]
			break
		}
	}
	var openMarker housekeeping.Marker
	if open != nil {
		m, ok := housekeeping.Decode(open.Body)
		if !ok {
			return blocked(d, "an open PR from "+o.Branch+", #"+itoa(open.Number)+" is not a meles PR")
		}
		d.PR, openMarker = open.Number, m
	}
	if hk.Branch != nil && !housekeeping.HasTrailer(hk.Branch.Message) {
		return blocked(d, o.Branch+" has a commit meles did not make; merge or delete it first")
	}
	d.Comment = acceptNote(rp, hk.PRs, o.Today)

	if len(items) == 0 {
		if open != nil {
			d.Kind = Close
			d.Body = closedBody(open.Body)
			d.DeleteBranch = hk.Branch != nil
		}
		return d
	}

	d.Title, d.Body, d.Message = Title, body(items, o.ControlRef), commitMessage(items)
	d.Rewrite = hk.Branch == nil || len(hk.Branch.Parents) != 1 || !maps.Equal(hk.Branch.Files, blobs(d.Files)) ||
		// A merged PR's branch may already be in the base; a PR from it would be empty.
		(open == nil && hk.Branch.Parents[0] != hk.BaseSHA)
	d.CreatePR = open == nil
	d.EditPR = open != nil && (open.Title != Title || !sameItems(openMarker.Items, markerItems(items)))
	if open == nil {
		d.AddLabels = slices.Clone(o.Labels)
	} else {
		for _, l := range o.Labels {
			if !slices.Contains(open.Labels, l) {
				d.AddLabels = append(d.AddLabels, l)
			}
		}
	}
	if d.Rewrite || d.CreatePR || d.EditPR || len(d.AddLabels) > 0 {
		d.Kind = Write
	}
	return d
}

// Limit defers new commits and new PRs beyond max per run, in order. Other
// updates are cheap and keep going.
func Limit(ds []Decision, maxPerRun int) []Decision {
	n := 0
	for i := range ds {
		if ds[i].Kind != Write || (!ds[i].Rewrite && !ds[i].CreatePR) {
			continue
		}
		if n++; maxPerRun > 0 && n > maxPerRun {
			ds[i].Kind, ds[i].Reason = Deferred, "max_per_run reached"
		}
	}
	return ds
}

// acceptNote is the comment for the newest meles PR when a human closed it
// with accept findings and meles has not commented yet.
func acceptNote(rp plan.RepoPlan, prs []facts.PR, today time.Time) *Comment {
	if len(prs) == 0 || prs[0].Open || prs[0].Merged || prs[0].Noted {
		return nil
	}
	m, ok := housekeeping.Decode(prs[0].Body)
	if !ok || m.ClosedBy != "" {
		return nil
	}
	carried := map[string]bool{}
	for _, it := range m.Items {
		if it.OnClose == config.Accept {
			carried[it.Key+"\x00"+it.Shape] = true
		}
	}
	var accepted []plan.Item
	for _, it := range rp.Items {
		if it.Action == plan.ActionSkip && carried[it.Key+"\x00"+it.Shape] {
			accepted = append(accepted, it)
		}
	}
	if len(accepted) == 0 {
		return nil
	}
	return &Comment{PR: prs[0].Number, Body: acceptBody(accepted, today)}
}

func blocked(d Decision, reason string) Decision {
	d.Kind, d.Reason = Blocked, reason
	d.Comment = nil
	return d
}

func blobs(files []policy.FileChange) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		if f.Delete {
			out[f.Path] = ""
		} else {
			out[f.Path] = housekeeping.BlobSHA(f.Content)
		}
	}
	return out
}

func markerItems(items []plan.Item) []housekeeping.Item {
	out := make([]housekeeping.Item, len(items))
	for i, it := range items {
		out[i] = housekeeping.Item{Key: it.Key, Shape: it.Shape, OnClose: it.OnClose}
	}
	return out
}

func sameItems(a, b []housekeeping.Item) bool {
	key := func(it housekeeping.Item) string { return it.Key + "\x00" + it.Shape + "\x00" + string(it.OnClose) }
	as, bs := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		as[i] = key(a[i])
	}
	for i := range b {
		bs[i] = key(b[i])
	}
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}
