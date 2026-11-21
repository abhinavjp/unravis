//go:build !linux

package dotnetdiag

import "context"

// platformDialer on a platform with no transport here (anything but Linux; the
// Windows dialer PR narrows this tag to exclude Windows too):
// every Dial is ReasonUnsupported and nothing is opened.
func platformDialer() Dialer { return unsupportedDialer{} }

type unsupportedDialer struct{}

func (unsupportedDialer) Dial(context.Context, Target) (Conn, Peer, error) {
	return nil, Peer{}, newErr(ReasonUnsupported, 0)
}
