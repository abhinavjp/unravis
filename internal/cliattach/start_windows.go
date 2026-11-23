//go:build windows

package cliattach

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

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
		notFound := clicore.NewError(clicore.CodeEndpointUnavailable, "The app program was not found next to this command line.", "Run 'unravis doctor'.")
		fi, err := os.Stat(app)
		if err != nil || !fi.Mode().IsRegular() {
			return clicore.Child{}, notFound
		}
		if me, err := os.Stat(self); err == nil && os.SameFile(me, fi) {
			return clicore.Child{}, notFound // this command line sits in the install folder itself: it is not the app
		}
		local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
		if err != nil {
			return clicore.Child{}, clicore.NewError(clicore.CodeEndpointUnavailable, "The folder for the app's files cannot be found.", "")
		}
		base, err := userEnvironment()
		if err != nil {
			return clicore.Child{}, clicore.NewError(clicore.CodeEndpointUnavailable, "The environment for the app cannot be made.", "")
		}
		env := childEnv(base, local)
		start := func(flags uint32) (*exec.Cmd, error) {
			cmd := exec.Command(app, "--pipe-server", "--owner=headless") // not tied to ctx: the owner outlives this command
			cmd.Dir = filepath.Dir(app)
			cmd.Env = env // the run folder the app writes its locator to is the one this command line reads
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

// userEnvironment is the default environment of this user, made by Windows from the registry, not the one this program was started with
// (so a variable that only this shell set cannot move the app's folders, and this program reads no environment variable, D25).
func userEnvironment() ([]string, error) {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_READ, &tok); err != nil {
		return nil, err
	}
	defer tok.Close()
	var block *uint16
	if err := windows.CreateEnvironmentBlock(&block, tok, false); err != nil {
		return nil, err
	}
	defer func() { _ = windows.DestroyEnvironmentBlock(block) }()
	// the block ends with an empty entry: find its length
	n := 0
	for p := unsafe.Pointer(block); ; n++ { //nolint:gosec // the block that Windows returned, read up to its two zeros
		if *(*uint16)(unsafe.Add(p, n*2)) == 0 && *(*uint16)(unsafe.Add(p, (n+1)*2)) == 0 {
			n += 2
			break
		}
	}
	return parseEnvBlock(unsafe.Slice(block, n)), nil
}
