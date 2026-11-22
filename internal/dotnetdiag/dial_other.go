//go:build !windows && !linux

package dotnetdiag

import "context"

// platformDialer on a platform with no transport (neither Windows nor Linux):
// every Dial is ReasonUnsupported and nothing is opened.
func platformDialer() Dialer { return unsupportedDialer{} }

type unsupportedDialer struct{}

func (unsupportedDialer) Dial(context.Context, Target) (Conn, Peer, error) {
	return nil, Peer{}, newErr(ReasonUnsupported, 0)
}
