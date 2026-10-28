//go:build !windows

package setup

// The installer's record only exists on Windows (datadir defines its names in trust_windows.go, and setup never keeps a copy of them).
// Elsewhere the names are empty: the package builds, setup refuses to run (preflight's Windows check), and the tests that need the
// record skip.
const (
	AnchorKey          = ""
	AnchorRootValue    = ""
	AnchorRootIDValue  = ""
	AnchorCasesIDValue = ""
)
