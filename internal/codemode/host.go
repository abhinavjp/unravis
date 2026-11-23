package codemode

import (
	"context"
	"encoding/json"
	"net/http"

	"api-audit-explorer/internal/mcp"
)

// Host runs one of the ten read-only tools for a script. The answer is the tool's envelope (the same bytes the MCP server sends as
// structured and as text content), and isError says the envelope is an error answer. A Host belongs to one run: it holds the handle of
// the run's last comparison (resultId) and the identifiers switch, neither of which another run may see.
type Host interface {
	Call(ctx context.Context, tool string, args json.RawMessage) (envelope []byte, isError bool)
}

// HostFactory makes the Host of one run. allowIdentifiers is the operator's choice for this run (never the script's).
type HostFactory func(allowIdentifiers bool) Host

type bridgeHost struct{ b *mcp.Bridge }

func (h bridgeHost) Call(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
	return h.b.CallEnvelope(ctx, tool, args)
}

// DashboardHosts is the production HostFactory: a new local bridge per run over the dashboard handler of this process (the one that
// owns the sources), with the dashboard's own session token. The bridge reaches only the read routes of its allow-list, and it is the
// same code the MCP server and the command line run, so a script gets the answer a direct tool call gets.
func DashboardHosts(dashboard http.Handler, token string) HostFactory {
	return func(allowIdentifiers bool) Host {
		return bridgeHost{b: mcp.NewLocal(dashboard, token, mcp.WithIdentifiers(allowIdentifiers))}
	}
}
