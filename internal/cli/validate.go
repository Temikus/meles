package cli

import (
	"fmt"
	"io"
)

func runValidate(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("validate", stderr)
	cfgPath := fs.String("c", "meles.yml", "path to meles.yml in the control repo")
	if err := parse(fs, args); err != nil {
		return err
	}
	cfg, insts, warnings, err := load(*cfgPath)
	for _, w := range warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s: ok (%d policies active, %d skipped)\n", *cfgPath, len(insts), len(cfg.Policies)-len(insts))
	return err
}
