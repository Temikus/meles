// Package deps implements the deps.updater policy.
package deps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
)

// Kind is the policy id.
const Kind = "deps.updater"

// Renovate's own lookup order.
var renovatePaths = []string{
	"renovate.json", "renovate.json5",
	".github/renovate.json", ".github/renovate.json5",
	".gitlab/renovate.json", ".gitlab/renovate.json5",
	".renovaterc", ".renovaterc.json", ".renovaterc.json5",
}

var dependabotPaths = []string{".github/dependabot.yml", ".github/dependabot.yaml"}

const (
	renovateNew   = "renovate.json"
	dependabotNew = ".github/dependabot.yml"
	schemaURL     = "https://docs.renovatebot.com/renovate-schema.json"
)

type with struct {
	Prefer     string   `yaml:"prefer"`
	Extends    []string `yaml:"extends"`
	ForbidBoth bool     `yaml:"forbid_both"`
}

type updater struct{ w with }

// New builds the policy from its with: block.
func New(n *yaml.Node, _ fs.FS) (policy.Policy, error) {
	w := with{Prefer: "renovate"}
	if err := config.DecodeStrict(n, &w); err != nil {
		return nil, err
	}
	if w.Prefer != "renovate" && w.Prefer != "dependabot" {
		return nil, fmt.Errorf("line %d: prefer: want renovate or dependabot, got %q", n.Line, w.Prefer)
	}
	return &updater{w: w}, nil
}

func (*updater) Kind() string { return Kind }

func (*updater) Needs() facts.Needs { return facts.Needs{Paths: slices.Clone(renovatePaths)} }

func (u *updater) preferRenovate() bool { return u.w.Prefer == "renovate" }

func (u *updater) Evaluate(r *facts.Repo) []policy.Finding {
	ren, hasRen := r.Find(renovatePaths...)
	dep, hasDep := r.Find(dependabotPaths...)
	finding := func(path, msg string, fix policy.Fix) policy.Finding {
		return policy.Finding{Kind: Kind, Subject: "file:" + path, Message: msg, Fix: fix}
	}

	switch {
	case !hasRen && !hasDep:
		if u.preferRenovate() {
			return []policy.Finding{finding(renovateNew, "no dependency updater configured; want Renovate", policy.FixCreate)}
		}
		return []policy.Finding{finding(dependabotNew, "no dependency updater configured; want Dependabot", policy.FixCreate)}
	case hasDep && !hasRen && u.preferRenovate():
		return []policy.Finding{finding(dep, "Dependabot is configured but Renovate is preferred; migrate by hand", policy.FixNone)}
	case hasRen && !hasDep && !u.preferRenovate():
		return []policy.Finding{finding(ren, "Renovate is configured but Dependabot is preferred; migrate by hand", policy.FixNone)}
	}

	var out []policy.Finding
	if hasRen && hasDep && u.w.ForbidBoth {
		other := dep
		if !u.preferRenovate() {
			other = ren
		}
		out = append(out, finding(other, "both Renovate and Dependabot are configured; remove "+other, policy.FixDelete))
	}
	if hasRen && u.preferRenovate() {
		if f, ok := u.checkExtends(r, ren); ok {
			out = append(out, f)
		}
	}
	return out
}

func (u *updater) checkExtends(r *facts.Repo, path string) (policy.Finding, bool) {
	f := policy.Finding{Kind: Kind, Subject: "file:" + path}
	content, ok := r.Files[path]
	if !ok {
		f.Message = path + " content not loaded"
		return f, true
	}
	if strings.HasSuffix(path, ".json5") {
		// No JSON5 parser; a substring check is enough to avoid false alarms.
		missing := slices.DeleteFunc(slices.Clone(u.w.Extends), func(p string) bool { return bytes.Contains(content, []byte(p)) })
		if len(missing) == 0 {
			return f, false
		}
		f.Message = "renovate config does not extend " + strings.Join(missing, ", ") + " (JSON5: edit by hand)"
		return f, true
	}
	var cfg struct {
		Extends []string `json:"extends"`
	}
	if err := json.Unmarshal(content, &cfg); err != nil {
		f.Message = path + " is not valid JSON"
		return f, true
	}
	missing := u.missingPresets(cfg.Extends)
	if len(missing) == 0 {
		return f, false
	}
	f.Message = "renovate config does not extend " + strings.Join(missing, ", ")
	f.Fix = policy.FixUpdate
	return f, true
}

// missingPresets treats "preset#tag" and "preset:name" as satisfying "preset".
func (u *updater) missingPresets(have []string) []string {
	var out []string
	for _, want := range u.w.Extends {
		if !slices.ContainsFunc(have, func(h string) bool {
			return h == want || strings.HasPrefix(h, want+"#") || strings.HasPrefix(h, want+":")
		}) {
			out = append(out, want)
		}
	}
	return out
}

func (u *updater) Remediate(r *facts.Repo, f policy.Finding) (policy.Change, bool) {
	path := strings.TrimPrefix(f.Subject, "file:")
	switch f.Fix {
	case policy.FixDelete:
		return policy.FileChange{Path: path, Delete: true}, true
	case policy.FixCreate:
		if path == renovateNew {
			return policy.FileChange{Path: path, Content: u.newRenovate()}, true
		}
		return policy.FileChange{Path: path, Content: newDependabot(r)}, true
	case policy.FixUpdate:
		var cfg struct {
			Extends []string `json:"extends"`
		}
		if json.Unmarshal(r.Files[path], &cfg) != nil {
			return nil, false
		}
		out, ok := addExtends(r.Files[path], u.missingPresets(cfg.Extends))
		if !ok {
			return nil, false
		}
		return policy.FileChange{Path: path, Content: out}, true
	}
	return nil, false
}

func (u *updater) newRenovate() []byte {
	extends := u.w.Extends
	if len(extends) == 0 {
		extends = []string{"config:recommended"}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep "github>owner/repo" readable
	enc.SetIndent("", "  ")
	_ = enc.Encode(struct {
		Schema  string   `json:"$schema"`
		Extends []string `json:"extends"`
	}{schemaURL, extends})
	return buf.Bytes()
}

var ecosystems = []struct {
	name    string
	markers []string
}{
	{"bundler", []string{"Gemfile"}},
	{"cargo", []string{"Cargo.toml"}},
	{"docker", []string{"Dockerfile"}},
	{"github-actions", nil}, // any file under .github/workflows/
	{"gomod", []string{"go.mod"}},
	{"npm", []string{"package.json"}},
	{"pip", []string{"pyproject.toml", "requirements.txt"}},
}

func newDependabot(r *facts.Repo) []byte {
	var found []string
	for _, e := range ecosystems {
		if e.markers == nil {
			for p := range r.Tree {
				if strings.HasPrefix(p, ".github/workflows/") {
					found = append(found, e.name)
					break
				}
			}
			continue
		}
		if _, ok := r.Find(e.markers...); ok {
			found = append(found, e.name)
		}
	}
	if len(found) == 0 {
		found = []string{"github-actions"}
	}
	var b strings.Builder
	b.WriteString("version: 2\nupdates:\n")
	for _, e := range found {
		fmt.Fprintf(&b, "  - package-ecosystem: %s\n    directory: /\n    schedule:\n      interval: weekly\n", e)
	}
	return []byte(b.String())
}

// addExtends splices presets into the extends array and leaves every other
// byte of src alone, so the PR diff shows only the new entries.
func addExtends(src []byte, add []string) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(src))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	openEnd := int(dec.InputOffset())
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, false
		}
		if key != "extends" {
			continue
		}
		end := int(dec.InputOffset())
		var cur []string
		if json.Unmarshal(raw, &cur) != nil {
			return nil, false
		}
		out := slices.Concat(src[:end-len(raw)], formatArray(append(cur, add...), raw), src[end:])
		return out, json.Valid(out)
	}

	rest := src[openEnd:]
	ws := rest[:len(rest)-len(bytes.TrimLeft(rest, " \t\r\n"))]
	entry := []byte(`"extends": ` + string(formatArray(add, nil)))
	var out []byte
	if bytes.HasPrefix(bytes.TrimLeft(rest, " \t\r\n"), []byte("}")) {
		out = slices.Concat(src[:openEnd], []byte("\n  "), entry, []byte("\n"), bytes.TrimLeft(rest, " \t\r\n"))
	} else {
		out = slices.Concat(src[:openEnd], ws, entry, []byte(","), rest)
	}
	return out, json.Valid(out)
}

// formatArray matches the layout of orig: one line, or one item per line
// with orig's indentation.
func formatArray(items []string, orig []byte) []byte {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = quote(s)
	}
	nl := bytes.IndexByte(orig, '\n')
	if nl < 0 {
		return []byte("[" + strings.Join(q, ", ") + "]")
	}
	after := orig[nl+1:]
	itemIndent := string(after[:len(after)-len(bytes.TrimLeft(after, " \t"))])
	last := orig[bytes.LastIndexByte(orig, '\n')+1:]
	closeIndent := string(last[:len(last)-len(bytes.TrimLeft(last, " \t"))])
	return []byte("[\n" + itemIndent + strings.Join(q, ",\n"+itemIndent) + "\n" + closeIndent + "]")
}

func quote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}
