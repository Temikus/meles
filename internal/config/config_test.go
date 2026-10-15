package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExample(t *testing.T) {
	cfg, ctl, err := Load("../../examples/meles.yml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Defaults.Mode != PR || cfg.Defaults.PR.OnClose != Accept || cfg.Defaults.PR.Branch != "meles/housekeeping" {
		t.Errorf("defaults = %+v", cfg.Defaults)
	}
	if got := len(cfg.Policies); got != 8 {
		t.Fatalf("got %d policies, want 8", got)
	}
	sec := cfg.Policies[1].Resolved(cfg.Defaults)
	if sec.ID != "security.features" || sec.Mode != Enforce || sec.OnClose != Accept {
		t.Errorf("security.features resolved = %+v", sec)
	}
	gov := cfg.Policies[2].Resolved(cfg.Defaults)
	if gov.Mode != PR || gov.OnClose != Accept {
		t.Errorf("governance.standards resolved = %+v", gov)
	}
	if cfg.Policies[4].Instance(cfg.Policies) != "ci.shape[go]" || cfg.Policies[0].Instance(cfg.Policies) != "deps.updater" {
		t.Errorf("instances: %q %q", cfg.Policies[4].Instance(cfg.Policies), cfg.Policies[0].Instance(cfg.Policies))
	}
	if cfg.Policies[0].Line == 0 {
		t.Error("policy line not recorded")
	}
	if _, err := ctl.Open("shapes/go.yml"); err != nil {
		t.Errorf("control fs not rooted at config dir: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, want string
	}{
		{"unknown top-level key", "version: 1\nbogus: 1\n", "meles.yml:2: unknown field \"bogus\""},
		{"unknown policy key", "version: 1\nselectors: {all: {}}\npolicies:\n  - id: x\n    on: all\n    bogus: {}\n", "meles.yml:6: unknown field \"bogus\""},
		{"unknown selector key", "version: 1\nselectors:\n  all: {archive: false}\n", "meles.yml:3: unknown field \"archive\""},
		{"snake_case key", "version: 1\ndefaults:\n  pr: {on_close: accept}\n", "meles.yml:3: unknown field \"on_close\"; did you mean \"onClose\"?"},
		{"bad type", "version: one\n", "meles.yml:1:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Load(writeConfig(t, tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       []string
	}{
		{"wrong version", "version: 2\n", []string{"meles.yml:1: unsupported version 2"}},
		{"missing version", "selectors: {}\n", []string{"unsupported version 0"}},
		{
			"bad enums",
			"version: 1\ndefaults: {mode: yolo, pr: {onClose: maybe}}\n",
			[]string{`invalid mode "yolo"`, `invalid onClose "maybe"`},
		},
		{
			"unknown selector and missing id",
			"version: 1\nselectors: {all: {}}\npolicies:\n  - id: a.b\n    on: nope\n  - on: all\n",
			[]string{`meles.yml:4: policy a.b: unknown selector "nope"`, "meles.yml:6: policy has no id"},
		},
		{
			"duplicate kind and selector",
			"version: 1\nselectors: {go: {}}\npolicies:\n  - {id: ci.shape, on: go}\n  - {id: ci.shape, on: go}\n",
			[]string{`meles.yml:5: policy ci.shape: duplicate of line 4 (same kind and selector)`},
		},
		{
			"bad visibility",
			"version: 1\nselectors: {x: {visibility: secret}}\n",
			[]string{`selector x: invalid visibility "secret"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, err := Load(writeConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			err = cfg.Validate()
			if err == nil {
				t.Fatal("Validate passed, want errors")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v\nwant containing %q", err, w)
				}
			}
		})
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "meles.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
