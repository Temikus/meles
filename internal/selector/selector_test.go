package selector

import (
	"testing"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
)

func ptr[T any](v T) *T { return &v }

func repo(mut func(*facts.Repo)) *facts.Repo {
	r := &facts.Repo{
		Meta: facts.Meta{Owner: "o", Name: "svc-api", Visibility: "public", Language: "Go", Topics: []string{"infra"}},
		Tree: map[string]string{"go.mod": "a", "README.md": "b"},
	}
	if mut != nil {
		mut(r)
	}
	return r
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		sel  config.Selector
		repo *facts.Repo
		want bool
	}{
		{"empty matches", config.Selector{}, repo(nil), true},
		{"archived excluded implicitly", config.Selector{}, repo(func(r *facts.Repo) { r.Archived = true }), false},
		{"archived opt-in", config.Selector{Archived: ptr(true)}, repo(func(r *facts.Repo) { r.Archived = true }), true},
		{"archived true excludes live repos", config.Selector{Archived: ptr(true)}, repo(nil), false},
		{"fork false", config.Selector{Fork: ptr(false)}, repo(func(r *facts.Repo) { r.Fork = true }), false},
		{"forks match when unset", config.Selector{}, repo(func(r *facts.Repo) { r.Fork = true }), true},
		{"visibility", config.Selector{Visibility: "private"}, repo(nil), false},
		{"files any-of", config.Selector{Files: []string{"go.work", "go.mod"}}, repo(nil), true},
		{"files none", config.Selector{Files: []string{"pyproject.toml"}}, repo(nil), false},
		{"files glob", config.Selector{Files: []string{"*.md"}}, repo(nil), true},
		{"topics any-of", config.Selector{Topics: []string{"web", "infra"}}, repo(nil), true},
		{"topics none", config.Selector{Topics: []string{"web"}}, repo(nil), false},
		{"language case-insensitive", config.Selector{Language: "go"}, repo(nil), true},
		{"name glob", config.Selector{Name: "svc-*"}, repo(nil), true},
		{"name glob miss", config.Selector{Name: "lib-*"}, repo(nil), false},
		{"fields AND", config.Selector{Visibility: "public", Files: []string{"Cargo.toml"}}, repo(nil), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.sel, tc.repo); got != tc.want {
				t.Errorf("Match = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrefilterIgnoresFiles(t *testing.T) {
	sel := config.Selector{Files: []string{"Cargo.toml"}}
	if !Prefilter(sel, repo(nil).Meta) {
		t.Error("Prefilter must not reject on files: the tree is not fetched yet")
	}
	if Prefilter(config.Selector{Visibility: "private"}, repo(nil).Meta) {
		t.Error("Prefilter should reject on visibility")
	}
}
