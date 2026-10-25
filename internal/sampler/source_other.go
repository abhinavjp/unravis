//go:build !windows

package sampler

// OSSource is the operating system's Source. Only Windows has one.
func OSSource() (Source, error) { return nil, ErrNotSupportedOS }

// beginBackground has no effect off Windows. The returned function ends it.
func beginBackground() func() { return func() {} }
