//go:build !windows

package localci

import (
	"os/exec"
	"syscall"
)

// prepareCmd puts the step in its own process group so killTree can stop the whole tree.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills the step's process group.
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

// setCmdLine is only meaningful on Windows (cmd.exe needs its command line verbatim).
func setCmdLine(cmd *exec.Cmd, line string) {}

// afterStart is a no-op off Windows (the process group set in prepareCmd is enough).
func afterStart(cmd *exec.Cmd) error { return nil }

// releaseTree kills anything the step left in its process group.
func releaseTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
