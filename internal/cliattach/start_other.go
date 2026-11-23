//go:build !windows

package cliattach

import (
	"context"

	"api-audit-explorer/internal/clicore"
)

// StartHeadless off Windows: the app and its pipes exist only on Windows, so nothing is started.
func StartHeadless(_ func() (string, error)) clicore.Starter {
	return func(context.Context) (clicore.Child, *clicore.Error) {
		return clicore.Child{}, clicore.NewError(clicore.CodeEndpointUnavailable, "The app can be started from the command line on Windows only.", "")
	}
}
