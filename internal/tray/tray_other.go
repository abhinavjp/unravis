//go:build !windows

package tray

import "context"

// Run reports ErrUnsupported: there is no notification area off Windows. A
// validation error wins so a bad Options is caught on every platform.
func Run(ctx context.Context, o Options) error {
	if err := o.validate(); err != nil {
		return err
	}
	return ErrUnsupported
}
