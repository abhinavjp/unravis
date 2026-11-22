//! Verified client of the ui pipe (spec 02 section 3.2). The shell talks to its Go owner only through here: the server end is checked
//! BEFORE anything is sent (pid is the one the locator names, same session, image is our sidecar), the open asks for identification
//! level only (the server cannot impersonate us), and every call has a time limit that cancels the I/O.
//!
//! One request per connection (`Connection: close`). A pooled keep-alive client comes with the relay (BL-314).

#![allow(dead_code)] // the supervisor wiring (BL-307) and the relay (BL-314) call this next

use ipc_core::http::{HttpError, Response, build_request, parse_hello, parse_response};
use std::fs::{File, OpenOptions};
use std::io::{Read, Write};
use std::os::windows::fs::OpenOptionsExt;
use std::os::windows::io::AsRawHandle;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::{Duration, Instant};
use windows_sys::Win32::Foundation::CloseHandle;
use windows_sys::Win32::System::IO::CancelIoEx;
use windows_sys::Win32::System::Pipes::{GetNamedPipeServerProcessId, GetNamedPipeServerSessionId, WaitNamedPipeW};
use windows_sys::Win32::System::RemoteDesktop::ProcessIdToSessionId;
use windows_sys::Win32::System::Threading::{
    GetCurrentProcessId, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION, QueryFullProcessImageNameW,
};

const SECURITY_IDENTIFICATION: u32 = 0x0001_0000;
const ERROR_PIPE_BUSY: i32 = 231;
/// Room for the status line and headers on top of the body limit.
const HEAD_ROOM: usize = 16 * 1024;
/// How long a connect waits while every pipe instance is busy.
const BUSY_WAIT: Duration = Duration::from_secs(5);

/// Stable codes the relay and the UI see (spec 02, error codes).
#[derive(Debug)]
pub enum PipeError {
    Busy,
    Gone(String),
    ServerMismatch(&'static str),
    Timeout,
    Http(HttpError),
}

impl PipeError {
    pub fn code(&self) -> &'static str {
        match self {
            PipeError::Busy => "PIPE_BUSY",
            PipeError::Gone(_) | PipeError::Timeout | PipeError::Http(_) => "PIPE_GONE",
            PipeError::ServerMismatch(_) => "PIPE_SERVER_MISMATCH",
        }
    }
}

/// What the other end of the pipe has to be.
#[derive(Clone, Debug)]
pub struct ServerExpect {
    pub pid: u32,
    /// Full path of the sidecar the shell would start itself (not the `exe` field of the locator file).
    pub image: PathBuf,
}

fn norm(p: &str) -> String {
    p.strip_prefix(r"\\?\").unwrap_or(p).replace('/', r"\").to_lowercase()
}

fn image_of(pid: u32) -> Option<String> {
    unsafe {
        let h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
        if h.is_null() {
            return None;
        }
        let mut buf = [0u16; 1024];
        let mut len = buf.len() as u32;
        let ok = QueryFullProcessImageNameW(h, 0, buf.as_mut_ptr(), &mut len) != 0;
        CloseHandle(h);
        ok.then(|| String::from_utf16_lossy(&buf[..len as usize]))
    }
}

fn verify(f: &File, expect: &ServerExpect) -> Result<(), PipeError> {
    let h = f.as_raw_handle();
    let (mut pid, mut session) = (0u32, 0u32);
    unsafe {
        if GetNamedPipeServerProcessId(h, &mut pid) == 0 || GetNamedPipeServerSessionId(h, &mut session) == 0 {
            return Err(PipeError::ServerMismatch("server identity unreadable"));
        }
        let mut mine = 0u32;
        if ProcessIdToSessionId(GetCurrentProcessId(), &mut mine) == 0 || mine != session {
            return Err(PipeError::ServerMismatch("server is in another session"));
        }
    }
    if pid != expect.pid {
        return Err(PipeError::ServerMismatch("server pid is not the one in the locator"));
    }
    match image_of(pid) {
        Some(img) if norm(&img) == norm(&expect.image.to_string_lossy()) => Ok(()),
        _ => Err(PipeError::ServerMismatch("server image is not our sidecar")),
    }
}

/// Open `name` at identification level and verify the server before returning. Waits up to `BUSY_WAIT` while all instances are busy.
pub fn connect_verified(name: &str, expect: &ServerExpect) -> Result<File, PipeError> {
    let deadline = Instant::now() + BUSY_WAIT;
    loop {
        match OpenOptions::new().read(true).write(true).security_qos_flags(SECURITY_IDENTIFICATION).open(name) {
            Ok(f) => {
                verify(&f, expect)?; // dropping `f` closes the handle: nothing was sent
                return Ok(f);
            }
            Err(e) if e.raw_os_error() == Some(ERROR_PIPE_BUSY) => {
                let left = deadline.saturating_duration_since(Instant::now());
                if left.is_zero() {
                    return Err(PipeError::Busy);
                }
                let w: Vec<u16> = name.encode_utf16().chain(Some(0)).collect();
                unsafe { WaitNamedPipeW(w.as_ptr(), left.as_millis().clamp(1, 250) as u32) };
            }
            Err(e) => return Err(PipeError::Gone(e.to_string())),
        }
    }
}

#[derive(Clone, Debug)]
pub struct Client {
    pub name: String,
    pub expect: ServerExpect,
}

impl Client {
    /// One request. The whole call, connect included in the I/O part, is cancelled after `limit`.
    pub fn call(
        &self,
        method: &str,
        path: &str,
        token: Option<&str>,
        body: &[u8],
        max_body: usize,
        limit: Duration,
    ) -> Result<Response, PipeError> {
        let req = build_request(method, path, token, body).map_err(PipeError::Http)?;
        let file = Arc::new(connect_verified(&self.name, &self.expect)?);
        let (tx, rx) = std::sync::mpsc::channel();
        let io = Arc::clone(&file);
        std::thread::spawn(move || {
            let r = (|| -> std::io::Result<Vec<u8>> {
                (&*io).write_all(&req)?;
                let mut out = Vec::new();
                (&*io).take((max_body + HEAD_ROOM) as u64 + 1).read_to_end(&mut out)?;
                Ok(out)
            })();
            let _ = tx.send(r);
        });
        match rx.recv_timeout(limit) {
            Ok(Ok(raw)) if raw.len() > max_body + HEAD_ROOM => Err(PipeError::Http(HttpError::TooLarge)),
            Ok(Ok(raw)) => parse_response(&raw, max_body).map_err(PipeError::Http),
            Ok(Err(e)) => Err(PipeError::Gone(e.to_string())),
            Err(_) => {
                // Ends the blocked read or write so the worker thread returns and drops its handle.
                unsafe { CancelIoEx(file.as_raw_handle(), std::ptr::null()) };
                Err(PipeError::Timeout)
            }
        }
    }

    /// The session token, from the ui-pipe-only hello route (the locator carries none).
    pub fn hello(&self) -> Result<String, PipeError> {
        let r = self.call("GET", "/api/desktop/hello", None, b"", 4096, Duration::from_secs(5))?;
        if r.status != 200 {
            return Err(PipeError::Gone(format!("hello answered {}", r.status)));
        }
        parse_hello(&r.body).ok_or(PipeError::Http(HttpError::Malformed("hello body")))
    }

    /// One health check: `GET /api/version` with the token, 2 s limit (spec 01 BL-307).
    pub fn health(&self, token: &str) -> bool {
        matches!(self.call("GET", "/api/version", Some(token), b"", 4096, Duration::from_secs(2)), Ok(r) if r.status == 200)
    }
}
