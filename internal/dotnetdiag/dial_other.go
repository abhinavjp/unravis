//go:build !windows

package dotnetdiag

import "context"

// platformDialer has no transport on this platform yet: every Dial is ReasonUnsupported
// and nothing is opened. The Windows dialer is in dial_windows.go; the Linux dialer PR narrows the
// build tag of this file to the platforms that still have none.
func platformDialer() Dialer { return unsupportedDialer{} }

type unsupportedDialer struct{}

func (unsupportedDialer) Dial(context.Context, Target) (Conn, Peer, error) {
	return nil, Peer{}, newErr(ReasonUnsupported, 0)
}
