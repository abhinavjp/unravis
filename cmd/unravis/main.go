// Command unravis is the thin console command line of Unravis (spec docs/desktop/spec/05-cli.md). It is installed as
// <install>\bin\unravis.exe and is not the app program: it starts no window, no server and no service.
package main

import (
	"context"
	"os"

	"api-audit-explorer/internal/cliattach"
	"api-audit-explorer/internal/clicore/cmdtree"
	"api-audit-explorer/internal/version"
)

func main() {
	attacher := cliattach.Default(os.Executable, "unravis-cli/"+version.Version)
	os.Exit(cmdtree.Run(context.Background(), os.Args[1:], cmdtree.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Attach: attacher.Attach, StartHeadless: cliattach.StartHeadless(os.Executable), ProcessGone: cliattach.ProcessGone}))
}
