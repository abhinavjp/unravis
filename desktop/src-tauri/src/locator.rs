//! Attach to the running Go owner through backend.json, or start one inside a Job Object.
//!
//! The liveness test is the Go one (internal/locator `Alive`): the pid runs, its start time and image match, and both pipes exist.
//! Like Go it proves a process and two names, not that this process serves them; the pipe client checks the server pid when it
//! connects (BL-314).

use crate::job::Job;
use crate::spawn::{Child, spawn_in_job};
use ipc_core::locator::{self, Record};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};
use windows_sys::Win32::Foundation::{CloseHandle, ERROR_ACCESS_DENIED, ERROR_PIPE_BUSY, ERROR_SEM_TIMEOUT, FILETIME};
use windows_sys::Win32::System::Pipes::WaitNamedPipeW;
use windows_sys::Win32::System::Threading::{
    GetExitCodeProcess, GetProcessTimes, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, QueryFullProcessImageNameW,
};

/// What the shell holds for its lifetime. `_job` is Some only when this shell started the owner: closing it kills the owner.
#[allow(dead_code)] // read by the supervisor and the pipe relay (BL-307, BL-314)
pub struct Backend {
    pub record: Record,
    /// Full path of the sidecar this shell trusts (what the pipe server's image must equal).
    pub sidecar: PathBuf,
    pub _job: Option<Job>,
    pub _child: Option<Child>,
}

/// Same precedence as Go's `appdirs.RunDir`: UNRAVIS_RUN_DIR, then AUDIT_CONFIG_DIR\run, then %LOCALAPPDATA%\Unravis\run. The child
/// inherits the same environment, so both sides agree.
fn run_dir() -> Result<PathBuf, String> {
    let env = |k: &str| std::env::var_os(k).filter(|v| !v.is_empty());
    if let Some(d) = env("UNRAVIS_RUN_DIR") {
        return Ok(PathBuf::from(d));
    }
    if let Some(d) = env("AUDIT_CONFIG_DIR") {
        return Ok(PathBuf::from(d).join("run"));
    }
    let base = env("LOCALAPPDATA").ok_or("LOCALAPPDATA is not set")?;
    Ok(PathBuf::from(base).join("Unravis").join("run"))
}

fn read_record(dir: &Path) -> Option<Record> {
    let f = std::fs::File::open(dir.join("backend.json")).ok()?;
    let mut bytes = Vec::new();
    f.take(ipc_core::locator::MAX_BYTES as u64 + 1).read_to_end(&mut bytes).ok()?;
    locator::parse(&bytes).ok()
}

fn norm(p: &str) -> String {
    p.strip_prefix(r"\\?\").unwrap_or(p).replace('/', r"\").to_lowercase()
}

/// A pipe name exists: the wait succeeds, or fails with the errors a live server with no free instance gives. Anything else is absent.
fn pipe_exists(name: &str) -> bool {
    let w: Vec<u16> = name.encode_utf16().chain(Some(0)).collect();
    unsafe {
        if WaitNamedPipeW(w.as_ptr(), 1) != 0 {
            return true;
        }
    }
    matches!(
        std::io::Error::last_os_error().raw_os_error().map(|e| e as u32),
        Some(ERROR_SEM_TIMEOUT | ERROR_PIPE_BUSY | ERROR_ACCESS_DENIED)
    )
}

/// The record names a running process of OUR sidecar image with the recorded start time, and both pipes exist. The image is compared
/// with the path the shell would start itself, not with the `exe` field of the file: any program of this user can write the file.
fn alive(r: &Record, sidecar: &Path) -> bool {
    const STILL_ACTIVE: u32 = 259;
    let image = unsafe {
        let h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, r.pid);
        if h.is_null() {
            return false;
        }
        let mut code = 0u32;
        let (mut c, mut e, mut k, mut u): (FILETIME, FILETIME, FILETIME, FILETIME) = std::mem::zeroed();
        let mut buf = [0u16; 1024];
        let mut len = buf.len() as u32;
        let ok = GetExitCodeProcess(h, &mut code) != 0
            && code == STILL_ACTIVE
            && GetProcessTimes(h, &mut c, &mut e, &mut k, &mut u) != 0
            && QueryFullProcessImageNameW(h, 0, buf.as_mut_ptr(), &mut len) != 0;
        CloseHandle(h);
        if !ok {
            return false;
        }
        let ft = (u64::from(c.dwHighDateTime) << 32) | u64::from(c.dwLowDateTime);
        let Some(rec) = locator::start_unix_nanos(&r.start_time) else { return false };
        if !locator::same_start(rec, locator::filetime_to_unix_nanos(ft)) {
            return false;
        }
        String::from_utf16_lossy(&buf[..len as usize])
    };
    norm(&image) == norm(&sidecar.to_string_lossy()) && pipe_exists(&r.ui) && pipe_exists(&r.ag)
}

fn sidecar_exe() -> Result<PathBuf, String> {
    let me = std::env::current_exe().map_err(|e| e.to_string())?;
    let p = me.with_file_name("unravis.exe");
    p.is_file().then_some(p).ok_or_else(|| "unravis.exe was not found next to the shell".to_string())
}

pub fn attach_or_spawn() -> Result<Backend, String> {
    attach_or_spawn_with(&run_dir()?, &sidecar_exe()?)
}

/// Exit code of a pipe-mode copy that found a live owner (cmd/audit-explorer exitAttached).
const EXIT_ATTACHED: u32 = 12;

fn attach_only(dir: &Path, exe: &Path, wait: Duration) -> Option<Backend> {
    let deadline = Instant::now() + wait;
    loop {
        if let Some(r) = read_record(dir).filter(|r| alive(r, exe)) {
            return Some(Backend { record: r, sidecar: exe.to_path_buf(), _job: None, _child: None });
        }
        if Instant::now() >= deadline {
            return None;
        }
        std::thread::sleep(Duration::from_millis(100));
    }
}

pub fn attach_or_spawn_with(dir: &Path, exe: &Path) -> Result<Backend, String> {
    if let Some(b) = attach_only(dir, exe, Duration::ZERO) {
        return Ok(b);
    }
    let job = Job::new()?;
    // `--pipe-only`: no TCP port, no session file (PLAN section 1).
    let child = spawn_in_job(&job, exe, &["--pipe-only"])?;
    // The owner writes backend.json when its pipes are up. Wait for a record that names this child, and watch the child: when it ends
    // early, do not wait out the clock.
    let deadline = Instant::now() + Duration::from_secs(15);
    while Instant::now() < deadline {
        if let Some(r) = read_record(dir).filter(|r| r.pid == child.pid && alive(r, exe)) {
            return Ok(Backend { record: r, sidecar: exe.to_path_buf(), _job: Some(job), _child: Some(child) });
        }
        match child.exit_code() {
            Some(EXIT_ATTACHED) => {
                // Another owner won the singleton race: attach to it.
                return attach_only(dir, exe, Duration::from_secs(5))
                    .ok_or_else(|| "another copy owns Unravis but is not reachable".to_string());
            }
            Some(code) => return Err(format!("the background part stopped at start (exit code {code})")),
            None => std::thread::sleep(Duration::from_millis(100)),
        }
    }
    Err("the background part did not start in 15 seconds".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn image_names_compare_without_case_or_verbatim_prefix() {
        assert_eq!(norm(r"\\?\C:\Program Files\Unravis\Unravis.exe"), norm(r"c:\program files\unravis\unravis.exe"));
        assert_ne!(norm(r"C:\x\unravis.exe"), norm(r"C:\y\unravis.exe"));
    }

    #[test]
    fn a_pipe_that_was_never_made_does_not_exist() {
        assert!(!pipe_exists(r"\\.\pipe\unravis.ui.00000000000000000000000000000000"));
    }

    /// Failure help: print the tail of every log file the sidecar wrote under `dir`.
    fn dump_logs(dir: &Path) {
        let Ok(rd) = std::fs::read_dir(dir) else { return };
        for e in rd.flatten() {
            let p = e.path();
            if p.is_dir() {
                dump_logs(&p);
            } else if p.extension().is_some_and(|x| x == "log" || x == "txt") {
                let t = std::fs::read_to_string(&p).unwrap_or_default();
                let tail: String = t.chars().rev().take(4000).collect::<Vec<_>>().into_iter().rev().collect();
                eprintln!("--- {} ---\n{tail}", p.display());
            }
        }
    }

    /// The real path: the real unravis.exe built by CI (UNRAVIS_TEST_SIDECAR) is started inside the job, a second call attaches to it,
    /// and closing the job ends it. Skipped when the variable is not set (no Go build on the machine).
    #[test]
    fn real_sidecar_spawn_attach_and_job_kill() {
        let Some(src) = std::env::var_os("UNRAVIS_TEST_SIDECAR") else {
            eprintln!("UNRAVIS_TEST_SIDECAR is not set: skipped");
            return;
        };
        let root = std::env::temp_dir().join(format!("unravis-shell-test-{}", std::process::id()));
        let data = root.join("data");
        std::fs::create_dir_all(&data).unwrap();
        let exe = root.join("unravis.exe");
        std::fs::copy(&src, &exe).unwrap();
        // One test owns the process environment here. The child inherits it, so Go and Rust resolve the same run folder.
        unsafe {
            std::env::set_var("AUDIT_CONFIG_DIR", &data);
            std::env::remove_var("UNRAVIS_RUN_DIR");
            // The sidecar is an `e2e` build: it also accepts this test binary as a pipe client and as the "shell" for hello.
            std::env::set_var("UNRAVIS_E2E_ALLOW_IMAGE", std::env::current_exe().unwrap());
        }
        let dir = run_dir().unwrap();

        let first = attach_or_spawn_with(&dir, &exe).expect("spawn");
        assert!(first._job.is_some(), "the first call starts the owner inside the job");
        let pid = first.record.pid;
        let second = attach_or_spawn_with(&dir, &exe).expect("attach");
        assert!(second._job.is_none() && second.record.pid == pid, "the second call attaches to the same owner");

        // The real pipe: hello gives the token, the token passes the session check, a wrong token and a wrong server do not.
        let client = crate::pipe_peer::Client {
            name: second.record.ui.clone(),
            expect: crate::pipe_peer::ServerExpect { pid, image: exe.clone(), start_time: second.record.start_time.clone() },
        };
        let token = client.hello().unwrap_or_else(|e| {
            dump_logs(&root);
            panic!("hello over the ui pipe: {e:?}");
        });
        assert!(client.health(&token), "GET /api/version with the token answers 200");
        assert!(!client.health("not-the-token-not-the-token-xx"), "a wrong token is refused");
        let wrong = crate::pipe_peer::Client {
            name: client.name.clone(),
            expect: crate::pipe_peer::ServerExpect { pid, image: root.join("other.exe"), start_time: second.record.start_time.clone() },
        };
        assert!(matches!(wrong.hello(), Err(crate::pipe_peer::PipeError::ServerMismatch(_))), "a server that is not our image is refused");
        let wrong_pid = crate::pipe_peer::Client {
            name: client.name.clone(),
            expect: crate::pipe_peer::ServerExpect { pid: pid + 4, image: exe.clone(), start_time: second.record.start_time.clone() },
        };
        assert!(matches!(wrong_pid.hello(), Err(crate::pipe_peer::PipeError::ServerMismatch(_))));
        let wrong_start = crate::pipe_peer::Client {
            name: client.name.clone(),
            expect: crate::pipe_peer::ServerExpect { pid, image: exe.clone(), start_time: "2001-01-01T00:00:00Z".into() },
        };
        assert!(
            matches!(wrong_start.hello(), Err(crate::pipe_peer::PipeError::ServerMismatch(_))),
            "a reused pid (other start time) is refused"
        );
        let foreign = crate::pipe_peer::Client { name: r"\\evil\pipe\x".into(), expect: client.expect.clone() };
        assert!(matches!(foreign.hello(), Err(crate::pipe_peer::PipeError::ServerMismatch(_))), "only local unravis ui pipes are dialled");

        drop(second);
        drop(first); // closes the job: KILL_ON_JOB_CLOSE
        let gone = Instant::now() + Duration::from_secs(10);
        while Instant::now() < gone && read_record(&dir).is_some_and(|r| alive(&r, &exe)) {
            std::thread::sleep(Duration::from_millis(100));
        }
        assert!(!read_record(&dir).is_some_and(|r| alive(&r, &exe)), "the owner is gone after the job closed");
        let _ = std::fs::remove_dir_all(&root);
    }
}
