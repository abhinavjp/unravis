//go:build !windows

package blackbox

import (
	"errors"
	"time"
)

// eventsSupported: the event log is read only on Windows; the factory errors
// so BuildGroups skips the group entirely.
func eventsSupported() error { return errors.New("event log is read only on Windows") }

// queryEvents is not available off Windows.
func queryEvents(since time.Time) ([]rawEvent, error) {
	return nil, errors.New("event log is read only on Windows")
}
