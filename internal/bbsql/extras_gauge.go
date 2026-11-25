// extras_gauge.go holds the one helper the single-row gauge extras share.
package bbsql

// extraSingleRow decodes the one summed row of a gauge extra into T and returns
// it as one object (not a one-element array), exactly as the Standard
// schedulers sampler does. The statements COALESCE every aggregate to 0 in SQL
// because for these DMVs "nothing in flight" is a real reading (zero grants,
// zero pending I/O); a reply with no row at all is not a sample.
func extraSingleRow[T any](rows []Row, rowCap int) (any, bool, error) {
	var out []T
	truncated, err := decodeRows(rows, &out, rowCap)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out[0], truncated, nil
}
