package history

import "api-audit-explorer/internal/sealedfile"

// The sealing, locking and atomic-replace code lives in internal/sealedfile so
// the hypothesis ledger can share it.

func currentUserProtector() (Protector, error)     { return sealedfile.CurrentUser() }
func acquireVaultLock(path string) (func(), error) { return sealedfile.Lock(path) }
func replaceFile(from, to string) error            { return sealedfile.Replace(from, to) }
