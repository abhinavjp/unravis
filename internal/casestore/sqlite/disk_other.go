//go:build !windows

package sqlite

import "math"

// freeBytes has no portable implementation here. The case store only runs on
// Windows (its key sealing fails closed elsewhere), so the floor is not enforced.
func freeBytes(string) (uint64, error) { return math.MaxUint64, nil }
