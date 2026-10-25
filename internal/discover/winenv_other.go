//go:build !windows

package discover

// NewSystemEnv returns ErrNotSupportedOS: discovery reads Windows sources and
// has no implementation for other systems.
func NewSystemEnv() (Env, error) { return notSupportedEnv() }
