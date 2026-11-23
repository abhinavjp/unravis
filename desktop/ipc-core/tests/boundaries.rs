//! Edge cases of the parsers and the supervisor, written to kill the mutants that `cargo mutants` (BL-452) found alive: each test sits
//! on the exact edge of a comparison (a limit, a boundary date, a tie) so that changing `>` to `>=` or `||` to `&&` fails it.

use ipc_core::floor::locked_version;
use ipc_core::http::{HttpError, build_request, parse_response};
use ipc_core::locator::{Error, MAX_BYTES, parse, start_unix_nanos};

const NS: i128 = 1_000_000_000;

// ---- floor ----------------------------------------------------------------------------------------------------------------------

#[test]
fn locked_version_keeps_the_first_of_equal_versions() {
    // Two entries with the same number written differently: the first stays (a tie is not "lower").
    let lock = "[[package]]\nname = \"x\"\nversion = \"1.2.3\"\n\n[[package]]\nname = \"x\"\nversion = \"1.02.3\"\n";
    assert_eq!(locked_version(lock, "x").as_deref(), Some("1.2.3"));
}

// ---- http -----------------------------------------------------------------------------------------------------------------------

fn text(v: Vec<u8>) -> String {
    String::from_utf8(v).unwrap()
}

#[test]
fn content_headers_follow_method_or_body() {
    let post_empty = text(build_request("POST", "/x", None, b"").unwrap());
    assert!(post_empty.contains("Content-Length: 0\r\n"), "an empty POST states its length");
    let get_body = text(build_request("GET", "/x", None, b"{}").unwrap());
    assert!(get_body.contains("Content-Length: 2\r\n"), "a GET with a body states its length");
    let get_plain = text(build_request("GET", "/x", None, b"").unwrap());
    assert!(!get_plain.contains("Content-"), "a plain GET has no content headers");
}

fn with_length(n: usize, body: usize) -> Vec<u8> {
    let mut v = format!("HTTP/1.1 200 OK\r\nContent-Length: {n}\r\n\r\n").into_bytes();
    v.extend(std::iter::repeat_n(b'a', body));
    v
}

#[test]
fn content_length_edges() {
    assert_eq!(parse_response(&with_length(10, 10), 10).unwrap().body.len(), 10, "length equal to the limit is allowed");
    assert_eq!(parse_response(&with_length(11, 11), 10), Err(HttpError::TooLarge));
    assert_eq!(parse_response(&with_length(10, 9), 100), Err(HttpError::Incomplete), "one byte short");
    assert_eq!(parse_response(&with_length(10, 10), 100).unwrap().body.len(), 10, "exactly the stated length");
}

#[test]
fn body_without_length_edges() {
    let raw = |n: usize| {
        let mut v = b"HTTP/1.1 200 OK\r\n\r\n".to_vec();
        v.extend(std::iter::repeat_n(b'a', n));
        v
    };
    assert_eq!(parse_response(&raw(10), 10).unwrap().body.len(), 10, "a body equal to the limit is allowed");
    assert_eq!(parse_response(&raw(11), 10), Err(HttpError::TooLarge));
}

fn chunked(rest: &str) -> Vec<u8> {
    format!("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n{rest}").into_bytes()
}

#[test]
fn chunk_edges() {
    assert_eq!(parse_response(&chunked("5\r\nhello\r\n0\r\n\r\n"), 5).unwrap().body, b"hello", "chunks equal to the limit are allowed");
    assert_eq!(parse_response(&chunked("6\r\nhello!\r\n0\r\n\r\n"), 5), Err(HttpError::TooLarge));
    assert_eq!(parse_response(&chunked("3\r\nabc\r\n3\r\ndef\r\n0\r\n\r\n"), 5), Err(HttpError::TooLarge), "the second chunk goes over");
    assert_eq!(parse_response(&chunked("5\r\nhel"), 100), Err(HttpError::Incomplete), "a cut chunk is incomplete, not a panic");
    assert_eq!(parse_response(&chunked("5\r\nhello"), 100), Err(HttpError::Incomplete), "the chunk end is missing");
    assert_eq!(parse_response(&chunked("5\r\nhelloXX"), 100), Err(HttpError::Malformed("chunk end")), "the chunk end is not CRLF");
}

// ---- locator --------------------------------------------------------------------------------------------------------------------

fn record(start: &str, pad: usize) -> String {
    let n = "0123456789abcdef0123456789abcdef";
    format!(
        r#"{{"version":2,"pid":42,"startTime":"{start}","exe":"C:\\u.exe","appVersion":"1","ui":"\\\\.\\pipe\\unravis.ui.{n}","ag":"\\\\.\\pipe\\unravis.ag.{n}","pad":"{}"}}"#,
        "x".repeat(pad)
    )
}

#[test]
fn locator_size_limit_is_inclusive() {
    let base = record("2026-11-22T00:47:40Z", 0).len();
    let at = record("2026-11-22T00:47:40Z", MAX_BYTES - base);
    assert_eq!(at.len(), MAX_BYTES);
    assert!(parse(at.as_bytes()).is_ok(), "a record of exactly MAX_BYTES parses");
    let over = record("2026-11-22T00:47:40Z", MAX_BYTES - base + 1);
    assert_eq!(parse(over.as_bytes()), Err(Error::TooBig));
}

#[test]
fn start_time_known_values() {
    let at = |s: &str, secs: i128| assert_eq!(start_unix_nanos(s), Some(secs * NS), "{s}");
    at("1970-01-01T00:00:00Z", 0);
    at("1969-12-31T23:59:59Z", -1);
    at("1999-12-31T23:59:59Z", 946_684_799);
    at("2000-02-29T12:34:56Z", 951_827_696);
    at("2000-03-01T00:00:00Z", 951_868_800);
    at("2024-01-31T01:02:03Z", 1_706_662_923);
    at("2026-11-22T00:47:40Z", 1_795_308_460);
    at("2100-03-01T00:00:00Z", 4_107_542_400);
    at("1900-02-28T00:00:00Z", -2_203_977_600);
    assert_eq!(start_unix_nanos("2026-11-22T00:47:40.6800051Z"), Some(1_795_308_460 * NS + 680_005_100));
    assert_eq!(start_unix_nanos("2026-11-22T00:47:40.5Z"), Some(1_795_308_460 * NS + 500_000_000));
    assert_eq!(start_unix_nanos("2026-11-22T00:47:40.123456789Z"), Some(1_795_308_460 * NS + 123_456_789));
}

#[test]
fn start_time_field_edges() {
    let ok = |s: &str| assert!(start_unix_nanos(s).is_some(), "{s} should parse");
    let bad = |s: &str| assert!(start_unix_nanos(s).is_none(), "{s} should be refused");
    ok("2026-11-22T23:59:59Z");
    bad("2026-11-22T24:00:00Z");
    ok("2026-11-22T00:59:00Z");
    bad("2026-11-22T00:60:00Z");
    ok("2026-11-22T00:00:59Z");
    bad("2026-11-22T00:00:60Z"); // Go never writes a leap second
    bad("2026-11-22T00:00:61Z");
    ok("2026-11-22T00:00:00.123456789Z");
    bad("2026-11-22T00:00:00.1234567890Z");
    bad("2026-11-22T00:00:00.12x4Z");
    bad("2026-11-22T00:00:00:01Z");
    bad("2026-11-22T00:00Z");
    ok("2026-12-31T00:00:00Z");
    bad("2026-13-01T00:00:00Z");
    ok("2026-01-31T00:00:00Z");
    bad("2026-01-32T00:00:00Z");
    bad("2026-00-10T00:00:00Z");
    bad("2026-01-00T00:00:00Z");
    bad("2026-11-22-01T00:00:00Z");
    bad("2026-11-22T00:00:00+01:00");
    bad("2026-11-22 00:00:00Z");
    // Only digits: no sign, no plus, no spaces; and only days that exist.
    bad("2026-11-22T-1:00:00Z");
    bad("2026-11-22T+1:00:00Z");
    bad("2026-11-22T 1:00:00Z");
    bad("+026-11-22T00:00:00Z");
    bad("12026-11-22T00:00:00Z");
    bad("2026-02-31T00:00:00Z");
    bad("2026-02-29T00:00:00Z");
    ok("2024-02-29T00:00:00Z");
    bad("2100-02-29T00:00:00Z");
    ok("2000-02-29T00:00:00Z");
    ok("2026-04-30T00:00:00Z");
    bad("2026-04-31T00:00:00Z");
    ok("2026-11-30T00:00:00Z");
    bad("2026-11-31T00:00:00Z");
    // Exact widths, as Go writes them; a dot needs digits after it.
    bad("2026-1-02T00:00:00Z");
    bad("2026-01-2T00:00:00Z");
    bad("2026-01-02T1:00:00Z");
    bad("2026-01-02T01:2:00Z");
    bad("2026-01-02T01:02:3Z");
    bad("26-01-02T00:00:00Z");
    bad("2026-11-22T00:00:00.Z");
    ok("0999-01-01T00:00:00Z");
    ok("9999-12-31T23:59:59.999999999Z");
    ok("2026-06-30T00:00:00Z");
    bad("2026-06-31T00:00:00Z");
    ok("2026-09-30T00:00:00Z");
    bad("2026-09-31T00:00:00Z");
}
