package codemode

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"api-audit-explorer/internal/mcp"
)

// Host runs one of the ten read-only tools for a script. The answer is the tool's envelope (the same bytes the MCP server sends as
// structured and as text content), and isError says the envelope is an error answer. A Host belongs to one run: it holds the handle of
// the run's last comparison (resultId) and the identifiers switch, neither of which another run may see.
type Host interface {
	Call(ctx context.Context, tool string, args json.RawMessage) (envelope []byte, isError bool)
}

// HostScope is what one run's tool calls may reach, fixed by the caller before the run starts (never by the script).
type HostScope struct {
	// AllowIdentifiers is the operator's choice for this run.
	AllowIdentifiers bool
	// Scoped says the run is limited to Sources (the source scope of a host's grant, BL-348); an empty list then reaches no source.
	// Unscoped is the owner's own command line and screen.
	Scoped  bool
	Sources []string
}

// HostFactory makes the Host of one run.
type HostFactory func(scope HostScope) Host

type bridgeHost struct{ b *mcp.Bridge }

func (h bridgeHost) Call(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
	return h.b.CallEnvelope(ctx, tool, args)
}

// DashboardHosts is the production HostFactory: a new local bridge per run over the dashboard handler of this process (the one that
// owns the sources), with the dashboard's own session token. The bridge reaches only the read routes of its allow-list, and it is the
// same code the MCP server and the command line run, so a script gets the answer a direct tool call gets.
func DashboardHosts(dashboard http.Handler, token string) HostFactory {
	return func(scope HostScope) Host {
		opts := []mcp.Option{mcp.WithIdentifiers(scope.AllowIdentifiers)}
		if scope.Scoped {
			// Same rule as the MCP dispatcher: the scope is part of the bridge, so no tool can reach another source whatever it is asked.
			opts = append(opts, mcp.WithSources(slices.Clone(scope.Sources)))
		}
		return bridgeHost{b: mcp.NewLocal(dashboard, token, opts...)}
	}
}
