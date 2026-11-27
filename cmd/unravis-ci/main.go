// Command unravis-ci is the old name of `homestretch ci` and stays as a shim (BL-708): it prints one line on stderr that
// names the new program and then runs the same engine (internal/cicli), with the same arguments, exit code and stdout.
// Installed agents and scheduled tasks that start `unravis-ci serve` keep working unchanged.
package main

import (
	"context"
	"os"
	"os/signal"

	"api-audit-explorer/internal/cicli"
	"api-audit-explorer/internal/hscli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(hscli.CIShim(ctx, cicli.Main, os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}
