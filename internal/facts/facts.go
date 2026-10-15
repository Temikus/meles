// Package facts holds the per-repo snapshot that policies evaluate.
package facts

import (
	"slices"
	"time"
)

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
	DefaultBranch string   `json:"defaultBranch"`
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
	SecretScanning       Tri `json:"secretScanning"`
	PushProtection       Tri `json:"pushProtection"`
	DependabotAlerts     Tri `json:"dependabotAlerts"`
	PrivateVulnReporting Tri `json:"privateVulnReporting"`
}

// Standards is what GitHub reports for the repo, wherever the file lives and
// including files inherited from the owner's public .github repo.
type Standards struct {
	License        bool `json:"license"`
	SecurityPolicy bool `json:"securityPolicy"`
	CodeOfConduct  bool `json:"codeOfConduct"`
	Contributing   bool `json:"contributing"`
	Codeowners     bool `json:"codeowners"`
	// Readme is the path of the README GitHub displays, "" if none.
	Readme string `json:"readme,omitempty"`
}

// PR is a pull request from the housekeeping branch.
type PR struct {
	Number int
	Title  string
	Body   string
	Open   bool
	Merged bool
	// ClosedAt is zero while the PR is open.
	ClosedAt time.Time
	Labels   []string
	// Noted: meles already commented on this closed PR. Read only for the
	// newest PR, and only with Needs.Branch.
	Noted bool
}

// Branch is the head commit of the housekeeping branch.
type Branch struct {
	SHA     string
	Parents []string
	Message string
	// Files the commit changes: path to blob SHA, "" for a deletion.
	Files map[string]string
}

// Housekeeping is the state of meles's own branch and PRs in a repo.
type Housekeeping struct {
	PRs      []PR // newest first
	BaseSHA  string
	BaseTree string
	Branch   *Branch // nil when the branch does not exist
}

// Repo is one repo's snapshot.
type Repo struct {
	Meta
	// Tree maps every path on the default branch to its blob SHA.
	Tree          map[string]string `json:"-"`
	TreeTruncated bool              `json:"treeTruncated,omitempty"`
	// Files holds content for needed paths that exist.
	Files     map[string][]byte `json:"-"`
	Security  Security          `json:"security"`
	Standards Standards         `json:"standards"`

	Housekeeping Housekeeping `json:"-"`
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
	// Standards fills Repo.Standards except Readme.
	Standards bool
	// Readme fills Standards.Readme and loads its content into Files.
	Readme bool
	// PRs fills Housekeeping.PRs.
	PRs bool
	// Branch fills Housekeeping's base and branch head.
	Branch bool
}

// Merge returns the union of n and o.
func (n Needs) Merge(o Needs) Needs {
	paths := slices.Concat(n.Paths, o.Paths)
	slices.Sort(paths)
	return Needs{
		Paths:     slices.Compact(paths),
		Security:  n.Security || o.Security,
		Standards: n.Standards || o.Standards,
		Readme:    n.Readme || o.Readme,
		PRs:       n.PRs || o.PRs,
		Branch:    n.Branch || o.Branch,
	}
}
