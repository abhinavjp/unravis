//go:build !windows

package instance

// Acquire always reports the first copy off Windows, so the code compiles and
// runs everywhere.
func Acquire(names Names) (release func(), first bool, err error) {
	return func() {}, true, nil
}
