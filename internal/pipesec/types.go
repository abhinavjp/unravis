package pipesec

import (
	"errors"
	"net"
)

// ErrUnsupported is returned by Listen and DialVerified on every OS but Windows.
var ErrUnsupported = errors.ErrUnsupported

// DefaultMaxInstances is the pipe instance pool size (spec 02 row 2.7).
const DefaultMaxInstances = 16

// ListenConfig configures Listen.
type ListenConfig struct {
	Name         string
	Policy       PeerPolicy // the zero value means DefaultPeerPolicy()
	MaxInstances int        // default 16, at most 254
}

// Conn is an accepted, peer-checked connection. Accept returns values of this type.
type Conn interface {
	net.Conn
	Peer() Peer
}
