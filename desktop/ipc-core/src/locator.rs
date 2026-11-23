//! Reader of `backend.json`, the rendezvous file the Go owner writes (internal/locator, schema 2). The shell only reads it. It never
//! trusts a field it did not check, and a record that fails any check is "no owner".

use serde::Deserialize;

pub const SCHEMA: u32 = 2;
pub const MAX_BYTES: usize = 64 << 10;

#[derive(Debug, PartialEq, Eq)]
pub enum Error {
    TooBig,
    Json,
    UnknownSchema(u32),
    Invalid(&'static str),
}

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Record {
    pub version: u32,
    pub pid: u32,
    pub start_time: String,
    pub exe: String,
    pub app_version: String,
    pub ui: String,
    pub ag: String,
}

/// `\\.\pipe\unravis.<role>.<32 lower hex>` -> nonce, when the role matches.
fn nonce<'a>(name: &'a str, role: &str) -> Option<&'a str> {
    let rest = name.strip_prefix(r"\\.\pipe\unravis.")?;
    let rest = rest.strip_prefix(role)?.strip_prefix('.')?;
    let ok = rest.len() == 32 && rest.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b));
    ok.then_some(rest)
}

pub fn parse(bytes: &[u8]) -> Result<Record, Error> {
    if bytes.len() > MAX_BYTES {
        return Err(Error::TooBig);
    }
    let r: Record = serde_json::from_slice(bytes).map_err(|_| Error::Json)?;
    if r.version != SCHEMA {
        return Err(Error::UnknownSchema(r.version));
    }
    if r.pid == 0 {
        return Err(Error::Invalid("pid"));
    }
    if start_unix_nanos(&r.start_time).is_none() {
        return Err(Error::Invalid("startTime"));
    }
    let (Some(u), Some(a)) = (nonce(&r.ui, "ui"), nonce(&r.ag, "ag")) else {
        return Err(Error::Invalid("pipe name"));
    };
    if u != a {
        return Err(Error::Invalid("pipe nonces differ"));
    }
    Ok(r)
}

/// A field of a timestamp: exactly `width` ASCII digits (no sign, no space), as Go writes it.
fn digits(s: &str, width: usize) -> Option<i64> {
    if s.len() != width || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    s.parse().ok()
}

/// Process start time in Unix nanoseconds from the `Z` form Go writes (`2026-11-22T00:47:40.6800051Z`). Only UTC is accepted.
pub fn start_unix_nanos(s: &str) -> Option<i128> {
    let s = s.strip_suffix('Z')?;
    let (date, time) = s.split_once('T')?;
    let mut d = date.split('-');
    let (y, m, day) = (digits(d.next()?, 4)?, digits(d.next()?, 2)?, digits(d.next()?, 2)?);
    let leap = y % 4 == 0 && (y % 100 != 0 || y % 400 == 0);
    let month_days = match m {
        2 if leap => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    };
    if d.next().is_some() || !(1..=12).contains(&m) || !(1..=month_days).contains(&day) {
        return None;
    }
    let (hms, frac) = time.split_once('.').unwrap_or((time, ""));
    if time.contains('.') && frac.is_empty() {
        return None; // a dot with no digits after it
    }
    let mut t = hms.split(':');
    let (h, mi, sec) = (digits(t.next()?, 2)?, digits(t.next()?, 2)?, digits(t.next()?, 2)?);
    if t.next().is_some() || h > 23 || mi > 59 || sec > 59 || frac.len() > 9 || !frac.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut nanos: i64 = if frac.is_empty() { 0 } else { frac.parse().ok()? };
    for _ in frac.len()..9 {
        nanos *= 10;
    }
    // days from civil (Howard Hinnant)
    let y2 = if m <= 2 { y - 1 } else { y };
    let era = y2.div_euclid(400);
    let yoe = y2 - era * 400;
    let doy = (153 * (if m > 2 { m - 3 } else { m + 9 }) + 2) / 5 + day - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146_097 + doe - 719_468;
    Some(i128::from(days * 86_400 + h * 3600 + mi * 60 + sec) * 1_000_000_000 + i128::from(nanos))
}

/// Windows creation time (FILETIME, 100 ns since 1601) to Unix nanoseconds.
pub fn filetime_to_unix_nanos(ft: u64) -> i128 {
    (i128::from(ft) - 116_444_736_000_000_000) * 100
}

/// The locator and the process agree on the start time when they are within one second (Go rounds, Windows counts 100 ns).
pub fn same_start(record_nanos: i128, process_nanos: i128) -> bool {
    (record_nanos - process_nanos).abs() <= 1_000_000_000
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rec(extra: &str) -> String {
        format!(
            r#"{{"version":2,"pid":42,"startTime":"2026-11-22T00:47:40.6800051Z","exe":"C:\\Unravis\\unravis.exe","appVersion":"0.1.0-rc.57","ui":"\\\\.\\pipe\\unravis.ui.{n}","ag":"\\\\.\\pipe\\unravis.ag.{n}"{extra}}}"#,
            n = "0123456789abcdef0123456789abcdef"
        )
    }

    #[test]
    fn locator_parses_v2() {
        let r = parse(rec("").as_bytes()).unwrap();
        assert_eq!(r.pid, 42);
        assert_eq!(r.app_version, "0.1.0-rc.57");
        assert!(r.ui.starts_with(r"\\.\pipe\unravis.ui."));
    }

    #[test]
    fn unknown_fields_are_ignored() {
        assert!(parse(rec(r#","future":1"#).as_bytes()).is_ok());
    }

    #[test]
    fn locator_rejects_bad_version() {
        let s = rec("").replace("\"version\":2", "\"version\":3");
        assert_eq!(parse(s.as_bytes()), Err(Error::UnknownSchema(3)));
    }

    #[test]
    fn rejects_bad_fields() {
        let swap = |from: &str, to: &str| rec("").replace(from, to);
        for (s, why) in [
            (swap("\"pid\":42", "\"pid\":0"), "pid"),
            (swap("2026-11-22T00:47:40.6800051Z", "yesterday"), "startTime"),
            (swap(r"unravis.ui.", r"unravis.xx."), "pipe name"),
            (swap(r"\\\\.\\pipe\\unravis.ag.0123", r"\\\\.\\pipe\\unravis.ag.4567"), "pipe nonces differ"),
            (swap("0123456789abcdef0123456789abcdef\"", "0123\""), "pipe name"),
        ] {
            assert_eq!(parse(s.as_bytes()), Err(Error::Invalid(why)), "{why}");
        }
        assert_eq!(parse(b"{"), Err(Error::Json));
        assert_eq!(parse(&vec![b' '; MAX_BYTES + 1]), Err(Error::TooBig));
    }

    #[test]
    fn rfc3339_known_values() {
        assert_eq!(start_unix_nanos("1970-01-01T00:00:00Z"), Some(0));
        assert_eq!(start_unix_nanos("2000-03-01T00:00:00Z"), Some(951_868_800 * 1_000_000_000));
        assert_eq!(start_unix_nanos("1970-01-01T00:00:01.5Z"), Some(1_500_000_000));
        assert_eq!(start_unix_nanos("1970-01-01T00:00:00+02:00"), None);
        assert_eq!(start_unix_nanos("1970-13-01T00:00:00Z"), None);
    }

    #[test]
    fn filetime_epoch() {
        assert_eq!(filetime_to_unix_nanos(116_444_736_000_000_000), 0);
        assert_eq!(filetime_to_unix_nanos(116_444_736_010_000_000), 1_000_000_000);
    }

    #[test]
    fn start_times_within_a_second_agree() {
        assert!(same_start(1_000_000_000, 1_900_000_000));
        assert!(!same_start(1_000_000_000, 2_100_000_000));
    }
}
