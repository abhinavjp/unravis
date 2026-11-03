# WP-02b W3C stores client IP, clean referer and cookie digests (wiring)
risk: high    size: M    wave: 3    model: sonnet

## Outcome
The IIS W3C parser stores `client-ip` (from `c-ip`), `referer` (from `cs(referer)`, scheme+host+path only) and, only for allowlisted names, `cookie:<name>` HMAC digests, using the WP-02a helpers. Server IP, query strings, user agents and non-allowlisted cookies stay dropped and no raw cookie value is ever stored (TR-19, TR-20, TR-30).

## Write scope
- `internal/importer/iis/w3c.go`
- `internal/importer/iis/testdata/w3c_good.log` (edit row 1 referer only)
- `internal/importer/iis/iis_test.go`
- `internal/evidence/caseexport_enums.go` (edit the `exportDropped` literal only)

## Must not change
- `internal/importer/iis/common.go` (WP-02a owns every helper; only call them here). The helpers used, with their exact signatures: `validIPv4or6(s string) bool`, `safeReferer(v string) (string, bool)`, `splitCookiePairs(v string) []cookiePair` (fields `Name`, `Value`), `HashCookie(p *importer.CookiePolicy, name, value string) string`, and the constant `maxCookieAttrs`. The test helper `testPolicy(allowed ...string) *importer.CookiePolicy` is in `privacy_helpers_test.go`.
- The `"cookie:*"` sentinel in `W3C.AllowedAttrs()` and the prefix match in `evidence.Event.Validate`: that is WP-03. Here cookie attrs are tested through `W3C.Parse` directly, not `importer.Collect` (Collect validates attrs and would refuse `cookie:` keys until WP-03).
- `w3cTimeIsEnd`, the two gap constants and `TestW3CTimeIsEndGap` (VF-01b is WP-45's).
- The per-case policy plumbing (`importer.CookiePolicy`, `Options.CookiePolicy`) is WP-01a; only read it here.
- `httperr.go`, `winevents`/WAS code, and the keep lists of other specs.
- `TestBuildCaseNoIPsOrUserIDs` in `internal/evidence` stays green (the export drops the new attrs through `exportDropped`).

## Changes
1. `w3c.go`: add `"c-ip"`, `"cs(referer)"`, `"cs(cookie)"` to `w3cSpec.keep` (`declare` lower-cases header names, so these lower-case spellings match `cs(Referer)` and `cs(Cookie)`). Add `"client-ip": true` and `"referer": true` to `W3C.AllowedAttrs()`. Do not add `"cookie:*"` (WP-03). Change the capacity in `w3cRow` from `make(map[string]string, 8)` to 16.
2. `w3c.go`, `w3cRow`: after the `cs-username` block and before `ev = evidence.Event{Kind: "request", ...}` add: `c-ip` -> if `c.get("c-ip")` is present and `validIPv4or6(v)` set `attrs["client-ip"] = v`; `cs(referer)` -> if present and `safeReferer(v)` is ok set `attrs["referer"]` to the cleaned value; if `c.opts.CookiePolicy != nil` and `cs(cookie)` is present, go through `splitCookiePairs` in order and for each pair whose `HashCookie(c.opts.CookiePolicy, pair.Name, pair.Value)` is non-empty and whose `"cookie:"+pair.Name` is not yet set, set `attrs["cookie:"+pair.Name]` to the digest, stopping at `maxCookieAttrs` cookie attrs (first occurrence of a name wins). A bad IP or referer never rejects the row. Attr count stays within `evidence.MaxEventAttrs` (16): 10 static + 2 + at most 4 cookies.
3. `testdata/w3c_good.log`: change only the referer of row 1 (`2001-02-03 04:05:06 ...`) from `http://ref.example.test/SENTINEL-7f3a` to `http://ref.example.test/landing?q=SENTINEL-7f3a#frag`, so the sentinel sits in the part that must be cut.
4. `internal/evidence/caseexport_enums.go`: in the `exportDropped` literal, change the `"iis-w3c"` entry from `{"stem": true}` to `{"stem": true, "client-ip": true, "referer": true}` (drift test `TestFix5ExportPolicyCoversEveryImporter` requires every importer-allowed attr to be exported or listed as dropped). Note: this file is not in the spec's file list. `"cookie:*"` joins in WP-03.
5. `iis_test.go`, minimal edits to existing tests: in `TestW3CGood` add `"client-ip": "192.0.2.77"` and `"referer": "http://ref.example.test/landing"` to `want` (event 0); in `TestW3CDropsSensitiveColumns` replace the banned string `"192.0.2."` with `"192.0.2.10"` (the server IP) and remove `"ref.example.test"` from the list. WP-05 replaces this test later.
6. `iis_test.go`, new helpers: `parseRaw(t testing.TB, data []byte, opts importer.Options) []evidence.Event` calls `NewW3C().Parse(context.Background(), bytes.NewReader(data), opts, emit)` with a collecting `emit` and fails the test on error. `cookieFile(rows ...string) []byte` builds a file with the software line as in `w3cFile` and header `#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken c-ip cs(Referer) cs(Cookie)`; rows are written verbatim, one per line. Use `testPolicy` from WP-02a.
7. New tests in `iis_test.go` (assertions in words):
   - `TestW3CStoresClientIPAndReferer`: a row with `c-ip 192.0.2.77` and referer `http://ref.example.test/landing?q=SENTINEL-7f3a#frag` gives attrs `client-ip=192.0.2.77` and `referer=http://ref.example.test/landing`; a row with `-` for both has neither key; a row with `c-ip not-an-ip` is still emitted without `client-ip`; the sentinel is in no marshaled event.
   - `TestW3CCookieHash`: policy allows `sid` and `theme`; cookie field `sid=abc123;+theme=dark;+other=zzz` gives `cookie:sid` and `cookie:theme`, each 32 lower-case hex characters, equal to `HashCookie(policy, name, value)`, and no `cookie:other`; the same value in two rows gives the same digest, a different key gives a different digest; `sid=first;+sid=second` keeps the digest of `first`; five allowlisted names in one row yield at most 4 cookie attrs.
   - `TestW3CCookieRawNeverStored`: with raw values `abc123` and `dark` in the cookie field, the JSON of every event contains neither; with a nil policy and with a policy whose `Allowed` is empty there is no `cookie:` key at all.
   - `TestW3CStillDropsUserAgentAndQuery`: the header also has `s-ip cs-uri-query cs(User-Agent)` with `SENTINEL-7f3a` in query and agent and `192.0.2.10` as server IP; attr keys are a subset of `AllowedAttrs()` (cookie keys excluded), and neither the sentinel nor `192.0.2.10` appears in the JSON of the events.
   - `TestW3CDuplicatePrivacyColumnImports`: a file whose `#Fields` line lists `c-ip` twice (and `cs(referer)` twice) imports without quarantine; `client-ip` comes from the first `c-ip` column and the second column's value is not stored.

## Proof
- `go test ./internal/importer/iis/ -run "TestW3C" -count=1` -> `ok` (includes the edited `TestW3CGood`, `TestW3CDropsSensitiveColumns` and the five new tests).
- `go test ./internal/importer/... ./internal/evidence/... -count=1` -> `ok` (includes the conformance, fuzz-seed and drift tests).
- `go vet ./internal/importer/iis/ ./internal/evidence/` -> no output.
- `go test ./internal/evidence/ -run "TestFix5ExportPolicyCoversEveryImporter|TestBuildCaseNoIPsOrUserIDs" -count=1` -> `ok`.

## Depends on
WP-02a, WP-45
