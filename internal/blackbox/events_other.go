//go:build !windows

package blackbox

import (
	"errors"
	"time"
)

// eventsSupported: the event log is read only on Windows; the factory errors
// so BuildGroups skips the group entirely.
func eventsSupported() error { return errors.New("event log is read only on Windows") }

// queryEventChannel is not available off Windows.
func queryEventChannel(channel uint8, afterID uint64, floor time.Time, budget int, deadline time.Time) (eventChannelRead, error) {
	return eventChannelRead{}, errors.New("event log is read only on Windows")
}
