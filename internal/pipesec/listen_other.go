//go:build !windows

package pipesec

import (
	"context"
	"net"
)

// Listen is Windows-only.
func Listen(ListenConfig) (net.Listener, error) { return nil, ErrUnsupported }

// DialVerified is Windows-only.
func DialVerified(context.Context, string, ServerPolicy) (net.Conn, Peer, error) {
	return nil, Peer{}, ErrUnsupported
}
