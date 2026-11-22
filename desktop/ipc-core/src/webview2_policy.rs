//! Machine or user policy can hand extra command-line flags to every WebView2 host (`AdditionalBrowserArguments`, per exe name or `*`).
//! A debug port or a disabled sandbox there turns the window into a remote-controllable browser, so the shell refuses to start when it
//! finds one (exit code 78). The registry reads are done by the Windows code; this file only judges the text.

/// Exit code of the shell when WebView2 policy carries a dangerous flag (PLAN section 1).
pub const EXIT_WEBVIEW2_POLICY: i32 = 78;

/// Flag names (without value) that open the browser to a debugger or switch off its protections.
const DANGEROUS: &[&str] = &[
    "--remote-debugging-port",
    "--remote-debugging-pipe",
    "--remote-debugging-address",
    "--remote-allow-origins",
    "--auto-open-devtools-for-tabs",
    "--disable-web-security",
    "--no-sandbox",
    "--disable-gpu-sandbox",
    "--disable-site-isolation-trials",
    "--inspect",
    "--gpu-launcher",
    "--renderer-cmd-prefix",
    "--utility-cmd-prefix",
    "--browser-subprocess-path",
    "--single-process",
    "--js-flags",
    "--allow-file-access-from-files",
    "--unsafely-treat-insecure-origin-as-secure",
    "--allow-running-insecure-content",
    "--ignore-certificate-errors",
    "--user-data-dir",
    "--load-extension",
    "--enable-automation",
];

/// Feature names whose removal weakens isolation. `--disable-features=RendererCodeIntegrity` (a common antivirus workaround that
/// administrators push to every WebView2 host) is allowed; one of these names is not.
const PROTECTIONS: &[&str] = &["isolateorigins", "site-per-process", "siteperprocess", "originkeyedprocessesbydefault", "strictorigin"];

/// Chromium reads a switch with `--`, `-` or `/` in front, and strips quotes like `CommandLineToArgvW`: all spellings count.
fn switch_name(tok: &str) -> Option<String> {
    let t = tok.trim_matches('"');
    let t = t.strip_prefix("--").or_else(|| t.strip_prefix('-')).or_else(|| t.strip_prefix('/'))?;
    let name = t.split('=').next().unwrap_or(t).trim_matches('"');
    Some(format!("--{}", name.to_ascii_lowercase()))
}

/// The dangerous flags in one `AdditionalBrowserArguments` string. Flags are split on whitespace and judged by name, ignoring case,
/// quotes and the prefix style.
pub fn dangerous_flags(args: &str) -> Vec<String> {
    args.split_whitespace()
        .filter_map(|tok| {
            let name = switch_name(tok)?;
            if name == "--disable-features" {
                let value =
                    tok.trim_matches('"').split_once('=').map(|(_, v)| v.trim_matches('"').to_ascii_lowercase()).unwrap_or_default();
                return value.split(',').any(|f| PROTECTIONS.contains(&f.trim())).then_some(name);
            }
            DANGEROUS.contains(&name.as_str()).then_some(name)
        })
        .collect()
}

/// The registry places read for one start: (label, value). The first dangerous flag found names the place.
pub fn first_violation<'a>(found: &'a [(&'a str, String)]) -> Option<(&'a str, Vec<String>)> {
    found.iter().find_map(|(place, v)| {
        let bad = dangerous_flags(v);
        (!bad.is_empty()).then_some((*place, bad))
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn flags_are_found_whatever_the_spelling() {
        assert_eq!(dangerous_flags("--remote-debugging-port=9222"), vec!["--remote-debugging-port"]);
        assert_eq!(dangerous_flags("--foo  --No-Sandbox   --disable-web-security"), vec!["--no-sandbox", "--disable-web-security"]);
        assert_eq!(dangerous_flags("--auto-open-devtools-for-tabs"), vec!["--auto-open-devtools-for-tabs"]);
        assert_eq!(dangerous_flags("--disable-features=IsolateOrigins,site-per-process"), vec!["--disable-features"]);
    }

    #[test]
    fn other_prefixes_and_quotes_do_not_hide_a_flag() {
        for s in [
            "-remote-debugging-port=9222",
            "/remote-debugging-port=9222",
            "\"--remote-debugging-port=9222\"",
            "\"/no-sandbox\"",
            "--INSPECT=9229",
        ] {
            assert_eq!(dangerous_flags(s).len(), 1, "{s}");
        }
    }

    #[test]
    fn a_common_enterprise_workaround_is_allowed() {
        assert!(dangerous_flags("--disable-features=RendererCodeIntegrity").is_empty());
        assert!(dangerous_flags("--disable-features").is_empty());
    }

    #[test]
    fn harmless_flags_pass() {
        assert!(dangerous_flags("").is_empty());
        assert!(dangerous_flags("--lang=en-GB --force-dark-mode").is_empty());
        assert!(dangerous_flags("--remote-debugging-portal").is_empty(), "a different name is not the flag");
    }

    #[test]
    fn exe_specific_and_star_places_are_both_judged() {
        let ok = [("HKCU *", String::new()), ("HKCU unravis-desktop.exe", "--lang=en".to_string())];
        assert!(first_violation(&ok).is_none());
        let bad = [("HKCU *", "--lang=en".to_string()), ("HKLM unravis-desktop.exe", "--no-sandbox".to_string())];
        let (place, flags) = first_violation(&bad).unwrap();
        assert_eq!(place, "HKLM unravis-desktop.exe");
        assert_eq!(flags, vec!["--no-sandbox"]);
    }
}
