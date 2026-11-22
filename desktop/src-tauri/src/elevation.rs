//! Reads the shell's own token (facts only; the decision is in ipc-core) and shows a message box, since the shell has no console.

use ipc_core::elevation::TokenFacts;
use std::mem::{size_of, zeroed};
use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
use windows_sys::Win32::Security::{
    GetSidSubAuthority, GetSidSubAuthorityCount, GetTokenInformation, TOKEN_ELEVATION, TOKEN_MANDATORY_LABEL, TOKEN_QUERY, TokenElevation,
    TokenElevationType, TokenIntegrityLevel,
};
use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};
use windows_sys::Win32::UI::WindowsAndMessaging::{MB_ICONERROR, MB_OK, MessageBoxW};

const ELEVATION_TYPE_FULL: i32 = 2;
const INTEGRITY_HIGH: u32 = 0x3000;

/// Fails closed: when any read fails the shell counts as elevated and refuses.
pub fn token_facts() -> TokenFacts {
    unsafe {
        let mut tok: HANDLE = std::ptr::null_mut();
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut tok) == 0 {
            return TokenFacts::UNREADABLE;
        }
        let facts = read(tok);
        CloseHandle(tok);
        facts.unwrap_or(TokenFacts::UNREADABLE)
    }
}

unsafe fn read(tok: HANDLE) -> Option<TokenFacts> {
    unsafe {
        let mut n = 0u32;
        let mut elev: TOKEN_ELEVATION = zeroed();
        if GetTokenInformation(tok, TokenElevation, (&mut elev as *mut TOKEN_ELEVATION).cast(), size_of::<TOKEN_ELEVATION>() as u32, &mut n)
            == 0
        {
            return None;
        }
        let mut kind: i32 = 0;
        if GetTokenInformation(tok, TokenElevationType, (&mut kind as *mut i32).cast(), size_of::<i32>() as u32, &mut n) == 0 {
            return None;
        }
        // TOKEN_MANDATORY_LABEL is a pointer to a SID that follows it in the same buffer; 256 bytes is far more than any label.
        let mut buf = [0u64; 32];
        if GetTokenInformation(tok, TokenIntegrityLevel, buf.as_mut_ptr().cast(), size_of_val(&buf) as u32, &mut n) == 0 {
            return None;
        }
        let label = &*(buf.as_ptr().cast::<TOKEN_MANDATORY_LABEL>());
        let sid = label.Label.Sid;
        if sid.is_null() {
            return None;
        }
        let count = *GetSidSubAuthorityCount(sid);
        if count == 0 {
            return None;
        }
        let level = *GetSidSubAuthority(sid, u32::from(count) - 1);
        Some(TokenFacts {
            full_token: kind == ELEVATION_TYPE_FULL,
            elevated: elev.TokenIsElevated != 0,
            integrity_high: level >= INTEGRITY_HIGH,
        })
    }
}

pub fn show(text: &str) {
    let wide: Vec<u16> = text.encode_utf16().chain(Some(0)).collect();
    let title: Vec<u16> = "Unravis".encode_utf16().chain(Some(0)).collect();
    unsafe {
        MessageBoxW(std::ptr::null_mut(), wide.as_ptr(), title.as_ptr(), MB_OK | MB_ICONERROR);
    }
}
