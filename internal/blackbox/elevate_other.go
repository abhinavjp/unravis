//go:build !windows

package blackbox

import (
	"context"
	"errors"
	"time"
)

// OSElevator does not exist off Windows; every call says so.
type OSElevator struct{ Timeout time.Duration }

// Run is not available.
func (OSElevator) Run(context.Context, string, []string, bool) ([]byte, error) {
	return nil, errors.New("the Blackbox service is Windows only")
}
