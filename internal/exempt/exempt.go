// Package exempt reads a target repo's .meles.yml opt-outs.
package exempt

import (
	"errors"
	"fmt"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/temikus/meles/internal/config"
)

// Path is where a repo keeps its exemptions.
const Path = ".meles.yml"

// Exemption skips a policy's findings, or one subject's, until a date.
type Exemption struct {
	Policy  string    // policy kind
	Subject string    // empty: every finding of the kind
	Reason  string    // required, so exemptions explain themselves
	Until   time.Time // required, inclusive, so exemptions do not rot silently
	Line    int
}

// Parse reads .meles.yml. Empty input has no exemptions.
func Parse(b []byte) ([]Exemption, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	var top struct {
		Exempt []yaml.Node `yaml:"exempt"`
	}
	if err := config.DecodeStrict(doc.Content[0], &top); err != nil {
		return nil, err
	}
	var out []Exemption
	for i := range top.Exempt {
		n := &top.Exempt[i]
		var raw struct {
			Policy  string `yaml:"policy"`
			Subject string `yaml:"subject"`
			Reason  string `yaml:"reason"`
			Until   string `yaml:"until"`
		}
		if err := config.DecodeStrict(n, &raw); err != nil {
			return nil, err
		}
		e := Exemption{Policy: raw.Policy, Subject: raw.Subject, Reason: raw.Reason, Line: n.Line}
		var errs []error
		if raw.Policy == "" {
			errs = append(errs, errors.New("policy is required"))
		}
		if raw.Reason == "" {
			errs = append(errs, errors.New("reason is required"))
		}
		if raw.Until == "" {
			errs = append(errs, errors.New("until is required"))
		} else if t, err := time.Parse(time.DateOnly, raw.Until); err != nil {
			errs = append(errs, fmt.Errorf("until %q is not a YYYY-MM-DD date", raw.Until))
		} else {
			e.Until = t
		}
		if err := errors.Join(errs...); err != nil {
			return nil, fmt.Errorf("line %d: %w", n.Line, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// Find returns the exemption that covers a finding today, or failing that one
// that covered it and has expired, so the plan can say so.
func Find(es []Exemption, kind, subject string, today time.Time) (active, expired *Exemption) {
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	for i := range es {
		e := &es[i]
		if e.Policy != kind || (e.Subject != "" && e.Subject != subject) {
			continue
		}
		if !today.After(e.Until) {
			return e, nil
		}
		expired = e
	}
	return nil, expired
}
