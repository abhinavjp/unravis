# WP-03 Event validation accepts dynamic cookie attrs
risk: high    size: M    wave: 4    model: sonnet

## Outcome
An importer that lists the sentinel `"cookie:*"` in its allowed attrs may emit events with `cookie:<name>` attributes, and the store accepts them; any other unlisted key is still refused. W3C lists the sentinel, so an import with an allowlisted cookie digest succeeds end to end (TR-19, TR-20).

## Write scope
- `internal/evidence/event.go`
- `internal/evidence/event_test.go`
- `internal/evidence/caseexport_enums.go` (edit the `exportDropped` literal only)
- `internal/importer/iis/w3c.go` (edit `AllowedAttrs` only)
- `internal/importer/iis/iis_test.go`
- `internal/casestore/sqlite/cookie_attr_test.go` (new)

## Must not change
- The exact-match path for every other attribute key, the count limit `MaxEventAttrs = 16`, the key limit `maxEventAttrKey = 64` and the value limit `MaxEventAttrValue = 256`.
- `internal/casestore/sqlite/case.go`: it already passes `meta.AllowedAttrs` to `e.ValidateFor`, so the single check in `Event.Validate` covers it (no edit; only a test here). The same holds for `casestore/memory.go` and `importer.Collect`.
- The sentinel is a permission in the allowed map only; an event whose key is literally `cookie:*` is refused.
- `TestFix5ExportPolicyCoversEveryImporter` and `TestBuildCaseNoIPsOrUserIDs` stay green.

## Changes
1. `event.go`: add exported constants `CookieAttrPrefix = "cookie:"` and `CookieAttrWildcard = "cookie:*"` next to `MaxEventAttrs`. Add an unexported `func cookieAttrAllowed(allowed map[string]bool, k string) bool`: true only when `allowed[CookieAttrWildcard]`, `strings.HasPrefix(k, CookieAttrPrefix)`, `len(k) > len(CookieAttrPrefix)`, and every byte after the prefix is one of `a-z 0-9 _ . -` (the name charset of `casestore.CleanCookieNames`, so digest keys can never carry free text). Verified anchor: the key check in `Validate` is `if k == "" || len(k) > maxEventAttrKey || !allowedAttrs[k]` (line ~137). Change it so the key passes when `allowedAttrs[k]` or `cookieAttrAllowed(allowedAttrs, k)`; the empty-key and length checks are unchanged. Add a one-line doc note on `Validate` that `"cookie:*"` is a prefix permission.
2. `w3c.go`: add `"cookie:*": true` to `W3C.AllowedAttrs()` (after WP-02b it holds the 10 static keys, `client-ip` and `referer`). `iis-httperr` does not get it.
3. `caseexport_enums.go`: add `"cookie:*": true` to the `"iis-w3c"` entry of `exportDropped` (now `stem`, `client-ip`, `referer`, `cookie:*`). Note: the drift test compares exact keys, so the sentinel itself is listed; the export projection drops real `cookie:<name>` keys by prefix in WP-07.
4. `event_test.go`, new `TestEventValidateCookieWildcard` (build on `goodEvent()`):
   - allowed `{"status":true,"cookie:*":true}` and attr `cookie:sid` -> nil.
   - the same attr with allowed lacking the sentinel -> error wrapping `ErrEventInvalid`.
   - key `cookie:` (nothing after the prefix), `cookie:UP` (upper case), `cookie:a b`, `cookie:a;b`, and the literal key `cookie:*` -> each rejected even with the sentinel allowed.
   - key `cookiex:sid` and `other` (not listed) -> rejected with the sentinel allowed.
   - a key of 64 bytes (`cookie:` plus 57 `a`) passes; 65 bytes fails with the existing length rule.
   - 17 attrs including cookie keys -> still rejected by the count rule.
   - value longer than 256 bytes for a cookie key -> rejected by the existing value rule.
   Existing `TestEventValidate` and `TestEventValidateFor` are not edited.
5. `iis_test.go`, new tests:
   - `TestW3CAllowedAttrsHasCookieWildcard`: `NewW3C().AllowedAttrs()["cookie:*"]` is true, `client-ip` and `referer` are true, `NewHTTPERR().AllowedAttrs()["cookie:*"]` is false, and the total is at most 16 (the existing `TestAttrsWithinAllowlist` still passes).
   - `TestW3CCookieAttrsPassCollect`: use `cookieFile` (WP-02b, in `iis_test.go`), `testPolicy` (WP-02a, in `privacy_helpers_test.go`) and `mustParse` (which runs `importer.Collect` and `e.Validate(imp.AllowedAttrs())`); a row with `sid=abc123;+theme=dark` and a policy allowing both gives events with `cookie:sid` and `cookie:theme`, no parse error and no rejected rows.
6. `internal/casestore/sqlite/cookie_attr_test.go` (new, package `sqlite`; helpers `newTestStore`, `mustCreate`, `mustOpen`, `tMeta`, `tBody`, `tRun`, `tEvents`, `counts` from `helpers_test.go`). Build one event with `tEvents(c, "ck", 1)`, then set `Attrs = map[string]string{"counter":"total","cookie:sid":"0123456789abcdef0123456789abcdef"}`:
   - `TestImportAcceptsCookieAttrWithWildcard`: `meta := tMeta("ck.csv")` with `meta.AllowedAttrs = map[string]bool{"counter": true, "cookie:*": true}`; `c.Import` succeeds and `counts` shows 1 event; reading the event back keeps the `cookie:sid` value.
   - `TestImportRefusesCookieAttrWithoutWildcard`: `meta.AllowedAttrs = map[string]bool{"counter": true}`; `c.Import` returns a non-nil error (assert `errors.Is(err, evidence.ErrEventInvalid)` if the store passes it through, otherwise only that an error occurred), and `counts` shows 0 events.
   - `TestImportRefusesBadCookieKeyEvenWithWildcard`: attr key `cookie:UP` with the sentinel allowed fails the same way and stores nothing.

## Proof
- `go test ./internal/evidence/ -run "TestEventValidateCookieWildcard|TestEventValidate|TestFix5ExportPolicyCoversEveryImporter|TestBuildCaseNoIPsOrUserIDs" -count=1` -> `ok`.
- `go test ./internal/importer/iis/ -run "TestW3CAllowedAttrsHasCookieWildcard|TestW3CCookieAttrsPassCollect|TestAttrsWithinAllowlist" -count=1` -> `ok`.
- `go test ./internal/casestore/sqlite/ -run "TestImportAcceptsCookieAttrWithWildcard|TestImportRefusesCookieAttrWithoutWildcard|TestImportRefusesBadCookieKeyEvenWithWildcard" -count=1` -> `ok`.
- `go test ./internal/evidence/... ./internal/importer/... ./internal/casestore/... -count=1` -> `ok`.
- `go vet ./internal/evidence/ ./internal/importer/iis/ ./internal/casestore/sqlite/` -> no output.

## Depends on
WP-02b
