// Command meles keeps GitHub repos tidy.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/temikus/meles/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
