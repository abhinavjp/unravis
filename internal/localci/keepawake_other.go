//go:build !windows

package localci

// setExecutionState does nothing off Windows: the agent runs where the machine is not expected to sleep.
func setExecutionState(bool) {}
