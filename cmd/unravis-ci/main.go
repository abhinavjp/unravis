// Command unravis-ci runs one GitHub Actions workflow of this repository on this PC. The engine is in
// internal/cicli, shared with `homestretch ci`.
package main

import (
	"context"
	"os"
	"os/signal"

	"api-audit-explorer/internal/cicli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cicli.Main(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}
