//! One shell per user and session (BL-309): a named mutex plus two auto-reset events, all in the `Local\` namespace and named with the
//! user's SID. The kernel gives the objects a default DACL that admits only the creator (and SYSTEM and administrators), so a program
//! of another user that took the name first makes our open fail with access denied: that is reported, never trusted.
//!
//! The second copy sets `.Open` (show and focus the window) or, for `--quit`, `.Quit`, then exits.

use windows_sys::Win32::Foundation::{CloseHandle, ERROR_ALREADY_EXISTS, HANDLE, LocalFree, WAIT_OBJECT_0};
use windows_sys::Win32::Security::Authorization::ConvertSidToStringSidW;
use windows_sys::Win32::Security::{GetTokenInformation, TOKEN_QUERY, TOKEN_USER, TokenUser};
use windows_sys::Win32::System::Threading::{
    CreateEventW, CreateMutexW, GetCurrentProcess, INFINITE, OpenProcessToken, SetEvent, WaitForMultipleObjects,
};
use windows_sys::Win32::UI::WindowsAndMessaging::{ASFW_ANY, AllowSetForegroundWindow};

pub struct First {
    _mutex: HANDLE,
    open: HANDLE,
    quit: HANDLE,
}

// Kernel handles: usable from any thread.
unsafe impl Send for First {}
unsafe impl Sync for First {}

pub enum Entry {
    First(First),
    /// Another shell of this user runs. The request (open or quit) was already sent to it.
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

fn user_sid() -> Result<String, String> {
    unsafe {
        let mut tok: HANDLE = std::ptr::null_mut();
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut tok) == 0 {
            return Err(format!("OpenProcessToken failed: {}", std::io::Error::last_os_error()));
        }
        let mut buf = [0u64; 64];
        let mut n = 0u32;
        let ok = GetTokenInformation(tok, TokenUser, buf.as_mut_ptr().cast(), size_of_val(&buf) as u32, &mut n);
        CloseHandle(tok);
        if ok == 0 {
            return Err(format!("GetTokenInformation failed: {}", std::io::Error::last_os_error()));
        }
        let user = &*(buf.as_ptr().cast::<TOKEN_USER>());
        let mut s: *mut u16 = std::ptr::null_mut();
        if ConvertSidToStringSidW(user.User.Sid, &mut s) == 0 {
            return Err(format!("ConvertSidToStringSid failed: {}", std::io::Error::last_os_error()));
        }
        let mut len = 0;
        while *s.add(len) != 0 {
            len += 1;
        }
        let out = String::from_utf16_lossy(std::slice::from_raw_parts(s, len));
        LocalFree(s.cast());
        Ok(out)
    }
}

fn event(name: &str) -> Result<HANDLE, String> {
    let w = wide(name);
    // Auto-reset, not signalled.
    let h = unsafe { CreateEventW(std::ptr::null(), 0, 0, w.as_ptr()) };
    if h.is_null() { Err(format!("cannot open {name}: {}", std::io::Error::last_os_error())) } else { Ok(h) }
}

/// `scope` is added to every name so tests can run beside a real shell.
pub fn enter(scope: &str, send: Signal) -> Result<Entry, String> {
    let base = format!("Local\\UnravisShell{scope}.{}", user_sid()?);
    let w = wide(&base);
    let m = unsafe { CreateMutexW(std::ptr::null(), 0, w.as_ptr()) };
    if m.is_null() {
        return Err(format!("another user holds the shell name ({})", std::io::Error::last_os_error()));
    }
    let already = unsafe { windows_sys::Win32::Foundation::GetLastError() } == ERROR_ALREADY_EXISTS;
    let open = event(&format!("{base}.Open"))?;
    let quit = event(&format!("{base}.Quit"))?;
    if !already {
        return Ok(Entry::First(First { _mutex: m, open, quit }));
    }
    unsafe {
        // The first copy may show its window in front of what the person is doing now.
        AllowSetForegroundWindow(ASFW_ANY);
        SetEvent(if send == Signal::Quit { quit } else { open });
        CloseHandle(open);
        CloseHandle(quit);
        CloseHandle(m);
    }
    Ok(Entry::Second)
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

    #[test]
    fn singleton_second_returns_false() {
        let s = scope();
        let Entry::First(first) = enter(&s, Signal::Open).unwrap() else { panic!("the first must be first") };
        assert!(matches!(enter(&s, Signal::Open).unwrap(), Entry::Second), "the second must be second");
        assert_eq!(first.wait(), Some(Signal::Open), "the second asked the first to open");
        assert!(matches!(enter(&s, Signal::Quit).unwrap(), Entry::Second));
        assert_eq!(first.wait(), Some(Signal::Quit));
    }

    #[test]
    fn user_sid_looks_like_a_sid() {
        let s = user_sid().unwrap();
        assert!(s.starts_with("S-1-"), "{s}");
    }
}
