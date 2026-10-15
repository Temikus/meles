// Package policy defines how a policy turns facts into findings and changes.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
)

// Policy evaluates one repo. Evaluate and Remediate must not do I/O:
// everything they need arrives in facts or was loaded by the Factory.
type Policy interface {
	Kind() string
	Needs() facts.Needs
	Evaluate(r *facts.Repo) []Finding
	// Remediate returns the change that fixes f, or false if f is check-only.
	Remediate(r *facts.Repo, f Finding) (Change, bool)
}

// Factory builds a policy from its `with:` block. It may read templates from
// the control repo; that is the only I/O a policy gets.
type Factory func(with *yaml.Node, ctl fs.FS) (Policy, error)

// Fix is the kind of change a finding calls for.
type Fix string

// Fix values. FixNone marks a check-only finding.
const (
	FixNone   Fix = ""
	FixCreate Fix = "create"
	FixUpdate Fix = "update"
	FixDelete Fix = "delete"
	FixSet    Fix = "set"
)

// Finding is one piece of drift in one repo.
type Finding struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"` // "file:SECURITY.md" or "setting:security.secretScanning"
	Message string `json:"message"`
	Fix     Fix    `json:"fix,omitempty"`
	// OnClose overrides the policy's onClose for this finding; empty keeps it.
	OnClose config.OnClose `json:"onClose,omitempty"`
}

// Key identifies the finding across runs; exemptions and onClose use it.
func (f Finding) Key() string { return f.Kind + "/" + f.Subject }

// Shape changes only when the drift changes shape (new subject, different
// fix), not when content changes. An accepted close holds until the shape
// changes or a year passes.
func (f Finding) Shape() string {
	h := sha256.Sum256([]byte(f.Kind + "\x00" + f.Subject + "\x00" + string(f.Fix)))
	return hex.EncodeToString(h[:8])
}

// Change is a FileChange or a SettingChange.
type Change interface{ change() }

// FileChange writes or deletes one file via the housekeeping PR.
type FileChange struct {
	Path    string `json:"path"`
	Content []byte `json:"-"`
	Delete  bool   `json:"delete,omitempty"`
}

// SettingChange is data only; the executor maps Setting to an API call.
type SettingChange struct {
	Setting string `json:"setting"`
	From    any    `json:"from"`
	To      any    `json:"to"`
}

func (FileChange) change()    {}
func (SettingChange) change() {}
