// Package facts holds the per-repo snapshot that policies evaluate.
package facts

import "slices"

// Tri is a fact that may be unreadable.
type Tri uint8

// Tri values.
const (
	Unknown     Tri = iota // not readable with this token (e.g. not admin)
	Off                    // readable and disabled
	On                     // readable and enabled
	Unavailable            // cannot be enabled on this repo (e.g. needs GHAS)
)

func (t Tri) String() string {
	return [...]string{"unknown", "off", "on", "unavailable"}[t]
}

// MarshalText renders Tri as its name in JSON.
func (t Tri) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// TriOf converts a readable bool.
func TriOf(b bool) Tri {
	if b {
		return On
	}
	return Off
}

// Meta is what the repo listing returns, before any per-repo calls.
type Meta struct {
	Owner         string   `json:"owner"`
	Name          string   `json:"name"`
	DefaultBranch string   `json:"default_branch"`
	Visibility    string   `json:"visibility"`
	Language      string   `json:"language,omitempty"`
	Topics        []string `json:"topics,omitempty"`
	Archived      bool     `json:"archived"`
	Fork          bool     `json:"fork"`
	Admin         bool     `json:"admin"`
}

// FullName is owner/name.
func (m Meta) FullName() string { return m.Owner + "/" + m.Name }

// Security holds the security feature toggles.
type Security struct {
	SecretScanning       Tri `json:"secret_scanning"`
	PushProtection       Tri `json:"push_protection"`
	DependabotAlerts     Tri `json:"dependabot_alerts"`
	PrivateVulnReporting Tri `json:"private_vuln_reporting"`
}

// Repo is one repo's snapshot.
type Repo struct {
	Meta
	// Tree maps every path on the default branch to its blob SHA.
	Tree          map[string]string `json:"-"`
	TreeTruncated bool              `json:"tree_truncated,omitempty"`
	// Files holds content for needed paths that exist.
	Files    map[string][]byte `json:"-"`
	Security Security          `json:"security"`
}

// Has reports whether path exists on the default branch.
func (r *Repo) Has(path string) bool {
	_, ok := r.Tree[path]
	return ok
}

// Find returns the first of paths that exists.
func (r *Repo) Find(paths ...string) (string, bool) {
	for _, p := range paths {
		if r.Has(p) {
			return p, true
		}
	}
	return "", false
}

// Needs is what a policy asks the collector to fetch beyond Meta and Tree.
type Needs struct {
	// Paths whose content is loaded into Repo.Files when present.
	Paths    []string
	Security bool
}

// Merge returns the union of n and o.
func (n Needs) Merge(o Needs) Needs {
	paths := slices.Concat(n.Paths, o.Paths)
	slices.Sort(paths)
	return Needs{Paths: slices.Compact(paths), Security: n.Security || o.Security}
}
