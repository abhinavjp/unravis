package clisrv

import (
	"context"
	"encoding/json"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/mcp"
)

// MCPBackend runs the tool in a bridge that calls the dashboard in this process (mcp.NewLocal). The tool code is the one the MCP bridge
// runs, so the CLI and MCP give one answer for one request; the result handle of audit_compare stays in this bridge, so a later
// command (a new process) can page the same result.
func MCPBackend(b *mcp.Bridge) Backend { return mcpBackend{b} }

type mcpBackend struct{ b *mcp.Bridge }

func (m mcpBackend) Call(ctx context.Context, tool string, args json.RawMessage) (any, *clicore.Error) {
	data, err := m.b.Call(ctx, tool, args)
	if err == nil {
		return data, nil
	}
	code := clicore.FromMCP(mcp.Code(err), false, "")
	if code == clicore.CodeAppNotRunning {
		// this process is the owner and it is answering: its own dashboard did not, which is an upstream failure, not "the app is not running"
		code = clicore.CodeUpstreamUnavailable
	}
	// The message is the fixed text of the tool code: it never repeats a connection string or a driver error (the bridge guarantees it
	// for the MCP clients, and the same text goes out here). The work may have started, so it is never retryable.
	return nil, clicore.NewErrorAfterWork(code, err.Error(), actionFor(code))
}

// actionFor is the one-line next step for the codes people meet.
func actionFor(c clicore.Code) string {
	switch c {
	case clicore.CodeResultExpired:
		return "Run 'unravis audit compare' again."
	case clicore.CodeCaseLocked:
		return "Unlock Unravis in the app."
	case clicore.CodeOwnerBusy:
		return "Wait for the running command to finish."
	case clicore.CodeNotFound:
		return "Check the id and run the command again."
	}
	return ""
}
