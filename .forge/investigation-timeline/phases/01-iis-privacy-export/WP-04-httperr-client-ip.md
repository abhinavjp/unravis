# WP-04 HTTPERR stores the client IP when the file has one
risk: medium    size: S    wave: 5    model: sonnet

## Outcome
HTTP.sys error log rows store `client-ip` (from `c-ip`) when the file declares that column; a file without it imports as before and says so in the import's known gaps. Server IP and ports stay dropped (TR-19). The behaviour when no real sample exists is the conservative one recorded by WP-45 (VF-01c).

## Write scope
- `internal/importer/iis/httperr.go`
- `internal/importer/iis/iis_test.go` (edit `TestHTTPERRGood` only)
- `internal/importer/iis/httperr_clientip_test.go` (new)
- `internal/evidence/caseexport_enums.go` (edit the `exportDropped` literal only)

## Must not change
- The HTTPERR `#Fields` requirement, the `required` list (`date`, `time`) and the reason mapping: the parser still quarantines a file with no `#Fields` header. Note: VF-01c assumed real HTTPERR files carry no `#Fields` header, but the code requires one; this packet does not change that (see WP-45, check VF-01c).
- No symbol from WP-02a or WP-02b is used: the IP check here is a private helper with its own limit.
- Server IP (`s-ip`), client port (`c-port`), server port, protocol version and query strings stay dropped: `keep` gains `c-ip` only.
- `w3c.go`, `common.go` and the W3C attrs.

## Changes
1. `httperr.go`: add `"c-ip"` to `httperrSpec.keep` (so `declare` records it in `st.declared`). Add `"client-ip": true` to `HTTPERR.AllowedAttrs()` (currently `reason`, `status`, `method`, `stem`, `queue`, `siteid`). Change the `attrs` capacity in `httperrRow` from 6 to 8.
2. `httperr.go`: `func httperrClientIP(s string) bool` (private): false when `len(s) == 0 || len(s) > 64`, or `s` contains a space or "/"; otherwise `net.ParseIP(s) != nil`. In `httperrRow`, after the existing attrs are filled and before the event is returned, `if v, ok := c.get("c-ip"); ok && httperrClientIP(v) { attrs["client-ip"] = v }` (value stored unchanged). An invalid or `-` value never rejects the row.
3. `httperr.go`: constant `gapHTTPERRNoClientIP = "HTTP error log has no client IP column; client addresses are not available for this file."` (no cause words: the causal lint `evidence.LintCausal` runs over known gaps). In `httperrSpec.gaps` return `[]string{gapW3CResolution}` plus `gapHTTPERRNoClientIP` when `!st.declared["c-ip"]`.
4. `caseexport_enums.go`: in `exportDropped` change the existing entry `"iis-httperr": {"stem": true}` to `{"stem": true, "client-ip": true}`. The drift test needs every importer-allowed attr to be exported or listed as dropped. Note: this file is not in the spec's file list.
5. `iis_test.go`, edit `TestHTTPERRGood`: event 1 now has two attrs, so change `len(evs[1].Attrs) != 1` to `!= 2` and also require `evs[1].Attrs["client-ip"] == "192.0.2.51"`; add `"client-ip": "192.0.2.50"` to `want` (event 0); in the banned list replace `"192.0.2."` with `"192.0.2.10"` (the server IP). `"51000"` stays banned (client port).
6. `httperr_clientip_test.go` (new, package `iis`; helpers `httperrFile`, `httperrOpts`, `fixture`, `mustParse`, `dump` from `iis_test.go`):
   - `TestHTTPERRStoresClientIP`: the fixture `httperr_good.log` gives `client-ip` 192.0.2.50 to 192.0.2.53 on events 0 to 3; each value passes `net.ParseIP`; the dumped output does not contain `192.0.2.10` (server IP) or `51000`..`51003` (ports).
   - `TestHTTPERRNoClientIPColumnGap`: a file built in the test with 40 rows under the header `date time cs-method cs-uri sc-status s-reason s-queuename` (no `c-ip`; `httperrFile` always declares it) imports all rows, no event has `client-ip`, and `rep.KnownGaps` contains `gapHTTPERRNoClientIP`; the fixture file (with `c-ip`) does not contain it.
   - `TestHTTPERRInvalidClientIP`: extra rows appended to `httperrFile(...)` (layout `date time c-ip cs-method cs-uri sc-status s-reason s-queuename`) with `c-ip` equal to `-`, `not-an-ip`, `1.2.3.4/32` and a 65-character string are all emitted without `client-ip` and `rep.Rejected == 0`.
   - `TestHTTPERRClientIPGapPassesCausalLint`: `evidence.LintCausal(gapHTTPERRNoClientIP)` returns nil.
   - `TestHTTPERRAllowedAttrsHasClientIP`: `NewHTTPERR().AllowedAttrs()["client-ip"]` is true and the count is at most 16.

## Proof
- `go test ./internal/importer/iis/ -run "HTTPERR" -count=1` -> `ok` (the five new tests and the existing HTTPERR tests, including conformance).
- `go test ./internal/importer/... ./internal/evidence/... -count=1` -> `ok` (includes the export drift test and the `importer/all` prober test).
- `go vet ./internal/importer/iis/ ./internal/evidence/` -> no output.

## Depends on
WP-45, WP-02b, WP-03
