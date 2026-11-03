# WP-05 IIS privacy tests match the new rule
risk: medium    size: S    wave: 6    model: sonnet

## Outcome
The IIS tests state the new privacy rule instead of the old one: `TestW3CDropsSensitiveColumns` is replaced by tests that prove what is still dropped (server IP, user agent, query string, non-allowlisted cookies, referer query) in every byte of events and report, and the conformance harness also runs a cookie-policy variant. `TestW3CTimeIsEndGap` is untouched (TR-19, TR-20, TR-30).

## Write scope
- `internal/importer/iis/iis_test.go`
- `internal/importer/iis/conformance_test.go`

## Must not change
- `TestW3CTimeIsEndGap` (iis_test.go, around line 215): not edited, not renamed, not moved.
- No production file: this packet is tests only. WP-02b and WP-04 already made the minimal edits to `TestW3CGood`, `TestHTTPERRGood` and the old banned list; this packet does the final cleanup.
- The shared fixtures `testdata/w3c_good.log` and `testdata/httperr_good.log` are read-only here.
- Test helper signatures from WP-02a and WP-02b (`testPolicy` in `privacy_helpers_test.go`; `parseRaw`, `cookieFile` in `iis_test.go`) are used as they are.

## Changes
1. `iis_test.go`: delete `TestW3CDropsSensitiveColumns` (it runs `mustParse` on `w3c_good.log` and bans `sentinel`, `"192.0.2.10"`, `"Mozilla"`, `"curl/"`, `"session="`, `"alice"`, `"bob"`, `"WEB-A"`, `"q="` after WP-02b's edit). Replace it with two tests:
   - `TestW3CDropsServerIPUserAgentQueryAndRawCookies`: `mustParse` on the fixture with `baseOpts()`; `dump(evs, rep)` must not contain `sentinel`, `192.0.2.10`, `Mozilla`, `curl/`, `session=`, `alice`, `bob`, `WEB-A`, `q=`, `frag`; the dump does contain `192.0.2.77` and `http://ref.example.test/landing` (kept by design); no event has a key starting with `cookie:`.
   - `TestW3CNoPseudonymiserDropsUser`: the nil-`Pseudonymize` check from the old test, unchanged in meaning: with `o.Pseudonymize = nil` no event has the `user` attr.
2. `iis_test.go`, new `TestW3CPrivacyRuleSentinels` using `cookieFile`-style rows with header `date time s-computername cs-method cs-uri-stem sc-status time-taken s-ip c-ip cs-uri-query cs(User-Agent) cs(Cookie) cs(Referer)` (build the header and at least 40 rows in the test; real W3C cookies use `;+` between pairs). Put `SENTINEL-7f3a` (use the `sentinel` constant) in the query, in the user agent, in the value of a non-allowlisted cookie (`other=SENTINEL-7f3a`), and in the referer query and fragment and userinfo (`http://user:SENTINEL-7f3a@ref.example.test/p?x=SENTINEL-7f3a#SENTINEL-7f3a`). Use `server IP 192.0.2.10`, `c-ip 192.0.2.77`, policy `testPolicy("keep")` and cookie field `keep=rawvalue-9x;+other=SENTINEL-7f3a`. Parse through `parseRaw` and also through `importer.Collect`-based `mustParse` after WP-03 (both must succeed). Assert, on the JSON of all events plus the dumped report: no `SENTINEL-7f3a`, no `192.0.2.10`, no `cookie:other`, no `rawvalue-9x`; each event has `client-ip=192.0.2.77`, `referer=http://ref.example.test/p`, and a `cookie:keep` value of 32 lower-case hex characters equal to `HashCookie(policy, "keep", "rawvalue-9x")`.
3. `iis_test.go`, new `TestW3CNoCookieAttrsWithoutPolicy`: the same file as step 2 with `Options.CookiePolicy == nil` and, in a second run, with a policy whose `Allowed` is empty: no event has any key starting with `cookie:`; `client-ip` and `referer` are still present.
4. `iis_test.go`, new `TestW3CCookieAttrBudget`: a row with a policy allowing 6 names (more than the 4 the store allows; built by hand with `testPolicy`) and cookies for all 6 stays valid: `e.Validate(NewW3C().AllowedAttrs())` is nil, the attr count is at most 16 and at most 4 attrs start with `cookie:`.
5. `conformance_test.go`: keep `NoFreeText: true` and `KeepsCellValues: true` in `w3cFixtures`, and update their comment: attributes are typed tokens, numbers, allowlisted words, normalised routes, pseudonyms, a validated IP, a scheme+host+path referer or a keyed digest. Add `TestW3CConformanceWithCookiePolicy`: copy `w3cFixtures(t)`, set `Options.CookiePolicy = testPolicy("session")` (the fixture's first row has a `session=` cookie, with the sentinel already replaced by `clean`), and run `importertest.Run(t, NewW3C(), fx)`. If the harness's generated-cell checks fail only because of the new attrs, record the failing check in `progress.md` `deviation:` instead of weakening the importer; do not change `importertest`.
6. `conformance_test.go`: `TestHTTPERRConformance` and the two fuzz targets are unchanged (the fuzz seeds still use the shared fixtures).

## Proof
- `go test ./internal/importer/iis/ -run "TestW3CDropsServerIPUserAgentQueryAndRawCookies|TestW3CNoPseudonymiserDropsUser|TestW3CPrivacyRuleSentinels|TestW3CNoCookieAttrsWithoutPolicy|TestW3CCookieAttrBudget|TestW3CConformance|TestW3CConformanceWithCookiePolicy|TestW3CTimeIsEndGap" -count=1` -> `ok`.
- `go test ./internal/importer/iis/... -count=1` -> `ok`.
- `go test ./internal/importer/iis/ -run FuzzW3CParse -fuzztime=10s` -> `ok` (optional; seeds run in the normal test pass anyway).
- `grep -n "TestW3CDropsSensitiveColumns" internal/importer/iis/*.go` -> no output.
- `git diff -U0 -- internal/importer/iis/iis_test.go | grep -c "TestW3CTimeIsEndGap"` -> `0` (the test is not in the diff).

## Depends on
WP-03, WP-04
