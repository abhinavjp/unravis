//go:build windows

package casestore

import "api-audit-explorer/internal/sealedfile"

// DefaultProtector seals case keys with Windows DPAPI for the current user
// (never the machine scope).
func DefaultProtector() (sealedfile.Protector, error) { return sealedfile.CurrentUser() }
