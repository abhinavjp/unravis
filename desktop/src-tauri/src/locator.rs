//! Attach to the running Go owner through backend.json, or start one inside a Job Object.

use crate::job::Job;
use crate::spawn::{Child, spawn_in_job};
use ipc_core::locator::{self, Record};
use std::path::PathBuf;
use std::time::{Duration, Instant};
use windows_sys::Win32::Foundation::{CloseHandle, FILETIME};
use windows_sys::Win32::System::Threading::{GetProcessTimes, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, QueryFullProcessImageNameW};

/// What the shell holds for its lifetime. `_job` is Some only when this shell started the owner: closing it kills the owner.
#[allow(dead_code)] // `record` is read by the pipe relay (BL-314)
pub struct Backend {
    pub record: Record,
    pub _job: Option<Job>,
    pub _child: Option<Child>,
}

fn run_dir() -> Result<PathBuf, String> {
    let base = std::env::var_os("LOCALAPPDATA").ok_or("LOCALAPPDATA is not set")?;
    Ok(PathBuf::from(base).join("Unravis").join("run"))
}

fn read_record(dir: &std::path::Path) -> Option<Record> {
    let bytes = std::fs::read(dir.join("backend.json")).ok()?;
    locator::parse(&bytes).ok()
}

/// The locator names a live process with the recorded start time and image. This proves a process, not that it serves the pipes; the
/// pipe client checks the server pid when it connects (BL-314).
fn alive(r: &Record) -> bool {
    unsafe {
        let h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, r.pid);
        if h.is_null() {
            return false;
        }
        let (mut c, mut e, mut k, mut u): (FILETIME, FILETIME, FILETIME, FILETIME) = std::mem::zeroed();
        let times = GetProcessTimes(h, &mut c, &mut e, &mut k, &mut u);
        let mut buf = [0u16; 1024];
        let mut len = buf.len() as u32;
        let img = QueryFullProcessImageNameW(h, 0, buf.as_mut_ptr(), &mut len);
        CloseHandle(h);
        if times == 0 || img == 0 {
            return false;
        }
        let ft = (u64::from(c.dwHighDateTime) << 32) | u64::from(c.dwLowDateTime);
        let Some(rec) = locator::start_unix_nanos(&r.start_time) else { return false };
        let image = String::from_utf16_lossy(&buf[..len as usize]);
        locator::same_start(rec, locator::filetime_to_unix_nanos(ft)) && image.eq_ignore_ascii_case(&r.exe)
    }
}

fn sidecar_exe() -> Result<PathBuf, String> {
    let me = std::env::current_exe().map_err(|e| e.to_string())?;
    let p = me.with_file_name("unravis.exe");
    p.is_file().then_some(p).ok_or_else(|| "unravis.exe was not found next to the shell".to_string())
}

pub fn attach_or_spawn() -> Result<Backend, String> {
    let dir = run_dir()?;
    if let Some(r) = read_record(&dir).filter(alive) {
        return Ok(Backend { record: r, _job: None, _child: None });
    }
    let exe = sidecar_exe()?;
    let job = Job::new()?;
    let line = format!("\"{}\" --pipe-server --owner=shell --shell-pid={}", exe.display(), std::process::id());
    let child = spawn_in_job(&job, &line)?;
    // The owner writes backend.json when its pipes are up. Wait for a record that names this child.
    let deadline = Instant::now() + Duration::from_secs(15);
    while Instant::now() < deadline {
        if let Some(r) = read_record(&dir).filter(|r| r.pid == child.pid && alive(r)) {
            return Ok(Backend { record: r, _job: Some(job), _child: Some(child) });
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    // Another owner won the singleton race (the child exited 12): attach to it instead.
    if let Some(r) = read_record(&dir).filter(alive) {
        return Ok(Backend { record: r, _job: None, _child: None });
    }
    Err("the background part did not start in 15 seconds".into())
}
