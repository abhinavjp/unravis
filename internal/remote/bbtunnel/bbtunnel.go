// Package bbtunnel reads a Blackbox recorder through an SSH connection, so a recorder that listens only on
// the server's own loopback can still be read from this PC. It reuses the one Blackbox reader: nothing here
// parses a recording.
package bbtunnel

import (
	"context"
	"net"
	"net/http"
	"strconv"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/remote/sshdirect"
)

// DefaultAddr is where a recorder listens unless it was set up otherwise.
const DefaultAddr = "127.0.0.1:7790"

// Tunneler is the part of a signed-in connection this package needs.
type Tunneler interface {
	DialTunnel(ctx context.Context) (net.Conn, error)
}

// NewSource builds a Blackbox source that reaches the recorder at addr (as the server sees it) through cl.
// The connection is dialled through cl only; no port is opened on this PC, and whatever address the HTTP
// layer asks for, the dial goes to the one configured address.
func NewSource(cl Tunneler, addr, key string) (*blackbox.HTTPSource, error) {
	host, port, err := sshdirect.TunnelTarget(addr)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return cl.DialTunnel(ctx) },
		// A tunnelled connection is cheap to open and the recorder allows few readers; do not hold idle ones.
		DisableKeepAlives: true,
		Proxy:             nil,
	}
	loc, err := blackbox.ParseLocation("http://" + net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return blackbox.NewHTTPSource(loc, key, &http.Client{Transport: tr}), nil
}
