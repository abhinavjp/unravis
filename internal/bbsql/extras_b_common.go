// extras_b_common.go: helpers shared by the batch B extras (perfctr, mem,
// memclerks, conns, trans, waiting, agdetail, locks, facts, factsdb, factscfg).
package bbsql

import "reflect"

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
