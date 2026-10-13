package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/temikus/meles/internal/buildinfo"
)

func runVersion(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("version", stderr)
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	i := buildinfo.Get()
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(i)
	}
	_, err := fmt.Fprintf(stdout, "meles %s (commit %s, built %s)\n", i.Version, i.Commit, i.Date)
	return err
}
