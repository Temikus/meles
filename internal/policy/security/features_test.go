package security

import (
	"testing"

	"github.com/temikus/meles/internal/facts"
	"github.com/temikus/meles/internal/policy"
	"github.com/temikus/meles/internal/policy/policytest"
)

const allOn = "{secret_scanning: true, push_protection: true, dependabot_alerts: true, private_vuln_reporting: true}"

func sec(ss, pp, da, pvr facts.Tri) *facts.Repo {
	r := policytest.Repo(nil)
	r.Security = facts.Security{SecretScanning: ss, PushProtection: pp, DependabotAlerts: da, PrivateVulnReporting: pvr}
	return r
}

func TestEvaluate(t *testing.T) {
	const on, off, unk, na = facts.On, facts.Off, facts.Unknown, facts.Unavailable
	for _, tc := range []struct {
		name string
		with string
		repo *facts.Repo
		want []policy.Finding
	}{
		{"compliant", allOn, sec(on, on, on, on), nil},
		{
			"one off", allOn, sec(on, on, off, on),
			[]policy.Finding{{Kind: Kind, Subject: "setting:security.dependabot_alerts", Message: "dependabot_alerts is off, want on", Fix: policy.FixSet}},
		},
		{
			"want disabled", "{private_vuln_reporting: false}", sec(on, on, on, on),
			[]policy.Finding{{Kind: Kind, Subject: "setting:security.private_vuln_reporting", Message: "private_vuln_reporting is on, want off", Fix: policy.FixSet}},
		},
		{
			"unknown is check-only", "{secret_scanning: true}", sec(unk, unk, on, on),
			[]policy.Finding{{Kind: Kind, Subject: "setting:security.secret_scanning", Message: "secret_scanning is unreadable with this token (needs admin)"}},
		},
		{"unavailable is silent", "{secret_scanning: true, push_protection: true}", sec(na, na, on, on), nil},
		{"unset keys ignored", "{}", sec(off, off, off, off), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPolicy(t, tc.with)
			got := p.Evaluate(tc.repo)
			if !equal(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestRemediate(t *testing.T) {
	p := newPolicy(t, allOn)
	r := sec(facts.Off, facts.On, facts.On, facts.On)
	fs := p.Evaluate(r)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v", fs)
	}
	c, ok := p.Remediate(r, fs[0])
	if !ok {
		t.Fatal("Remediate returned check-only")
	}
	want := policy.SettingChange{Setting: "security.secret_scanning", From: false, To: true}
	if c != want {
		t.Errorf("change = %+v, want %+v", c, want)
	}
	if _, ok := p.Remediate(r, policy.Finding{Kind: Kind, Subject: "setting:security.secret_scanning", Message: "x"}); ok {
		t.Error("check-only finding must not remediate")
	}
}

func TestConverges(t *testing.T) {
	p := newPolicy(t, allOn)
	left := policytest.Converge(t, p, sec(facts.Off, facts.Off, facts.Off, facts.Off), func(r *facts.Repo, c policy.SettingChange) {
		*field(&r.Security, c.Setting) = facts.TriOf(c.To.(bool))
	})
	if len(left) != 0 {
		t.Errorf("not converged: %+v", left)
	}
}

func TestFactoryRejectsUnknownKeys(t *testing.T) {
	if _, err := New(policytest.With(t, "{secret_scaning: true}"), nil); err == nil {
		t.Error("want error for misspelled key")
	}
}

func newPolicy(t *testing.T, with string) policy.Policy {
	t.Helper()
	p, err := New(policytest.With(t, with), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func equal(a, b []policy.Finding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
