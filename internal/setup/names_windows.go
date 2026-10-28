//go:build windows

package setup

import "api-audit-explorer/internal/datadir"

// The installer's record of the shared folder: the very key and value names datadir.ReadAnchor reads, never a copy of them.
const (
	AnchorKey          = datadir.AnchorKey
	AnchorRootValue    = datadir.AnchorRootValue
	AnchorRootIDValue  = datadir.AnchorRootIDValue
	AnchorCasesIDValue = datadir.AnchorCasesIDValue
)
