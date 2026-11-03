# WP-02a IIS privacy helpers: IP check, referer cleaning, cookie split and digest
risk: high    size: M    wave: 2    model: sonnet

## Outcome
`internal/importer/iis/common.go` gains the pure helpers the W3C parser will use to keep a client IP, a cleaned referer and keyed cookie digests: `validIPv4or6`, `safeReferer`, `splitCookiePairs` and `HashCookie`, with unit tests. Nothing calls them yet (WP-02b wires them), so import behaviour does not change (TR-19, TR-20).

## Write scope
- `internal/importer/iis/common.go`
- `internal/importer/iis/privacy_helpers_test.go` (new)

## Must not change
- `w3c.go`, `httperr.go`, `testdata/*`, `iis_test.go`, `internal/evidence/*`: all WP-02b or later. No keep list gains a column in this packet, so no import output changes.
- The per-case policy plumbing (`importer.CookiePolicy`, `Options.CookiePolicy`) is WP-01a; only read the type here.
- `w3cTimeIsEnd`, the gap constants and `TestW3CTimeIsEndGap` (WP-45).
- The quarantine behaviour of `declare` for every column except the three named in step 7 (`TestW3CDuplicateKeptColumn` stays green).
- INV-3: no helper stores or logs a raw cookie value, server IP or query string.

## Changes
1. `common.go`: replace the package comment sentence "Drop at parse: client and server IPs, query strings, user agents, cookies and referers are never read into any value." with text saying: the W3C parser keeps `c-ip` as `client-ip`, `cs(referer)` as `referer` without query or fragment, and allowlisted cookies as HMAC digests; server IPs, query strings, user agents and all other cookies are dropped at parse. Keep the sentence "Every field not named in a spec's keep list is skipped, so an unknown or sensitive column cannot leak." The phrase `never read into any value` must not remain anywhere in the file. Add the imports `crypto/hmac`, `crypto/sha256`, `encoding/hex`, `net`, `net/url` (`strings` and `unicode/utf8` are already imported).
2. `common.go`, constants (new const block): `maxIPBytes = 64`, `maxRefererBytes = 512` (input), `maxCookiePairs = 20`, `maxCookiePairBytes = 512`, `maxCookieAttrs = 4`, `cookieDigestPrefix = "unravis-cookie-v1\x00"`. The stored referer is at most 256 bytes (`evidence.MaxEventAttrValue`).
3. `func validIPv4or6(s string) bool`: false when `len(s) == 0 || len(s) > maxIPBytes`, or `s` contains a space or "/"; otherwise `net.ParseIP(s) != nil`. The caller stores `s` unchanged.
4. `func safeReferer(v string) (string, bool)` returns the cleaned referer and true, or `"", false` (the caller drops the attr and keeps the row). Rules in order: `len(v) <= maxRefererBytes` and `utf8.ValidString(v)`; `url.Parse(v)` succeeds; scheme is `http` or `https`; `u.Host` non-empty; build `scheme + "://" + u.Host + u.EscapedPath()` (userinfo, query and fragment are never copied); the result must be valid UTF-8 and at most 256 bytes, otherwise drop the whole attr (never truncate). Note: the spec shows a bool-only helper; the cleaned value is needed, so this returns both.
5. `type cookiePair struct{ Name, Value string }` and `func splitCookiePairs(v string) []cookiePair`. W3C encodes spaces as `+`, so the logged value of `sid=a; theme=b` is `sid=a;+theme=b`. Split on ";", apply `strings.TrimLeft(pair, " +")`, skip empty pairs, skip pairs longer than `maxCookiePairBytes`, split at the first "=" (a pair without "=" or with an empty name is skipped), lower-case the name, keep the value exactly as logged (no decoding), stop after `maxCookiePairs` pairs. Note: the spec example uses a space; real logs use "+".
6. `func HashCookie(p *importer.CookiePolicy, name, value string) string`. Returns "" when `p == nil`, `len(p.HMACKey) != 32`, or `strings.ToLower(name)` is not in `p.Allowed`. Otherwise it returns the first 32 characters of the lower-case hex of `HMAC-SHA256(p.HMACKey, cookieDigestPrefix + strings.ToLower(name) + "\x00" + value)` (`crypto/hmac`, `crypto/sha256`, `encoding/hex`). A 32-hex digest is a pseudonym, not an anonymous value; the export scrub handles it (WP-07a/07b).
7. `common.go`, `declare`: rule for the three new privacy columns. A `#Fields` line that lists `c-ip`, `cs(referer)` or `cs(cookie)` more than once must still import as it did before these columns were kept: the first occurrence is used and the later ones are ignored, not quarantined. Implement with a package variable `dupIgnoredColumns = map[string]bool{"c-ip": true, "cs(referer)": true, "cs(cookie)": true}` and, in the `if _, dup := col[n]; dup {` branch, `if dupIgnoredColumns[n] { continue }` before the `quarantine(...)` return. Every other duplicated kept column still quarantines with `ReasonFieldCount`.
8. Tests in the new file `privacy_helpers_test.go` (package `iis`; the helpers `wantQuarantine` and `baseOpts` already exist in `iis_test.go`). Helper `testPolicy(allowed ...string) *importer.CookiePolicy` returning `Allowed: allowed` and `HMACKey` of 32 bytes with values `0..31`; WP-02b reuses it.
   - `TestHashCookieKnownAnswer`: for key bytes all `0x01` the result for name `SID` (allowed `sid`) and value `v` equals `hex(HMAC-SHA256(key, "unravis-cookie-v1\x00sid\x00v"))[:32]` computed in the test with `crypto/hmac`; "" for a name not allowed, a nil policy, a 31-byte key and a 33-byte key; a different key gives a different digest.
   - `TestW3CClientIPValidation` (calls `validIPv4or6`): `192.0.2.1`, `2001:db8::1`, `::1` are valid; `""`, `1.2.3`, `999.1.1.1`, `1.2.3.4/32`, `"1.2.3.4 "` and a 65-character string are not.
   - `TestSafeReferer`: `http://a.test/x?q=1#f` -> `http://a.test/x`; `https://a.test:8443/p/q` unchanged; `https://user:pw@a.test/p` -> `https://a.test/p`; `http://a.test` unchanged; `ftp://a.test/x`, `a.test/x`, `http:///x`, `javascript:alert(1)` and `http://a.test/\xff` are dropped; an input over 512 bytes is dropped; a path of 300 `a` characters is dropped (cleaned result over 256 bytes).
   - `TestSplitCookiePairs`: `sid=a;+theme=b` -> two pairs; `;;`, `=x`, `novalue` yield none; names are lower-cased; values keep a second `=`; 25 pairs give 20; a pair over 512 bytes is skipped; an empty string gives none.
   - `TestDeclareDuplicatePrivacyColumn`: call `declare("#Fields: date c-ip x c-ip cs(referer) cs(referer)", &spec{keep: []string{"date","c-ip","cs(referer)"}}, importer.Limits{MaxFields: 4096}, &runState{declared: map[string]bool{}})`; no error, `col["c-ip"] == 1`, `col["cs(referer)"] == 4`, field count 6. A second call with `"#Fields: date time time"` and `keep: {"date","time"}` returns a quarantine (`wantQuarantine(t, err, importer.ReasonFieldCount, "time")`).

## Proof
- `go test ./internal/importer/iis/ -run "TestHashCookieKnownAnswer|TestW3CClientIPValidation|TestSafeReferer|TestSplitCookiePairs|TestDeclareDuplicatePrivacyColumn" -count=1` -> `ok`.
- `go test ./internal/importer/... ./internal/evidence/... -count=1` -> `ok` (existing tests, including `TestW3CDuplicateKeptColumn`, unchanged).
- `go vet ./internal/importer/iis/` -> no output.
- `grep -n "never read into any value" internal/importer/iis/common.go` -> no output.

## Depends on
WP-01a
