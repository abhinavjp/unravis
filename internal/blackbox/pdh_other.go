//go:build !windows

package blackbox

import "errors"

// errPDHWindowsOnly is returned by every PDH entry point off Windows.
var errPDHWindowsOnly = errors.New("PDH counters are Windows only")

// PDHSource is the non-Windows stub so callers compile cross-platform.
type PDHSource struct{}

var _ CounterSource = (*PDHSource)(nil)

func NewPDHSource(paths []string) (*PDHSource, error) { return nil, errPDHWindowsOnly }

func (p *PDHSource) Collect() ([]CounterReading, error) { return nil, errPDHWindowsOnly }
func (p *PDHSource) RefreshInstances() error            { return errPDHWindowsOnly }
func (p *PDHSource) Close() error                      { return errPDHWindowsOnly }
func (p *PDHSource) Paths() []string                   { return nil }