//! Unravis desktop shell (BL-306 skeleton). Thin by design (budget: under 1,500 lines for the whole crate).
//!
//! Flow: refuse administrator -> find the Go owner through backend.json (attach) or start it inside a Job Object (spawn) -> open the
//! one window. The skeleton shows a placeholder page; the pipe relay that fills it is BL-314.
#![cfg_attr(windows, windows_subsystem = "windows")]

#[cfg(windows)]
mod elevation;
#[cfg(windows)]
mod job;
#[cfg(windows)]
mod locator;
#[cfg(windows)]
mod spawn;

#[cfg(windows)]
fn main() {
    use ipc_core::elevation::{Decision, EXIT_ADMIN, MESSAGE, decide};

    if decide(elevation::token_facts()) == Decision::Refuse {
        elevation::show(MESSAGE);
        std::process::exit(EXIT_ADMIN);
    }
    let backend = match locator::attach_or_spawn() {
        Ok(b) => b,
        Err(e) => {
            elevation::show(&format!("Unravis could not start its background part.\n\n{e}"));
            std::process::exit(1);
        }
    };
    run_window(backend);
}

#[cfg(windows)]
fn run_window(backend: locator::Backend) {
    use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

    tauri::Builder::default()
        .setup(move |app| {
            WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html".into()))
                .title("Unravis")
                .inner_size(1280.0, 800.0)
                .build()?;
            // The job (when this shell started the owner) lives as long as the app: closing it kills the sidecar.
            app.manage(backend);
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("the shell could not start");
}

#[cfg(not(windows))]
fn main() {
    eprintln!("unravis-desktop runs on Windows only");
    std::process::exit(1);
}
