// Package cli dispatches meles subcommands.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

const usage = `Usage: meles [command] [flags]

Commands:
  plan      show what meles would change (default; never writes)
  validate  check meles.yml without calling GitHub
  version   print version information

Run "meles <command> -h" for flags.
`

// Run executes the subcommand named in args and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") && args[0] != "-h" && args[0] != "--help" {
		args = append([]string{"plan"}, args...)
	}
	var err error
	switch args[0] {
	case "plan":
		err = runPlan(ctx, args[1:], stdout, stderr)
	case "validate":
		err = runValidate(args[1:], stdout, stderr)
	case "version":
		err = runVersion(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "meles: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, new(usageError)):
		fmt.Fprintf(stderr, "meles: %v\n", err)
		return 2
	default:
		fmt.Fprintf(stderr, "meles: %v\n", err)
		return 1
	}
}

type usageError struct{ error }

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("meles "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err}
	}
	if fs.NArg() > 0 {
		return usageError{fmt.Errorf("unexpected arguments: %v", fs.Args())}
	}
	return nil
}
