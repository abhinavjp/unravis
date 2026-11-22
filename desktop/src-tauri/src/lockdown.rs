//! WebView2 and window lockdown (BL-311, BL-328). One place configures the one window; `build_main_window` is the only constructor.

use ipc_core::policy;
use std::path::PathBuf;
use tauri::webview::{DownloadEvent, NewWindowResponse};
use tauri::{AppHandle, Runtime, WebviewUrl, WebviewWindow, WebviewWindowBuilder};

/// Removes every `WEBVIEW2_*` variable. Called first in `main`, while the process has one thread.
pub fn env_strip() {
    let names: Vec<_> =
        std::env::vars_os().filter_map(|(k, _)| k.to_str().map(str::to_owned)).filter(|k| policy::is_webview2_var(k)).collect();
    for n in names {
        // SAFETY: called at the start of main, before any other thread exists.
        unsafe { std::env::remove_var(n) };
    }
}

/// The WebView2 profile folder, inside the per-user data folder (never the exe folder).
pub fn webview2_dir() -> Option<PathBuf> {
    let base = std::env::var_os("LOCALAPPDATA").filter(|v| !v.is_empty())?;
    Some(PathBuf::from(base).join("Unravis").join("webview2"))
}

pub fn build_main_window<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<WebviewWindow<R>> {
    let mut b = WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html".into()))
        .title("Unravis")
        .inner_size(1280.0, 800.0)
        .use_https_scheme(true)
        .devtools(cfg!(feature = "e2e"))
        .disable_drag_drop_handler()
        .browser_extensions_enabled(false)
        .zoom_hotkeys_enabled(false)
        .general_autofill_enabled(false)
        .on_navigation(|url| policy::navigation_allowed(url.as_str()))
        .on_new_window(|_url, _features| NewWindowResponse::Deny)
        .on_download(|_w, ev| !matches!(ev, DownloadEvent::Requested { .. }));
    if let Some(dir) = webview2_dir() {
        b = b.data_directory(dir);
    }
    b.build()
}
