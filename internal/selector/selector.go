// Package selector matches config selectors against repo facts.
package selector

import (
	"path"
	"slices"
	"strings"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
)

// Prefilter checks every field except files, so it can run on the repo
// listing before the tree is fetched.
func Prefilter(s config.Selector, m facts.Meta) bool {
	wantArchived := s.Archived != nil && *s.Archived
	if m.Archived != wantArchived {
		return false
	}
	if s.Fork != nil && m.Fork != *s.Fork {
		return false
	}
	if s.Visibility != "" && m.Visibility != s.Visibility {
		return false
	}
	if s.Language != "" && !strings.EqualFold(m.Language, s.Language) {
		return false
	}
	if len(s.Topics) > 0 && !slices.ContainsFunc(s.Topics, func(t string) bool { return slices.Contains(m.Topics, t) }) {
		return false
	}
	if s.Name != "" {
		if ok, _ := path.Match(s.Name, m.Name); !ok {
			return false
		}
	}
	return true
}

// Match checks every field. Files entries are tree paths or path.Match globs.
func Match(s config.Selector, r *facts.Repo) bool {
	if !Prefilter(s, r.Meta) {
		return false
	}
	if len(s.Files) == 0 {
		return true
	}
	for _, pat := range s.Files {
		if r.Has(pat) {
			return true
		}
		for p := range r.Tree {
			if ok, _ := path.Match(pat, p); ok {
				return true
			}
		}
	}
	return false
}
