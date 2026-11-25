//go:build !windows

package power

func Apply(Mode) error { return nil }
