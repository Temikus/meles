package cli

import (
	"github.com/temikus/meles/internal/ghclient"
	"github.com/temikus/meles/internal/plan"
	"github.com/temikus/meles/internal/policy/security"
)

// appInstances drops security.features when the App has no Administration
// permission: it could neither read nor change the settings.
func appInstances(in ghclient.Installation, insts []plan.Instance) ([]plan.Instance, []string) {
	if in.Administration != "" {
		return insts, nil
	}
	var out []plan.Instance
	var warnings []string
	for _, i := range insts {
		if i.Spec.ID == security.Kind {
			warnings = append(warnings, i.Name+" skipped: the App has no Administration permission")
			continue
		}
		out = append(out, i)
	}
	return out, warnings
}

// restrictToApp keeps apply within the App's permissions.
func restrictToApp(in ghclient.Installation, p *plan.Plan) {
	if in.Administration != "write" {
		p.DemoteSettings("the App has Administration: read; settings need write")
	}
	if in.Workflows != "write" {
		p.Block(plan.IsWorkflow, "the App lacks the Workflows: write permission that changes under .github/workflows/ need")
	}
}
