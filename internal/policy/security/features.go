// Package security implements the security.features policy.
package security

import (
	"fmt"
	"io/fs"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/config"
	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
)

// Kind is the policy id.
const Kind = "security.features"

type with struct {
	SecretScanning       *bool `yaml:"secret_scanning"`
	PushProtection       *bool `yaml:"push_protection"`
	DependabotAlerts     *bool `yaml:"dependabot_alerts"`
	PrivateVulnReporting *bool `yaml:"private_vuln_reporting"`
}

type features struct{ want with }

// New builds the policy from its with: block.
func New(n *yaml.Node, _ fs.FS) (policy.Policy, error) {
	var w with
	if err := config.DecodeStrict(n, &w); err != nil {
		return nil, err
	}
	return &features{want: w}, nil
}

func (*features) Kind() string       { return Kind }
func (*features) Needs() facts.Needs { return facts.Needs{Security: true} }

// toggles pairs each setting name with its desired value; order fixes output order.
func (p *features) toggles() []struct {
	name string
	want *bool
} {
	return []struct {
		name string
		want *bool
	}{
		{"secret_scanning", p.want.SecretScanning},
		{"push_protection", p.want.PushProtection},
		{"dependabot_alerts", p.want.DependabotAlerts},
		{"private_vuln_reporting", p.want.PrivateVulnReporting},
	}
}

func (p *features) Evaluate(r *facts.Repo) []policy.Finding {
	var out []policy.Finding
	sec := r.Security
	for _, t := range p.toggles() {
		if t.want == nil {
			continue
		}
		have := *field(&sec, "security."+t.name)
		f := policy.Finding{Kind: Kind, Subject: "setting:security." + t.name}
		switch have {
		case facts.Unavailable:
			continue
		case facts.Unknown:
			f.Message = t.name + " is unreadable with this token (needs admin)"
		default:
			if have == facts.TriOf(*t.want) {
				continue
			}
			f.Message = fmt.Sprintf("%s is %s, want %s", t.name, have, facts.TriOf(*t.want))
			f.Fix = policy.FixSet
		}
		out = append(out, f)
	}
	return out
}

func (p *features) Remediate(r *facts.Repo, f policy.Finding) (policy.Change, bool) {
	if f.Fix != policy.FixSet {
		return nil, false
	}
	setting := strings.TrimPrefix(f.Subject, "setting:")
	sec := r.Security
	cur := field(&sec, setting)
	if cur == nil {
		return nil, false
	}
	return policy.SettingChange{Setting: setting, From: *cur == facts.On, To: *cur != facts.On}, true
}

func field(s *facts.Security, setting string) *facts.Tri {
	switch setting {
	case "security.secret_scanning":
		return &s.SecretScanning
	case "security.push_protection":
		return &s.PushProtection
	case "security.dependabot_alerts":
		return &s.DependabotAlerts
	case "security.private_vuln_reporting":
		return &s.PrivateVulnReporting
	}
	return nil
}
