//! Version floor of Tauri (BL-320). The CI step feeds `Cargo.lock` text to `locked_version` and compares with `meets`.

/// Lowest allowed Tauri: GHSA-w28w-mhc8-qvjv (CVE-2026-42184) is fixed from 2.12.7.
pub const TAURI_FLOOR: (u64, u64, u64) = (2, 12, 7);

/// `x.y.z` with an optional `-pre` or `+build` tail. Anything else is `None`.
pub fn parse_version(s: &str) -> Option<(u64, u64, u64)> {
    let core = s.split(['-', '+']).next()?;
    let mut it = core.split('.');
    let v = (it.next()?.parse().ok()?, it.next()?.parse().ok()?, it.next()?.parse().ok()?);
    if it.next().is_some() {
        return None;
    }
    Some(v)
}

/// A pre-release of the floor version is below the floor.
pub fn meets(version: &str, floor: (u64, u64, u64)) -> bool {
    let Some(v) = parse_version(version) else { return false };
    if v != floor {
        return v > floor;
    }
    !version.contains('-')
}

/// The version of package `name` in `Cargo.lock` text. More than one entry (two versions linked) returns the lowest, so a stale copy
/// cannot hide behind a new one.
pub fn locked_version(lock: &str, name: &str) -> Option<String> {
    let mut best: Option<((u64, u64, u64), String)> = None;
    let mut cur: Option<&str> = None;
    for line in lock.lines() {
        let line = line.trim();
        if line == "[[package]]" {
            cur = None;
        } else if let Some(n) = line.strip_prefix("name = ") {
            cur = Some(n.trim_matches('"'));
        } else if let Some(v) = line.strip_prefix("version = ")
            && cur == Some(name)
        {
            let v = v.trim_matches('"');
            let key = parse_version(v)?;
            if best.as_ref().is_none_or(|(b, _)| key < *b) {
                best = Some((key, v.to_string()));
            }
        }
    }
    best.map(|(_, v)| v)
}

/// Names of locked packages that start with `prefix` (used to prove zero `tauri-plugin-*`).
pub fn locked_with_prefix(lock: &str, prefix: &str) -> Vec<String> {
    lock.lines()
        .filter_map(|l| l.trim().strip_prefix("name = "))
        .map(|n| n.trim_matches('"'))
        .filter(|n| n.starts_with(prefix))
        .map(str::to_string)
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    const LOCK: &str = "[[package]]\nname = \"serde\"\nversion = \"1.0.0\"\n\n[[package]]\nname = \"tauri\"\nversion = \"2.12.7\"\n\n[[package]]\nname = \"tauri-plugin-shell\"\nversion = \"2.0.0\"\n";

    #[test]
    fn floor_accepts_equal_and_newer() {
        assert!(meets("2.12.7", TAURI_FLOOR));
        assert!(meets("2.12.8", TAURI_FLOOR));
        assert!(meets("2.13.0", TAURI_FLOOR));
        assert!(meets("3.0.0", TAURI_FLOOR));
    }

    #[test]
    fn floor_rejects_older_and_prerelease_of_floor() {
        assert!(!meets("2.12.6", TAURI_FLOOR));
        assert!(!meets("2.11.99", TAURI_FLOOR));
        assert!(!meets("2.12.7-rc.1", TAURI_FLOOR));
        assert!(!meets("garbage", TAURI_FLOOR));
        assert!(!meets("2.12", TAURI_FLOOR));
    }

    #[test]
    fn doctored_lock_fails_the_floor() {
        let doctored = LOCK.replace("2.12.7", "2.12.6");
        assert_eq!(locked_version(LOCK, "tauri").as_deref(), Some("2.12.7"));
        assert!(!meets(&locked_version(&doctored, "tauri").unwrap(), TAURI_FLOOR));
    }

    #[test]
    fn two_versions_report_the_lowest() {
        let two = format!("{LOCK}\n[[package]]\nname = \"tauri\"\nversion = \"2.9.0\"\n");
        assert_eq!(locked_version(&two, "tauri").as_deref(), Some("2.9.0"));
    }

    #[test]
    fn plugin_prefix_is_found() {
        assert_eq!(locked_with_prefix(LOCK, "tauri-plugin-"), vec!["tauri-plugin-shell"]);
        assert!(locked_with_prefix("[[package]]\nname = \"tauri\"\n", "tauri-plugin-").is_empty());
    }

    #[test]
    fn missing_package_is_none() {
        assert_eq!(locked_version(LOCK, "nope"), None);
    }
}
