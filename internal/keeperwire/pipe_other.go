//go:build !windows

package keeperwire

import "context"

// PipeDialer connects to the keeper's pipe (Windows only; elsewhere the keeper is never running).
type PipeDialer struct {
	Name               string
	RequireSession0    bool
	RequireKeeperOwner bool
	ExtraOwners        []string
}

// NewPipeDialer is the production dialer for the pipe name.
func NewPipeDialer(name string) PipeDialer {
	return PipeDialer{Name: name, RequireSession0: true, RequireKeeperOwner: true}
}

func (PipeDialer) Dial(context.Context) (ClientConn, error) { return nil, ErrNotRunning }
