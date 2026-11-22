//go:build !windows

package locator

import (
	"errors"
	"time"
)

type unsupportedProber struct{}

func (unsupportedProber) ProcessStart(int) (time.Time, error) {
	return time.Time{}, errors.ErrUnsupported
}
func (unsupportedProber) PipeExists(string) bool { return false }

// SystemProber is the real prober; off Windows nothing is ever alive (the sidecar is Windows-only).
func SystemProber() Prober { return unsupportedProber{} }

// SelfStart is this process's creation time. Unsupported off Windows.
func SelfStart() (time.Time, error) { return time.Time{}, errors.ErrUnsupported }

// StartDetached is unsupported off Windows.
func StartDetached(string, ...string) error { return errors.ErrUnsupported }
