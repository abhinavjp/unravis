//go:build !windows

package blackbox

// sqlEnumPDHObjectsPlatform is the non-Windows default of the discovery seam:
// PDH counters exist only on Windows, so there are no objects to enumerate and
// the sqlHost group is skipped (same build-tag pattern as NewPDHSource in
// pdh_other.go).
func sqlEnumPDHObjectsPlatform() ([]string, error) {
	return nil, errSQLHostNoPDH
}