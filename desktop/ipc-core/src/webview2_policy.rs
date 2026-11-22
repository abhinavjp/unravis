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
    "--disable-features",
    "--allow-running-insecure-content",
    "--ignore-certificate-errors",
    "--user-data-dir",
    "--load-extension",
    "--enable-automation",
];

/// The dangerous flags in one `AdditionalBrowserArguments` string. Flags are split on whitespace; `--name=value` is judged by its name;
/// case is ignored. `--disable-features` is listed because it can switch off site isolation and other protections.
pub fn dangerous_flags(args: &str) -> Vec<String> {
    args.split_whitespace()
        .filter_map(|tok| {
            let name = tok.split('=').next().unwrap_or(tok).to_ascii_lowercase();
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
