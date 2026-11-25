//go:build windows

package localci

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Every step runs inside its own Windows Job Object (KILL_ON_JOB_CLOSE). The process starts suspended, is put into
// the job, then resumed, so no child it spawns can escape the job. killTree terminates the whole job.

var jobs sync.Map // *exec.Cmd -> windows.Handle

var (
	ntdll               = windows.NewLazySystemDLL("ntdll.dll")
	procNtResumeProcess = ntdll.NewProc("NtResumeProcess")
)

// prepareCmd makes the step start suspended in its own process group.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
}

// afterStart puts the started (still suspended) process into a new job and resumes it.
func afterStart(cmd *exec.Cmd) error {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return fmt.Errorf("configure job object: %w", err)
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME|windows.PROCESS_QUERY_INFORMATION,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(h)
		return fmt.Errorf("open step process: %w", err)
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(h, ph); err != nil {
		windows.CloseHandle(h)
		return fmt.Errorf("assign step to job object: %w", err)
	}
	jobs.Store(cmd, h)
	if r, _, e := procNtResumeProcess.Call(uintptr(ph)); r != 0 {
		return fmt.Errorf("resume step process: status 0x%x (%v)", r, e)
	}
	return nil
}

// killTree terminates every process of the step's job (the step and everything it started).
func killTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if v, ok := jobs.Load(cmd); ok {
		_ = windows.TerminateJobObject(v.(windows.Handle), 1)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// releaseTree kills anything the step left behind (like Actions does at the end of a step) and closes the job.
func releaseTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if v, ok := jobs.LoadAndDelete(cmd); ok {
		h := v.(windows.Handle)
		_ = windows.TerminateJobObject(h, 1)
		_ = windows.CloseHandle(h)
	}
}

// setCmdLine sets the raw command line (cmd.exe does not understand Go's argument quoting).
func setCmdLine(cmd *exec.Cmd, line string) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = line
}
