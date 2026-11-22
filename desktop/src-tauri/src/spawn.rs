//! Starts a child suspended with no console window, puts it into the job, then lets it run. No window of the child can flash and the
//! child is never outside the job, even for a moment.

use crate::job::Job;
use std::path::Path;
use windows_sys::Win32::Foundation::{CloseHandle, HANDLE, WAIT_OBJECT_0};
use windows_sys::Win32::System::JobObjects::AssignProcessToJobObject;
use windows_sys::Win32::System::Threading::{
    CREATE_NO_WINDOW, CREATE_SUSPENDED, CreateProcessW, GetExitCodeProcess, PROCESS_INFORMATION, ResumeThread, STARTUPINFOW,
    TerminateProcess, WaitForSingleObject,
};

pub struct Child {
    pub pid: u32,
    pub process: HANDLE,
}

unsafe impl Send for Child {}
unsafe impl Sync for Child {}

impl Child {
    /// `Some(exit code)` once the child has ended.
    pub fn exit_code(&self) -> Option<u32> {
        unsafe {
            if WaitForSingleObject(self.process, 0) != WAIT_OBJECT_0 {
                return None;
            }
            let mut code = 0u32;
            (GetExitCodeProcess(self.process, &mut code) != 0).then_some(code)
        }
    }
}

impl Drop for Child {
    fn drop(&mut self) {
        unsafe {
            CloseHandle(self.process);
        }
    }
}

fn wide(s: &std::ffi::OsStr) -> Vec<u16> {
    use std::os::windows::ffi::OsStrExt;
    s.encode_wide().chain(Some(0)).collect()
}

/// Runs `exe` with `args` (already split; none contains a quote). The program is named by path, not found by search, and runs in its
/// own folder.
pub fn spawn_in_job(job: &Job, exe: &Path, args: &[&str]) -> Result<Child, String> {
    let app = wide(exe.as_os_str());
    let dir = wide(exe.parent().ok_or("the program has no folder")?.as_os_str());
    let mut line = format!("\"{}\"", exe.display());
    for a in args {
        if a.contains('"') {
            return Err("an argument contains a quote".into());
        }
        line.push(' ');
        line.push_str(a);
    }
    let mut wide_line: Vec<u16> = line.encode_utf16().chain(Some(0)).collect();
    unsafe {
        let mut si: STARTUPINFOW = std::mem::zeroed();
        si.cb = std::mem::size_of::<STARTUPINFOW>() as u32;
        let mut pi: PROCESS_INFORMATION = std::mem::zeroed();
        let ok = CreateProcessW(
            app.as_ptr(),
            wide_line.as_mut_ptr(),
            std::ptr::null(),
            std::ptr::null(),
            0,
            CREATE_NO_WINDOW | CREATE_SUSPENDED,
            std::ptr::null(),
            dir.as_ptr(),
            &si,
            &mut pi,
        );
        if ok == 0 {
            return Err(format!("CreateProcess failed: {}", std::io::Error::last_os_error()));
        }
        if AssignProcessToJobObject(job.0, pi.hProcess) == 0 {
            let e = std::io::Error::last_os_error();
            TerminateProcess(pi.hProcess, 1);
            CloseHandle(pi.hThread);
            CloseHandle(pi.hProcess);
            return Err(format!("AssignProcessToJobObject failed: {e}"));
        }
        if ResumeThread(pi.hThread) == u32::MAX {
            let e = std::io::Error::last_os_error();
            TerminateProcess(pi.hProcess, 1);
            CloseHandle(pi.hThread);
            CloseHandle(pi.hProcess);
            return Err(format!("ResumeThread failed: {e}"));
        }
        CloseHandle(pi.hThread);
        Ok(Child { pid: pi.dwProcessId, process: pi.hProcess })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use windows_sys::Win32::Foundation::WAIT_OBJECT_0;
    use windows_sys::Win32::System::Threading::WaitForSingleObject;

    /// The child sleeps 60 s; dropping the job must end it within seconds.
    #[test]
    fn job_kills_child() {
        let job = Job::new().unwrap();
        let ping = std::path::PathBuf::from(std::env::var_os("SystemRoot").unwrap()).join("System32").join("PING.EXE");
        let child = spawn_in_job(&job, &ping, &["-n", "60", "127.0.0.1"]).unwrap();
        assert_eq!(unsafe { WaitForSingleObject(child.process, 0) }, 258, "child should still run (WAIT_TIMEOUT)");
        drop(job);
        let r = unsafe { WaitForSingleObject(child.process, 5000) };
        assert_eq!(r, WAIT_OBJECT_0, "child must be gone after the job closed");
    }
}
