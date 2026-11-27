//go:build !windows

package keeper

import "errors"

// PipeListenOptions configures ListenPipe.
type PipeListenOptions struct {
	SDDL string
}

// ListenPipe is Windows only.
func ListenPipe(string, PipeListenOptions) (Listener, error) {
	return nil, errors.New("the key keeper pipe needs Windows")
}
