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
// listing before the tree is fetched. A not with files waits for Match.
func Prefilter(s config.Selector, m facts.Meta) bool {
	if !meta(s, m, true) {
		return false
	}
	return s.Not == nil || len(s.Not.Files) > 0 || !meta(*s.Not, m, false)
}

// Match checks every field. Files entries are tree paths or path.Match globs.
func Match(s config.Selector, r *facts.Repo) bool {
	if !meta(s, r.Meta, true) || !files(s, r) {
		return false
	}
	return s.Not == nil || !meta(*s.Not, r.Meta, false) || !files(*s.Not, r)
}

// meta checks the fields known before the tree is fetched. With implicit,
// an unset archived means "not archived".
func meta(s config.Selector, m facts.Meta, implicit bool) bool {
	switch {
	case s.Archived != nil:
		if m.Archived != *s.Archived {
			return false
		}
	case implicit && m.Archived:
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

func files(s config.Selector, r *facts.Repo) bool {
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
