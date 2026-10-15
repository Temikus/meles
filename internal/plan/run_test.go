package plan

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
)

type needsStub struct {
	stub
	needs facts.Needs
}

func (n needsStub) Needs() facts.Needs { return n.needs }

// recorder stands in for GitHub; what is under test is which repos Run asks for.
type recorder struct {
	mu    sync.Mutex
	asked map[string]facts.Needs
}

func (c *recorder) Collect(_ context.Context, m facts.Meta, n facts.Needs) (*facts.Repo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked[m.Name] = n
	if m.Name == "broken" {
		return nil, errors.New("boom")
	}
	return &facts.Repo{Meta: m, Tree: map[string]string{}}, nil
}

func TestRunCollectsOnlyWhatMatches(t *testing.T) {
	f := []policy.Finding{{Kind: "k", Subject: "s", Message: "m"}}
	insts := []Instance{
		inst("a", "a", config.PR, config.Selector{}, needsStub{stub{"a", f, nil}, facts.Needs{Paths: []string{"x"}}}),
		inst("b", "b", config.PR, config.Selector{Visibility: "public"}, needsStub{stub{"b", nil, nil}, facts.Needs{Paths: []string{"y"}, Security: true}}),
	}
	metas := []facts.Meta{
		{Owner: "o", Name: "pub", Visibility: "public"},
		{Owner: "o", Name: "priv", Visibility: "private"},
		{Owner: "o", Name: "old", Visibility: "public", Archived: true},
		{Owner: "o", Name: "broken", Visibility: "private"},
	}
	col := &recorder{asked: map[string]facts.Needs{}}
	p, repos := Run(context.Background(), insts, metas, col, RunOptions{Workers: 3, Today: today, Needs: facts.Needs{Branch: true}})
	if len(repos) != 2 || repos[0].Name != "priv" || !col.asked["pub"].Branch {
		t.Errorf("repos = %v; want priv and pub, sorted, with the extra needs", repos)
	}

	if _, ok := col.asked["old"]; ok {
		t.Error("archived repo matched no selector and must not be fetched")
	}
	if n := col.asked["pub"]; !slices.Equal(n.Paths, []string{".meles.yml", "x", "y"}) || !n.Security || !n.PRs {
		t.Errorf("pub needs = %+v, want union of both policies", n)
	}
	if n := col.asked["priv"]; !slices.Equal(n.Paths, []string{".meles.yml", "x"}) || n.Security {
		t.Errorf("priv needs = %+v, want only policy a", n)
	}
	if p.Summary.Repos != 4 || p.Summary.Findings != 2 {
		t.Errorf("summary = %+v", p.Summary)
	}
	var broken *RepoPlan
	for i := range p.Repos {
		if p.Repos[i].Repo == "o/broken" {
			broken = &p.Repos[i]
		}
	}
	if broken == nil || len(broken.Errors) != 1 {
		t.Errorf("collection failure not reported: %+v", p.Repos)
	}
}
