//go:build windows

package main

import "api-audit-explorer/internal/sealedfile"

// demoSeal on Windows is the normal per-user DPAPI sealer; see demo_seal_other.go.
func demoSeal() (sealedfile.Protector, error) { return sealedfile.CurrentUser() }
