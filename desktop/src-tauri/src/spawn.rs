//! Starts a child suspended with no console window, puts it into the job, then lets it run. No window of the child can flash and the
//! child is never outside the job, even for a moment.

use crate::job::Job;
use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
use windows_sys::Win32::System::JobObjects::AssignProcessToJobObject;
use windows_sys::Win32::System::Threading::{
    CREATE_NO_WINDOW, CREATE_SUSPENDED, CreateProcessW, PROCESS_INFORMATION, ResumeThread, STARTUPINFOW, TerminateProcess,
};

pub struct Child {
    pub pid: u32,
    pub process: HANDLE,
}

unsafe impl Send for Child {}
unsafe impl Sync for Child {}

impl Drop for Child {
    fn drop(&mut self) {
        unsafe {
            CloseHandle(self.process);
        }
    }
}

/// `cmdline` is the full command line, program first (quoted when it has spaces).
pub fn spawn_in_job(job: &Job, cmdline: &str) -> Result<Child, String> {
    let mut wide: Vec<u16> = cmdline.encode_utf16().chain(Some(0)).collect();
    unsafe {
        let mut si: STARTUPINFOW = std::mem::zeroed();
        si.cb = std::mem::size_of::<STARTUPINFOW>() as u32;
        let mut pi: PROCESS_INFORMATION = std::mem::zeroed();
        let ok = CreateProcessW(
            std::ptr::null(),
            wide.as_mut_ptr(),
            std::ptr::null(),
            std::ptr::null(),
            0,
            CREATE_NO_WINDOW | CREATE_SUSPENDED,
            std::ptr::null(),
            std::ptr::null(),
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
        ResumeThread(pi.hThread);
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
        let child = spawn_in_job(&job, "ping.exe -n 60 127.0.0.1").unwrap();
        assert_eq!(unsafe { WaitForSingleObject(child.process, 0) }, 258, "child should still run (WAIT_TIMEOUT)");
        drop(job);
        let r = unsafe { WaitForSingleObject(child.process, 5000) };
        assert_eq!(r, WAIT_OBJECT_0, "child must be gone after the job closed");
    }
}
