package plan

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/temikus/meles/internal/policy"
)

// Validator checks generated file content before it can go into a PR.
type Validator struct {
	Name  string
	Match func(path string) bool
	Check func(ctx context.Context, path string, content []byte) error
}

// RenovateValidator wraps renovate-config-validator, if it is on PATH.
func RenovateValidator() (Validator, bool) {
	bin, err := exec.LookPath("renovate-config-validator")
	if err != nil {
		return Validator{}, false
	}
	return Validator{
		Name:  "renovate-config-validator",
		Match: IsRenovateConfig,
		Check: func(ctx context.Context, p string, content []byte) error {
			dir, err := os.MkdirTemp("", "meles-renovate-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			f := filepath.Join(dir, path.Base(p)) // the validator picks JSON5 by extension
			if err := os.WriteFile(f, content, 0o600); err != nil {
				return err
			}
			out, err := exec.CommandContext(ctx, bin, f).CombinedOutput()
			if err == nil {
				return nil
			}
			var keep []string
			for _, l := range strings.Split(strings.ReplaceAll(string(out), f, p), "\n") {
				if !strings.Contains(l, "INFO:") && strings.TrimSpace(l) != "" {
					keep = append(keep, l)
				}
			}
			return fmt.Errorf("%s", strings.Join(keep, "\n"))
		},
	}, true
}

// IsRenovateConfig reports whether p is a Renovate config file.
func IsRenovateConfig(p string) bool {
	b := path.Base(p)
	return strings.HasPrefix(b, "renovate.json") || strings.HasPrefix(b, ".renovaterc")
}

// IsWorkflow reports whether p is a GitHub Actions workflow. Writing one needs
// the workflow scope on a PAT, or workflows: write on an App.
func IsWorkflow(p string) bool { return strings.HasPrefix(p, ".github/workflows/") }

// Block marks file changes whose path matches as invalid, so apply leaves
// them out of the PR.
func (p *Plan) Block(match func(path string) bool, reason string) {
	for i := range p.Repos {
		for j := range p.Repos[i].Items {
			it := &p.Repos[i].Items[j]
			if fc, ok := it.Change.(policy.FileChange); ok && it.Invalid == "" && match(fc.Path) {
				it.Invalid = reason
			}
		}
	}
}

// DemoteSettings turns setting changes apply would make into reports, for
// when the token cannot write settings.
func (p *Plan) DemoteSettings(reason string) {
	for i := range p.Repos {
		for j := range p.Repos[i].Items {
			if it := &p.Repos[i].Items[j]; it.Action == ActionApply {
				it.Action, it.Note = ActionReport, reason
			}
		}
	}
	p.recount()
}

// Validate runs validators over file changes and marks failures Invalid.
// A file that already fails before the change is noted, not blocked: the
// error is not meles's, and a stale validator rejects newer options.
// Results are cached by content, so a fleet of identical files runs once.
func (p *Plan) Validate(ctx context.Context, vs []Validator) {
	seen := map[[32]byte]string{}
	for i := range p.Repos {
		for j := range p.Repos[i].Items {
			it := &p.Repos[i].Items[j]
			fc, ok := it.Change.(policy.FileChange)
			if !ok || fc.Delete {
				continue
			}
			for _, v := range vs {
				if !v.Match(fc.Path) {
					continue
				}
				check := func(content []byte) string {
					key := sha256.Sum256(append([]byte(v.Name+"\x00"+path.Base(fc.Path)+"\x00"), content...))
					msg, done := seen[key]
					if !done {
						if err := v.Check(ctx, fc.Path, content); err != nil {
							msg = v.Name + ": " + err.Error()
						}
						seen[key] = msg
					}
					return msg
				}
				msg := check(fc.Content)
				switch {
				case msg == "":
				case it.Before != nil && check(it.Before) != "":
					it.Note = joinNote(it.Note, v.Name+" also rejects the current file; not blocking")
				default:
					it.Invalid = msg
				}
			}
		}
	}
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
