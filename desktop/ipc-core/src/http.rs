//! The little HTTP/1.1 the shell speaks over the ui pipe (spec 01, "Pipe wire"): one request per connection, `Connection: close`, no
//! `Origin`, `Host: localhost`, the session token in `X-Audit-Token`. Pure, so it is tested on Linux. Answers are bounded by the caller.

pub const TOKEN_HEADER: &str = "X-Audit-Token";

#[derive(Debug, PartialEq, Eq)]
pub enum HttpError {
    BadInput(&'static str),
    Malformed(&'static str),
    TooLarge,
    Incomplete,
}

#[derive(Debug, PartialEq, Eq)]
pub struct Response {
    pub status: u16,
    pub body: Vec<u8>,
}

fn clean(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| (0x21..0x7f).contains(&b))
}

/// Build one request. Path and token must be printable ASCII with no space, so nothing can inject a header line.
pub fn build_request(method: &str, path: &str, token: Option<&str>, body: &[u8]) -> Result<Vec<u8>, HttpError> {
    if !matches!(method, "GET" | "POST") || !clean(path) || !path.starts_with('/') {
        return Err(HttpError::BadInput("method or path"));
    }
    if token.is_some_and(|t| !clean(t)) {
        return Err(HttpError::BadInput("token"));
    }
    let mut out = format!("{method} {path} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n").into_bytes();
    if let Some(t) = token {
        out.extend_from_slice(format!("{TOKEN_HEADER}: {t}\r\n").as_bytes());
    }
    if method == "POST" || !body.is_empty() {
        out.extend_from_slice(format!("Content-Type: application/json\r\nContent-Length: {}\r\n", body.len()).as_bytes());
    }
    out.extend_from_slice(b"\r\n");
    out.extend_from_slice(body);
    Ok(out)
}

fn find(hay: &[u8], needle: &[u8]) -> Option<usize> {
    hay.windows(needle.len()).position(|w| w == needle)
}

fn dechunk(mut raw: &[u8], max: usize) -> Result<Vec<u8>, HttpError> {
    let mut out = Vec::new();
    loop {
        let eol = find(raw, b"\r\n").ok_or(HttpError::Incomplete)?;
        let size_txt = std::str::from_utf8(&raw[..eol]).map_err(|_| HttpError::Malformed("chunk size"))?;
        let size_txt = size_txt.split(';').next().unwrap_or("").trim();
        if size_txt.is_empty() || !size_txt.bytes().all(|b| b.is_ascii_hexdigit()) {
            return Err(HttpError::Malformed("chunk size"));
        }
        let size = usize::from_str_radix(size_txt, 16).map_err(|_| HttpError::TooLarge)?;
        raw = &raw[eol + 2..];
        if size == 0 {
            return Ok(out);
        }
        if size > max.saturating_sub(out.len()) {
            return Err(HttpError::TooLarge);
        }
        if raw.len() < size.saturating_add(2) {
            return Err(HttpError::Incomplete);
        }
        out.extend_from_slice(&raw[..size]);
        if &raw[size..size + 2] != b"\r\n" {
            return Err(HttpError::Malformed("chunk end"));
        }
        raw = &raw[size + 2..];
    }
}

/// Parse a whole answer read to end of stream. `max_body` bounds the body.
pub fn parse_response(raw: &[u8], max_body: usize) -> Result<Response, HttpError> {
    let end = find(raw, b"\r\n\r\n").ok_or(HttpError::Incomplete)?;
    let head = std::str::from_utf8(&raw[..end]).map_err(|_| HttpError::Malformed("header bytes"))?;
    let mut lines = head.split("\r\n");
    let status_line = lines.next().ok_or(HttpError::Malformed("status line"))?;
    let mut parts = status_line.splitn(3, ' ');
    if !parts.next().is_some_and(|v| v == "HTTP/1.1" || v == "HTTP/1.0") {
        return Err(HttpError::Malformed("version"));
    }
    let status: u16 = parts.next().and_then(|s| s.parse().ok()).ok_or(HttpError::Malformed("status code"))?;
    let (mut length, mut chunked) = (None, false);
    for l in lines {
        let Some((k, v)) = l.split_once(':') else { return Err(HttpError::Malformed("header line")) };
        match k.trim().to_ascii_lowercase().as_str() {
            "content-length" => {
                if length.is_some() {
                    return Err(HttpError::Malformed("duplicate content-length"));
                }
                length = Some(v.trim().parse::<usize>().map_err(|_| HttpError::Malformed("content-length"))?);
            }
            "transfer-encoding" => {
                if !v.trim().eq_ignore_ascii_case("chunked") || chunked {
                    return Err(HttpError::Malformed("transfer-encoding"));
                }
                chunked = true;
            }
            _ => {}
        }
    }
    if chunked && length.is_some() {
        return Err(HttpError::Malformed("both content-length and chunked"));
    }
    let body = &raw[end + 4..];
    let body = if chunked {
        dechunk(body, max_body)?
    } else if let Some(n) = length {
        if n > max_body {
            return Err(HttpError::TooLarge);
        }
        if body.len() < n {
            return Err(HttpError::Incomplete);
        }
        body[..n].to_vec()
    } else {
        if body.len() > max_body {
            return Err(HttpError::TooLarge);
        }
        body.to_vec()
    };
    Ok(Response { status, body })
}

/// The token out of the `{"token": "..."}` answer of the hello route. A token has no spaces or control characters (43 URL-safe chars today);
/// anything else is refused so it can never be written into a header line.
pub fn parse_hello(body: &[u8]) -> Option<String> {
    #[derive(serde::Deserialize)]
    struct Hello {
        token: String,
    }
    let h: Hello = serde_json::from_slice(body).ok()?;
    (h.token.len() >= 16 && h.token.len() <= 128 && clean(&h.token)).then_some(h.token)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn request_has_host_close_and_token() {
        let r = String::from_utf8(build_request("GET", "/api/version", Some("abc"), b"").unwrap()).unwrap();
        assert_eq!(r, "GET /api/version HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nX-Audit-Token: abc\r\n\r\n");
        assert!(!r.to_lowercase().contains("origin"));
    }

    #[test]
    fn post_carries_length() {
        let r = String::from_utf8(build_request("POST", "/x", None, b"{}").unwrap()).unwrap();
        assert!(r.contains("Content-Length: 2\r\n") && r.ends_with("\r\n\r\n{}"));
    }

    #[test]
    fn injection_is_refused() {
        assert!(build_request("GET", "/a b", None, b"").is_err());
        assert!(build_request("GET", "/a\r\nX: y", None, b"").is_err());
        assert!(build_request("GET", "/a", Some("t\r\nX: y"), b"").is_err());
        assert!(build_request("GET", "/a", Some(""), b"").is_err());
        assert!(build_request("GET", "a", None, b"").is_err());
        assert!(build_request("PUT", "/a", None, b"").is_err());
    }

    #[test]
    fn content_length_answer() {
        let r = parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nokEXTRA", 100).unwrap();
        assert_eq!((r.status, r.body.as_slice()), (200, &b"ok"[..]));
    }

    #[test]
    fn chunked_answer() {
        let raw = b"HTTP/1.1 202 Accepted\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n4;x=1\r\n abc\r\n0\r\n\r\n";
        let r = parse_response(raw, 100).unwrap();
        assert_eq!((r.status, r.body.as_slice()), (202, &b"hello abc"[..]));
    }

    #[test]
    fn answer_to_end_of_stream() {
        assert_eq!(parse_response(b"HTTP/1.1 401 Unauthorized\r\n\r\nno", 10).unwrap().body, b"no");
    }

    #[test]
    fn bad_answers() {
        assert_eq!(parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nab", 10), Err(HttpError::Incomplete));
        assert_eq!(parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 50\r\n\r\n", 10), Err(HttpError::TooLarge));
        assert_eq!(
            parse_response(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n", 10),
            Err(HttpError::Malformed("chunk size"))
        );
        assert_eq!(parse_response(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nff\r\n", 10), Err(HttpError::TooLarge));
        assert!(parse_response(b"SPDY 200\r\n\r\n", 10).is_err());
        assert!(parse_response(b"HTTP/1.1 abc\r\n\r\n", 10).is_err());
        assert_eq!(parse_response(b"HTTP/1.1 200 OK\r\n", 10), Err(HttpError::Incomplete));
        assert!(parse_response(b"HTTP/1.1 200 OK\r\nbadheader\r\n\r\n", 10).is_err());
    }

    #[test]
    fn hello_token() {
        let t = "a".repeat(43);
        assert_eq!(parse_hello(format!(r#"{{"token":"{t}"}}"#).as_bytes()), Some(t));
        assert_eq!(parse_hello(br#"{"token":""}"#), None);
        assert_eq!(parse_hello(br#"{"token":"short"}"#), None);
        assert_eq!(parse_hello(br#"{"token":"aaaaaaaaaaaaaaaa\r\nX: y"}"#), None);
        assert_eq!(parse_hello(b"nope"), None);
    }

    #[test]
    fn chunk_size_overflow_is_an_error_not_a_panic() {
        let raw = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\nfffffffffffffffe\r\nx";
        assert_eq!(parse_response(raw, 100), Err(HttpError::TooLarge));
        let raw = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nffffffffffffffffffff\r\nx";
        assert_eq!(parse_response(raw, 100), Err(HttpError::TooLarge));
        let raw = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n+3\r\nabc\r\n0\r\n\r\n";
        assert_eq!(parse_response(raw, 100), Err(HttpError::Malformed("chunk size")));
    }

    #[test]
    fn conflicting_framing_is_refused() {
        assert!(parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nab", 10).is_err());
        assert!(parse_response(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip, chunked\r\n\r\n0\r\n\r\n", 10).is_err());
        assert!(parse_response(b"HTTP/1.1 200 OK\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n", 10).is_err());
    }
}
