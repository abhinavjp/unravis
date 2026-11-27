package hscli

import (
	"context"
	"fmt"
	"io"
)

// OldCIName is the name `homestretch ci` had before the rename (BL-708). The program of that name stays as a shim.
const OldCIName = "unravis-ci"

// CINotice is the one line the unravis-ci shim prints (to stderr, never stdout) before it forwards.
const CINotice = OldCIName + " is now `homestretch ci`; forwarding"

// EngineFunc runs one engine command line in this process (cicli.Main has this shape).
type EngineFunc func(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int

// CIShim is the whole of the old `unravis-ci` program: one plain line on stderr that says the new name, then the very same
// engine, with the same arguments, the same exit code and the same stdout. The engine keeps its old name for itself
// ("unravis-ci 0.1.0", "logs: unravis-ci log ..."), so a script that reads stdout sees no difference. Only stderr gains the
// line.
//
// The line is left out when the agent starts the engine on itself as a child (`run-item ... --child`): that is not a
// person or a script typing the old name, and every run would otherwise begin with it in its log.
func CIShim(ctx context.Context, engine EngineFunc, args []string, stdout, stderr io.Writer, environ []string) int {
	if !isChildCall(args) {
		_, _ = fmt.Fprintln(stderr, CINotice)
	}
	return engine(ctx, args, stdout, stderr, environ)
}

// isChildCall reports whether args are the engine's own child-process call: `run-item <id> --child ...`.
func isChildCall(args []string) bool {
	if len(args) == 0 || args[0] != "run-item" {
		return false
	}
	for _, a := range args[1:] {
		if a == "--child" || a == "-child" {
			return true
		}
	}
	return false
}
