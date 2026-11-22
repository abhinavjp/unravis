//! Reads the shell's own token (facts only; the decision is in ipc-core) and shows a message box, since the shell has no console.

use ipc_core::elevation::TokenFacts;
use std::mem::{size_of, zeroed};
use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
use windows_sys::Win32::Security::{
    CheckTokenMembership, CreateWellKnownSid, GetTokenInformation, TOKEN_ELEVATION, TOKEN_QUERY, TokenElevation, TokenElevationType,
    WinBuiltinAdministratorsSid,
};
use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};
use windows_sys::Win32::UI::WindowsAndMessaging::{MB_ICONERROR, MB_OK, MessageBoxW};

const ELEVATION_TYPE_DEFAULT: i32 = 1;

/// Fails closed: when the token cannot be read the shell counts as elevated and refuses.
pub fn token_facts() -> TokenFacts {
    let closed = TokenFacts { elevated: true, admin_member_filtered: false, uac_off_admin: false };
    unsafe {
        let mut tok: HANDLE = std::ptr::null_mut();
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut tok) == 0 {
            return closed;
        }
        let mut elev: TOKEN_ELEVATION = zeroed();
        let mut n = 0u32;
        let ok1 = GetTokenInformation(
            tok,
            TokenElevation,
            (&mut elev as *mut TOKEN_ELEVATION).cast(),
            size_of::<TOKEN_ELEVATION>() as u32,
            &mut n,
        );
        let mut kind: i32 = 0;
        let ok2 = GetTokenInformation(tok, TokenElevationType, (&mut kind as *mut i32).cast(), size_of::<i32>() as u32, &mut n);
        CloseHandle(tok);
        if ok1 == 0 || ok2 == 0 {
            return closed;
        }
        let admin = is_admin_member();
        TokenFacts {
            elevated: elev.TokenIsElevated != 0,
            admin_member_filtered: kind == 3 && admin,
            // Default type plus administrator membership: no UAC split, so every program runs with the full token.
            uac_off_admin: kind == ELEVATION_TYPE_DEFAULT && admin,
        }
    }
}

fn is_admin_member() -> bool {
    let mut sid = [0u8; 68];
    let mut len = sid.len() as u32;
    unsafe {
        if CreateWellKnownSid(WinBuiltinAdministratorsSid, std::ptr::null_mut(), sid.as_mut_ptr().cast(), &mut len) == 0 {
            return false;
        }
        let mut member = 0;
        // A null token asks about the effective token of the calling thread. For a filtered token the group is deny-only, so this is false.
        CheckTokenMembership(std::ptr::null_mut(), sid.as_mut_ptr().cast(), &mut member) != 0 && member != 0
    }
}

pub fn show(text: &str) {
    let wide: Vec<u16> = text.encode_utf16().chain(Some(0)).collect();
    let title: Vec<u16> = "Unravis".encode_utf16().chain(Some(0)).collect();
    unsafe {
        MessageBoxW(std::ptr::null_mut(), wide.as_ptr(), title.as_ptr(), MB_OK | MB_ICONERROR);
    }
}
