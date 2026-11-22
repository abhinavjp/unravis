//! Startup hardening (BL-325, BL-326): WebView2 policy check and process mitigations.
//!
//! Mitigations set at run time are weaker than flags given at process creation (anything loaded before the call is not covered); the
//! linker flags in `.cargo/config.toml` and the PE check cover the rest. ACG is NOT enabled here: it is unproven with the WebView2 loader.

use ipc_core::webview2_policy::first_violation;
use std::ptr::null_mut;
use windows_sys::Win32::System::Registry::{
    HKEY, HKEY_CURRENT_USER, HKEY_LOCAL_MACHINE, KEY_QUERY_VALUE, KEY_WOW64_64KEY, REG_SZ, RegCloseKey, RegOpenKeyExW, RegQueryValueExW,
};
use windows_sys::Win32::System::Threading::{
    ProcessExtensionPointDisablePolicy, ProcessImageLoadPolicy, ProcessStrictHandleCheckPolicy, SetProcessMitigationPolicy,
};

const KEY: &str = "SOFTWARE\\Policies\\Microsoft\\Edge\\WebView2\\AdditionalBrowserArguments";
const EXE_NAME: &str = "unravis-desktop.exe";

fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(Some(0)).collect()
}

fn read_value(root: HKEY, name: &str) -> Option<String> {
    unsafe {
        let mut key: HKEY = null_mut();
        if RegOpenKeyExW(root, wide(KEY).as_ptr(), 0, KEY_QUERY_VALUE | KEY_WOW64_64KEY, &mut key) != 0 {
            return None;
        }
        let wname = wide(name);
        let (mut ty, mut bytes) = (0u32, 0u32);
        let probe = RegQueryValueExW(key, wname.as_ptr(), std::ptr::null(), &mut ty, null_mut(), &mut bytes);
        let mut out = None;
        if probe == 0 && ty == REG_SZ && (2..=64 * 1024).contains(&bytes) {
            let mut buf = vec![0u16; (bytes as usize).div_ceil(2)];
            let mut n = bytes;
            if RegQueryValueExW(key, wname.as_ptr(), std::ptr::null(), &mut ty, buf.as_mut_ptr().cast(), &mut n) == 0 {
                let len = buf.iter().position(|&c| c == 0).unwrap_or(buf.len());
                out = Some(String::from_utf16_lossy(&buf[..len]));
            }
        }
        RegCloseKey(key);
        out
    }
}

/// `Some(message)` when HKCU or HKLM policy gives WebView2 a dangerous flag, for this exe or for all (`*`). A failed read counts as
/// "no value" (the key normally does not exist).
pub fn webview2_policy_violation() -> Option<String> {
    let mut found: Vec<(&str, String)> = Vec::new();
    for (label, root, name) in [
        ("HKCU *", HKEY_CURRENT_USER, "*"),
        ("HKCU unravis-desktop.exe", HKEY_CURRENT_USER, EXE_NAME),
        ("HKLM *", HKEY_LOCAL_MACHINE, "*"),
        ("HKLM unravis-desktop.exe", HKEY_LOCAL_MACHINE, EXE_NAME),
    ] {
        if let Some(v) = read_value(root, name) {
            found.push((label, v));
        }
    }
    first_violation(&found).map(|(place, flags)| {
        format!(
            "Windows policy gives the embedded browser unsafe options ({}) at {KEY}\\{place}. Ask your administrator to remove them.",
            flags.join(", ")
        )
    })
}

/// Strict handle checks, no remote or low-integrity images and System32-first image search, extension points off. Failures are
/// ignored: an older Windows may not know a policy, and the shell must still start.
pub fn set_process_mitigations() {
    // Each policy struct is one DWORD of flag bits.
    let set = |policy, flags: u32| unsafe {
        SetProcessMitigationPolicy(policy, (&flags as *const u32).cast(), size_of::<u32>());
    };
    set(ProcessStrictHandleCheckPolicy, 0x3); // RaiseExceptionOnInvalidHandleReference | HandleExceptionsPermanentlyEnabled
    set(ProcessImageLoadPolicy, 0x7); // NoRemoteImages | NoLowMandatoryLabelImages | PreferSystem32Images
    set(ProcessExtensionPointDisablePolicy, 0x1);
}
