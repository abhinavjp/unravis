//go:build !windows

package casestore

import "api-audit-explorer/internal/sealedfile"

// DefaultProtector has no backend off Windows. Failing closed keeps a case key
// from ever being written unsealed.
func DefaultProtector() (sealedfile.Protector, error) { return nil, sealedfile.ErrUnavailable }
