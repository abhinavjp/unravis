// Package envguard clears MCPGODEBUG before the Go MCP SDK reads it.
//
// The SDK parses the MCPGODEBUG variable in an init function and panics when the value is malformed, so a stray MCPGODEBUG=x would crash
// every Unravis binary at startup with a stack trace. Valid values are no better: they switch compatibility behaviours of the wire
// (error codes, annotation encoding) that the MCP tests pin. Nothing in Unravis uses them, so the variable is removed.
//
// This works because package initialization runs in import-path order among packages that are ready, and this package imports nothing of
// the SDK: its path ("api-audit-explorer/...") sorts before the SDK's ("github.com/..."). TestEnvGuardClearsMCPGODEBUG re-executes the test
// binary with a malformed value and fails if the order ever changes, for example after a Go release or a module rename.
package envguard

import "os"

func init() {
	if _, set := os.LookupEnv("MCPGODEBUG"); set {
		_ = os.Unsetenv("MCPGODEBUG")
	}
}
