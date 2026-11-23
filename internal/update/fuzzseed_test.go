//go:build fuzzseed

package update

import "testing"

// A planted bug, built only with `-tags fuzzseed`: proves the nightly fuzz run finds a crash and uploads the crasher file (BL-450).
// It panics on inputs that start with "BOOM" and is never part of a normal build or test run.
func FuzzSeededBug(f *testing.F) {
	f.Add([]byte("BOO"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) >= 4 && string(data[:4]) == "BOOM" {
			panic("planted bug: input starts with BOOM")
		}
	})
}
