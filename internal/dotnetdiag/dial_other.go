package dotnetdiag

import "context"

// platformDialer has no transport in this PR: every Dial is ReasonUnsupported
// and nothing is opened. The Windows and Linux dialers (next PRs) narrow the
// build tag of this file to the platforms that still have none.
func platformDialer() Dialer { return unsupportedDialer{} }

type unsupportedDialer struct{}

func (unsupportedDialer) Dial(context.Context, Target) (Conn, Peer, error) {
	return nil, Peer{}, newErr(ReasonUnsupported, 0)
}
