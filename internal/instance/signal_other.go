//go:build !windows

package instance

import "context"

// Listen does nothing off Windows.
func Listen(ctx context.Context, names Names, onOpen, onQuit func()) (stop func(), err error) {
	return func() {}, nil
}

// Signal reports that no copy is running off Windows.
func Signal(names Names, k Kind) error {
	return ErrNoInstance
}
