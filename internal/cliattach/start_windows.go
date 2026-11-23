//go:build windows

package cliattach

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"api-audit-explorer/internal/clicore"
)

// Creation flags of the headless owner (spec 05 D20): no console, its own process group, and out of the job of the shell that started it
// when that job allows it (a job that ends would end the owner with it).
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	createBreakawayJob    = 0x01000000
)

// StartHeadless starts `<install>\unravis.exe --pipe-server --owner=headless`: unelevated when this program is, detached, no window, no
// browser, no stdio. It never starts the desktop shell. exe returns the path of this program.
func StartHeadless(exe func() (string, error)) clicore.Starter {
	return func(ctx context.Context) (clicore.Child, *clicore.Error) {
		self, err := exe()
		if err != nil {
			return clicore.Child{}, clicore.NewError(clicore.CodeEndpointUnavailable, "This command line cannot tell where it is installed.", "")
		}
		if real, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = real
		}
		app := filepath.Join(InstallDir(self), "unravis.exe")
		if fi, err := os.Stat(app); err != nil || !fi.Mode().IsRegular() {
			return clicore.Child{}, clicore.NewError(clicore.CodeEndpointUnavailable, "The app program was not found next to this command line.", "Run 'unravis doctor'.")
		}
		start := func(flags uint32) (*exec.Cmd, error) {
			cmd := exec.Command(app, "--pipe-server", "--owner=headless") // not tied to ctx: the owner outlives this command
			cmd.Dir = filepath.Dir(app)
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
			return cmd, cmd.Start()
		}
		cmd, err := start(detachedProcess | createNewProcessGroup | createBreakawayJob)
		if err != nil { // a job that does not allow breakaway refuses the flag: start inside it
			cmd, err = start(detachedProcess | createNewProcessGroup)
		}
		if err != nil {
			return clicore.Child{}, clicore.NewError(clicore.CodeEndpointUnavailable, "The app program could not be started.", "")
		}
		exit := make(chan int, 1)
		go func() {
			_ = cmd.Wait()
			code := -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			exit <- code
		}()
		return clicore.Child{PID: cmd.Process.Pid, Exit: exit}, nil
	}
}
