//go:build !windows

package evtx

import (
	"context"
	"io"
)

// queryChannel has no implementation off Windows.
func queryChannel(context.Context, io.Writer, string, string) (Result, error) {
	return Result{}, ErrNotSupportedOS
}
