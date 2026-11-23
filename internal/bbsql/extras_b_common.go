// extras_b_common.go: helpers shared by the batch B extras (perfctr, mem,
// memclerks, conns, trans, waiting, agdetail, locks, facts, factsdb, factscfg).
package bbsql

import "reflect"

// extraOneRow decodes the one summed row of a gauge extra into T and returns it
// as one object (not a one-element array), as the Standard schedulers sampler
// does. A reply with no row at all is not a sample. The statements ISNULL every
// aggregate to 0 in SQL where "nothing in flight" is a real reading.
func extraOneRow[T any](rows []Row, rowCap int) (any, bool, error) {
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

// specAll builds a rowSpec where every named JSON key is required and not
// nullable.
func specAll(keys ...string) rowSpec {
	m := make(map[string]colRule, len(keys))
	for _, k := range keys {
		m[k] = req()
	}
	return rowSpec{cols: m}
}

// reflectTypeOf is reflect.TypeOf of the zero T, so a registration reads
// without a value.
func reflectTypeOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }
