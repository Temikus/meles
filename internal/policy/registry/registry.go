// Package registry maps policy kinds to their factories.
package registry

import (
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/deps"
	"github.com/temikus/meles/internal/policy/governance"
	"github.com/temikus/meles/internal/policy/security"
)

var factories = map[string]policy.Factory{
	deps.Kind:                deps.New,
	security.Kind:            security.New,
	governance.FilesKind:     governance.NewFiles,
	governance.StandardsKind: governance.NewStandards,
}

// Kinds in the design that later milestones implement. Configs may use them;
// they are skipped with a warning.
var planned = map[string]bool{
	"governance.settings": true,
	"ci.shape":            true,
	"actions.pinning":     true,
	"release.tooling":     true,
}

// Lookup returns the factory for kind.
func Lookup(kind string) (policy.Factory, bool) {
	f, ok := factories[kind]
	return f, ok
}

// Planned reports whether kind is known but not implemented yet.
func Planned(kind string) bool { return planned[kind] }
