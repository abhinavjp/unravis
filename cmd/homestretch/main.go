// Command homestretch is the command line of Homestretch, the release and CI screen. It is also installed as hs: the
// two names run this same code. The commands live in internal/hscli; the engine commands of unravis-ci are under
// "homestretch ci".
package main

import (
	"context"
	"os"
	"os/signal"

	"api-audit-explorer/internal/hscli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(hscli.Main(ctx, os.Args[0], os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}
