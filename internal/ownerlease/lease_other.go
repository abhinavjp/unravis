//go:build !windows

package ownerlease

// Acquire always succeeds off Windows, so the code compiles and runs everywhere (the product is
// Windows-only; mirrors internal/instance/guard_other.go).
func Acquire(runDir string, demo bool) (release func(), err error) {
	return func() {}, nil
}
