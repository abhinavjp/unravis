//! Window policy that needs no OS: which URLs the one window may show, which environment variables are removed at start, and which
//! command-line arguments the shell understands. WebView2 is told to use the https scheme, so the app origin is `https://tauri.localhost`.

/// The origin of the bundled UI on Windows with `use_https_scheme(true)`.
pub const APP_HOST: &str = "tauri.localhost";

/// A navigation is allowed only inside the app origin. Anything else, including `about:blank` (it would inherit the app origin), `http`,
/// `file`, `data`, `javascript`, other hosts, userinfo tricks and other ports, is refused.
pub fn navigation_allowed(url: &str) -> bool {
    let Some(rest) = url.strip_prefix("https://") else { return false };
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    rest[..end].eq_ignore_ascii_case(APP_HOST)
}

/// `WEBVIEW2_*` variables can add browser arguments (a debug port), swap the browser or redirect the data folder: the shell removes
/// them all before WebView2 starts.
pub fn is_webview2_var(name: &str) -> bool {
    name.get(..9).is_some_and(|p| p.eq_ignore_ascii_case("WEBVIEW2_"))
}

#[derive(Debug, Default, PartialEq, Eq)]
pub struct Args {
    pub quit: bool,
    pub background: bool,
}

/// Unknown arguments are dropped, never passed on.
pub fn parse_args<I: IntoIterator<Item = String>>(args: I) -> Args {
    let mut a = Args::default();
    for s in args {
        match s.as_str() {
            "--quit" => a.quit = true,
            "--background" => a.background = true,
            _ => {}
        }
    }
    a
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn navigation_blocked_external() {
        for ok in ["https://tauri.localhost/", "https://tauri.localhost", "https://TAURI.localhost/x?y#z"] {
            assert!(navigation_allowed(ok), "{ok}");
        }
        for bad in [
            "http://tauri.localhost/",
            "https://example.com/",
            "https://tauri.localhost.evil.com/",
            "https://tauri.localhost@evil.com/",
            "https://evil.com/https://tauri.localhost/",
            "https://tauri.localhost:8443/",
            "file:///C:/Windows/win.ini",
            "data:text/html,hi",
            "javascript:alert(1)",
            "ipc://localhost/x",
            "about:blank",
            "",
        ] {
            assert!(!navigation_allowed(bad), "{bad}");
        }
    }

    #[test]
    fn lockdown_env_names() {
        assert!(is_webview2_var("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS"));
        assert!(is_webview2_var("webview2_browser_executable_folder"));
        assert!(!is_webview2_var("WEBVIEW_X"));
        assert!(!is_webview2_var("PATH"));
        assert!(!is_webview2_var("é"));
    }

    #[test]
    fn args_unknown_dropped() {
        let a = parse_args(["--quit", "--evil", "--background=1", "x"].map(String::from));
        assert_eq!(a, Args { quit: true, background: false });
        assert_eq!(parse_args(["--background".to_string()]), Args { quit: false, background: true });
        assert_eq!(parse_args(Vec::new()), Args::default());
    }
}
