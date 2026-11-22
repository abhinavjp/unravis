//! Unravis desktop shell (BL-306 skeleton). Thin by design (budget: under 1,500 lines for the whole crate).
//!
//! Flow: strip WEBVIEW2_* -> refuse administrator -> one shell per user (a second copy only asks the first to open or quit) -> find the
//! Go owner through backend.json (attach) or start it inside a Job Object (spawn) -> open the one window. The skeleton shows a
//! placeholder page; the pipe relay that fills it is BL-314.
#![cfg_attr(windows, windows_subsystem = "windows")]

#[cfg(windows)]
mod elevation;
#[cfg(windows)]
mod hardening;
#[cfg(windows)]
mod job;
#[cfg(windows)]
mod locator;
#[cfg(windows)]
mod lockdown;
#[cfg(windows)]
mod singleton;
#[cfg(windows)]
mod spawn;

#[cfg(windows)]
fn main() {
    use ipc_core::elevation::{Decision, EXIT_ADMIN, MESSAGE, decide};
    use ipc_core::policy::parse_args;

    lockdown::env_strip();
    let _mitigations = hardening::set_process_mitigations();
    let args = parse_args(std::env::args().skip(1));
    // `--quit` is a signal only: no window, no dialog, no elevation refusal. Exit 0 = asked (or nothing was running), 1 = could not ask.
    if args.quit {
        std::process::exit(if singleton::enter("", Some(singleton::Signal::Quit)).is_ok() { 0 } else { 1 });
    }
    if decide(elevation::token_facts()) == Decision::Refuse {
        elevation::show(MESSAGE);
        std::process::exit(EXIT_ADMIN);
    }
    if let Some(why) = hardening::webview2_policy_violation() {
        elevation::show(&why);
        std::process::exit(ipc_core::webview2_policy::EXIT_WEBVIEW2_POLICY);
    }
    // A copy started with `--background` (sign-in start) must not pull the first copy's window forward: it asks for nothing.
    let signal = if args.background { None } else { Some(singleton::Signal::Open) };
    let first = match singleton::enter("", signal) {
        Ok(singleton::Entry::First(f)) => f,
        Ok(singleton::Entry::Second) => return,
        Err(e) => {
            elevation::show(&format!("Unravis could not start.\n\n{e}"));
            std::process::exit(1);
        }
    };
    let backend = match locator::attach_or_spawn() {
        Ok(b) => b,
        Err(e) => {
            elevation::show(&format!("Unravis could not start its background part.\n\n{e}"));
            std::process::exit(1);
        }
    };
    run_window(backend, first);
}

#[cfg(windows)]
fn run_window(backend: locator::Backend, first: singleton::First) {
    use tauri::Manager;

    tauri::Builder::default()
        .setup(move |app| {
            lockdown::build_main_window(app.handle())?;
            // The job (when this shell started the owner) lives as long as the app: closing it kills the sidecar.
            app.manage(backend);
            let handle = app.handle().clone();
            std::thread::spawn(move || {
                while let Some(sig) = first.wait() {
                    match sig {
                        singleton::Signal::Open => {
                            let h = handle.clone();
                            let _ = handle.run_on_main_thread(move || {
                                if let Some(w) = h.get_webview_window("main") {
                                    let _ = w.unminimize();
                                    let _ = w.show();
                                    let _ = w.set_focus();
                                }
                            });
                        }
                        singleton::Signal::Quit => {
                            handle.exit(0);
                            break;
                        }
                    }
                }
            });
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
