package localci

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// runTree is the one place every child process is started and waited for. It puts the command in its own
// process tree (a process group, or a suspended-then-resumed Windows job object), starts it, lets ctx kill
// the whole tree, waits, and kills whatever the command left behind. On Windows a command prepared by
// prepareCmd starts suspended and only runs after afterStart, so no code may call cmd.Start directly.
//
// rawLine, when not empty, is the verbatim Windows command line (cmd.exe does not understand Go's quoting).
// started is false when the process never ran (err says why); otherwise err is the Wait result. A command
// that exited 0 but left a background process holding its output pipes is a success: the tree is killed.
func runTree(ctx context.Context, cmd *exec.Cmd, rawLine string) (started bool, err error) {
	prepareCmd(cmd)
	if rawLine != "" {
		setCmdLine(cmd, rawLine)
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	if err := afterStart(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		releaseTree(cmd)
		return false, fmt.Errorf("cannot contain the process: %w", err)
	}
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			killTree(cmd)
		case <-done:
		}
	}()
	werr := cmd.Wait()
	close(done)
	<-watcherDone // killTree never races releaseTree
	releaseTree(cmd)
	if errors.Is(werr, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		werr = nil
	}
	return true, werr
}
