//go:build !windows

package blackbox

import "api-audit-explorer/internal/sampler"

// OSCollector is only available on Windows.
func OSCollector(processes bool) (*HostCollector, error) {
	return nil, sampler.ErrNotSupportedOS
}
