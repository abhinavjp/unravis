//go:build !windows

package keeper

import (
	"context"
	"errors"
)

// PipeListenOptions configures ListenPipe.
type PipeListenOptions struct {
	SDDL string
}

// ListenPipe is Windows only.
func ListenPipe(string, PipeListenOptions) (Listener, error) {
	return nil, errors.New("the key keeper pipe needs Windows")
}

// PipeDialer connects to the keeper's pipe (Windows only; elsewhere the keeper is never running).
type PipeDialer struct {
	Name            string
	RequireSession0 bool
}

// NewPipeDialer is the production dialer for the pipe name.
func NewPipeDialer(name string) PipeDialer { return PipeDialer{Name: name, RequireSession0: true} }

func (PipeDialer) Dial(context.Context) (ClientConn, error) { return nil, ErrNotRunning }
