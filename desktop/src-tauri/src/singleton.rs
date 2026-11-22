//! One shell per user and session (BL-309): a named mutex, two auto-reset events and a small named memory with the first shell's pid,
//! all in the `Local\` namespace and named with the user's SID.
//!
//! Nothing is trusted just because it has the right name: every object that already exists is checked for its owner (the person's
//! SID or the token's default owner), as Go's `internal/instance` does. A foreign owner is refused, never adopted. A second copy only
//! OPENS the events (it never creates them), so a signal cannot be lost on an object nobody else holds, and `--quit` never touches the
//! mutex: with no shell running it finds nothing and does nothing.

use std::ptr::null_mut;
use std::time::Duration;
use windows_sys::Win32::Foundation::{
    CloseHandle, ERROR_ALREADY_EXISTS, ERROR_FILE_NOT_FOUND, GetLastError, HANDLE, INVALID_HANDLE_VALUE, LocalFree, WAIT_OBJECT_0,
};
use windows_sys::Win32::Security::Authorization::{ConvertSidToStringSidW, GetSecurityInfo, SE_KERNEL_OBJECT};
use windows_sys::Win32::Security::{
    GetTokenInformation, OWNER_SECURITY_INFORMATION, PSID, TOKEN_OWNER, TOKEN_QUERY, TOKEN_USER, TokenOwner, TokenUser,
};
use windows_sys::Win32::System::Memory::{
    CreateFileMappingW, FILE_MAP_READ, FILE_MAP_WRITE, MapViewOfFile, OpenFileMappingW, PAGE_READWRITE, UnmapViewOfFile,
};
use windows_sys::Win32::System::Threading::{
    CreateEventW, CreateMutexW, EVENT_MODIFY_STATE, GetCurrentProcess, GetCurrentProcessId, INFINITE, OpenEventW, OpenProcessToken,
    SetEvent, WaitForMultipleObjects,
};
use windows_sys::Win32::UI::WindowsAndMessaging::AllowSetForegroundWindow;

/// Access right to read the owner of an object.
const READ_CONTROL: u32 = 0x0002_0000;

pub struct First {
    _mutex: HANDLE,
    _pid_map: HANDLE,
    open: HANDLE,
    quit: HANDLE,
}

// Kernel handles: usable from any thread.
unsafe impl Send for First {}
unsafe impl Sync for First {}

pub enum Entry {
    First(First),
    /// Another shell of this user runs (or, for a quit request, none does). Any request was already sent.
    Second,
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Signal {
    Open,
    Quit,
}

fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(Some(0)).collect()
}

fn os_err(what: &str) -> String {
    format!("{what}: {}", std::io::Error::last_os_error())
}

unsafe fn sid_string(sid: PSID) -> Option<String> {
    unsafe {
        let mut s: *mut u16 = null_mut();
        if ConvertSidToStringSidW(sid, &mut s) == 0 {
            return None;
        }
        let mut len = 0;
        while *s.add(len) != 0 {
            len += 1;
        }
        let out = String::from_utf16_lossy(std::slice::from_raw_parts(s, len));
        LocalFree(s.cast());
        Some(out)
    }
}

/// The person's SID and the token's default owner SID, as strings.
fn token_sids() -> Result<(String, String), String> {
    unsafe {
        let mut tok: HANDLE = null_mut();
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut tok) == 0 {
            return Err(os_err("OpenProcessToken failed"));
        }
        let (mut ub, mut ob) = ([0u64; 64], [0u64; 64]);
        let mut n = 0u32;
        let a = GetTokenInformation(tok, TokenUser, ub.as_mut_ptr().cast(), size_of_val(&ub) as u32, &mut n);
        let ea = std::io::Error::last_os_error();
        let b = GetTokenInformation(tok, TokenOwner, ob.as_mut_ptr().cast(), size_of_val(&ob) as u32, &mut n);
        let eb = std::io::Error::last_os_error();
        CloseHandle(tok);
        if a == 0 {
            return Err(format!("GetTokenInformation(user) failed: {ea}"));
        }
        if b == 0 {
            return Err(format!("GetTokenInformation(owner) failed: {eb}"));
        }
        let user = sid_string((*(ub.as_ptr().cast::<TOKEN_USER>())).User.Sid).ok_or("cannot read the user SID")?;
        let owner = sid_string((*(ob.as_ptr().cast::<TOKEN_OWNER>())).Owner).ok_or("cannot read the owner SID")?;
        Ok((user, owner))
    }
}

fn user_sid() -> Result<String, String> {
    token_sids().map(|(u, _)| u)
}

/// True when the object behind `h` is owned by the person (or the token's default owner).
fn owned_by_me(h: HANDLE) -> bool {
    let Ok((user, owner)) = token_sids() else { return false };
    unsafe {
        let mut osid: PSID = null_mut();
        let mut sd = null_mut();
        let rc = GetSecurityInfo(h, SE_KERNEL_OBJECT, OWNER_SECURITY_INFORMATION, &mut osid, null_mut(), null_mut(), null_mut(), &mut sd);
        if rc != 0 {
            return false;
        }
        let s = sid_string(osid);
        LocalFree(sd);
        s.is_some_and(|s| s == user || s == owner)
    }
}

type OwnerCheck<'a> = &'a dyn Fn(HANDLE) -> bool;

fn open_event(name: &str, retries: u32, ok: OwnerCheck) -> Result<Option<HANDLE>, String> {
    let w = wide(name);
    for i in 0..=retries {
        let h = unsafe { OpenEventW(EVENT_MODIFY_STATE | READ_CONTROL, 0, w.as_ptr()) };
        if !h.is_null() {
            if !ok(h) {
                unsafe { CloseHandle(h) };
                return Err("another user holds the shell name".into());
            }
            return Ok(Some(h));
        }
        let code = unsafe { GetLastError() };
        if code != ERROR_FILE_NOT_FOUND {
            return Err(format!("cannot open {name}: error {code}"));
        }
        if i < retries {
            std::thread::sleep(Duration::from_millis(50));
        }
    }
    Ok(None)
}

fn create_event(name: &str) -> Result<HANDLE, String> {
    let w = wide(name);
    // Auto-reset, not signalled.
    let h = unsafe { CreateEventW(std::ptr::null(), 0, 0, w.as_ptr()) };
    if h.is_null() {
        return Err(os_err(&format!("cannot create {name}")));
    }
    if unsafe { GetLastError() } == ERROR_ALREADY_EXISTS {
        unsafe { CloseHandle(h) };
        return Err(format!("{name} already exists but no shell holds the mutex: refused"));
    }
    Ok(h)
}

fn read_pid(name: &str, ok: OwnerCheck) -> Option<u32> {
    let w = wide(name);
    unsafe {
        let m = OpenFileMappingW(FILE_MAP_READ | READ_CONTROL, 0, w.as_ptr());
        if m.is_null() {
            return None;
        }
        let mut pid = None;
        if ok(m) {
            let v = MapViewOfFile(m, FILE_MAP_READ, 0, 0, 4);
            if !v.Value.is_null() {
                pid = Some(*(v.Value as *const u32));
                UnmapViewOfFile(v);
            }
        }
        CloseHandle(m);
        pid
    }
}

/// `scope` is added to every name so tests can run beside a real shell. `send` is what a second copy asks of the first (None: nothing).
pub fn enter(scope: &str, send: Option<Signal>) -> Result<Entry, String> {
    enter_with(scope, send, &owned_by_me)
}

pub fn enter_with(scope: &str, send: Option<Signal>, ok: OwnerCheck) -> Result<Entry, String> {
    let base = format!("Local\\UnravisShell{scope}.{}", user_sid()?);
    let (open_name, quit_name, pid_name) = (format!("{base}.Open"), format!("{base}.Quit"), format!("{base}.Pid"));

    if send == Some(Signal::Quit) {
        // Never touches the mutex. No event: no shell is running, nothing to do.
        if let Some(h) = open_event(&quit_name, 0, ok)? {
            unsafe {
                SetEvent(h);
                CloseHandle(h);
            }
        }
        return Ok(Entry::Second);
    }

    let w = wide(&base);
    let m = unsafe { CreateMutexW(std::ptr::null(), 0, w.as_ptr()) };
    if m.is_null() {
        return Err(os_err("cannot take the shell name"));
    }
    if unsafe { GetLastError() } == ERROR_ALREADY_EXISTS {
        let result = (|| {
            if !ok(m) {
                return Err("another user holds the shell name".to_string());
            }
            let Some(sig) = send else { return Ok(()) };
            // The first copy may still be creating its events: wait a moment for them.
            let Some(ev) = open_event(&open_name, 10, ok)? else {
                return Err("the running Unravis does not answer".to_string());
            };
            if let Some(pid) = read_pid(&pid_name, ok) {
                unsafe { AllowSetForegroundWindow(pid) };
            }
            debug_assert_eq!(sig, Signal::Open);
            unsafe {
                SetEvent(ev);
                CloseHandle(ev);
            }
            Ok(())
        })();
        unsafe { CloseHandle(m) };
        return result.map(|()| Entry::Second);
    }

    // First. Events and the pid map must be ours alone: one that already exists was made by someone else.
    let first = (|| {
        let open = create_event(&open_name)?;
        let quit = create_event(&quit_name).inspect_err(|_| unsafe {
            CloseHandle(open);
        })?;
        let pw = wide(&pid_name);
        let map = unsafe { CreateFileMappingW(INVALID_HANDLE_VALUE, std::ptr::null(), PAGE_READWRITE, 0, 4, pw.as_ptr()) };
        if map.is_null() || unsafe { GetLastError() } == ERROR_ALREADY_EXISTS {
            unsafe {
                CloseHandle(open);
                CloseHandle(quit);
                if !map.is_null() {
                    CloseHandle(map);
                }
            }
            return Err("the shell's pid record already exists: refused".to_string());
        }
        unsafe {
            let v = MapViewOfFile(map, FILE_MAP_WRITE, 0, 0, 4);
            if !v.Value.is_null() {
                *(v.Value as *mut u32) = GetCurrentProcessId();
                UnmapViewOfFile(v);
            }
        }
        Ok((open, quit, map))
    })();
    match first {
        Ok((open, quit, map)) => Ok(Entry::First(First { _mutex: m, _pid_map: map, open, quit })),
        Err(e) => {
            unsafe { CloseHandle(m) };
            Err(e)
        }
    }
}

impl First {
    /// Blocks until the next request from a second copy. `None` means the wait itself failed.
    pub fn wait(&self) -> Option<Signal> {
        let hs = [self.open, self.quit];
        let r = unsafe { WaitForMultipleObjects(2, hs.as_ptr(), 0, INFINITE) };
        match r.checked_sub(WAIT_OBJECT_0) {
            Some(0) => Some(Signal::Open),
            Some(1) => Some(Signal::Quit),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn scope() -> String {
        format!("Test{}", std::process::id())
    }

    fn first(s: &str) -> First {
        match enter(s, Some(Signal::Open)).unwrap() {
            Entry::First(f) => f,
            Entry::Second => panic!("the first must be first"),
        }
    }

    #[test]
    fn singleton_second_returns_false() {
        let s = scope();
        let f = first(&s);
        assert!(matches!(enter(&s, Some(Signal::Open)).unwrap(), Entry::Second), "the second must be second");
        assert_eq!(f.wait(), Some(Signal::Open), "the second asked the first to open");
        assert!(matches!(enter(&s, Some(Signal::Quit)).unwrap(), Entry::Second));
        assert_eq!(f.wait(), Some(Signal::Quit));
        // A second copy started with --background asks for nothing.
        assert!(matches!(enter(&s, None).unwrap(), Entry::Second));
    }

    #[test]
    fn quit_with_no_shell_does_nothing_and_takes_no_name() {
        let s = format!("{}NoShell", scope());
        assert!(matches!(enter(&s, Some(Signal::Quit)).unwrap(), Entry::Second));
        // The quit request did not leave a fake first behind: a real start is first.
        let _f = first(&s);
    }

    #[test]
    fn singleton_rejects_foreign_owner() {
        let s = format!("{}Foreign", scope());
        let _f = first(&s);
        let deny: OwnerCheck = &|_| false;
        assert!(enter_with(&s, Some(Signal::Open), deny).is_err(), "an object owned by someone else is refused");
        assert!(enter_with(&s, Some(Signal::Quit), deny).is_err(), "so is a quit request to it");
    }

    #[test]
    fn real_owner_check_accepts_our_own_objects() {
        let s = format!("{}Own", scope());
        let f = first(&s);
        assert!(owned_by_me(f._mutex));
        assert!(owned_by_me(f.open));
    }

    #[test]
    fn pre_made_events_are_refused() {
        let s = format!("{}Squat", scope());
        let name = format!("Local\\UnravisShell{s}.{}.Open", user_sid().unwrap());
        let squatter = create_event(&name).unwrap();
        assert!(enter(&s, Some(Signal::Open)).is_err(), "events that exist before the first shell are not adopted");
        unsafe { CloseHandle(squatter) };
    }

    #[test]
    fn user_sid_looks_like_a_sid() {
        assert!(user_sid().unwrap().starts_with("S-1-"));
    }
}
