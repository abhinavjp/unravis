# WP-25 Demo case with SQL and IIS lanes for the tracing view
risk: low    size: M    wave: 4    model: sonnet

## Outcome
With `--demo` and `UNRAVIS_DEMO_CASES=1` the demo folder holds one more case, "Orders API stall, 19 October", whose saved data fills the `sql` and `iis` lanes of `POST /api/cases/{id}/tracing` for the hour 09:00-10:00 UTC on the day before the demo clock: 18 SQL Server events, 48 web requests (a few 5xx with a win32 code) and 3 HTTP.sys error markers. The phase 04 UI and e2e packets use it to see a lane with bars, markers and a clean timeline. Covers the demo requirement of Appendix P5 (SQL and IIS lanes visible in the demo). Spec WP-25 asserts three lanes; the audit lane needs WP-17 (phase 04), so this packet asserts two and phase 04 adds the third.

## Write scope
- `cmd/audit-explorer/demo_cases.go`
- `cmd/audit-explorer/demo_cases_test.go`

## Must not change
- Every existing demo case, its fixed id, title, age, import count and events (`demoEvents`, `importOK`, `importRefused`, `laneDayNothingRead`, `afterCreate`); the existing step order (the new step is appended last).
- `demoCaseIDs()` entries already there; the `demoIDSource` rules (ids still end in `a` padding to 26 characters; the first eight characters of each demo id are distinct).
- Real data and secrets: no host name, address, user name or SQL text of a real server; no `client-ip`, `referer`, `cookie:*` attribute; no credential. Everything is synthetic and the import report says so (`KnownGaps: "synthetic demo data"`).
- `tools/e2e/support/api.ts` (its demo id map; phase 04 e2e packets add the new id there).
- Never assert a millisecond figure in a test.

## Changes
1. `demo_cases.go`: new var `demoCaseTracingID = demoCaseID("demotracing")` in the `var (` block (first eight characters `demotrac`, distinct from the others) and append it to `demoCaseIDs()`.
2. New step appended after the keyed case in `seed`: `{demoCaseTracingID, "Orders API stall, 19 October", 1 * day, func(ctx, c, created) error { return d.importTracing(ctx, c, created) }}`. The case stays open, no close, no archive; `afterCreate` already ignores unknown ids.
3. `func (d demoSeeder) importTracing(ctx context.Context, c casestore.Case, created time.Time) error`. Let `base = created.Truncate(time.Hour)` (09:00 UTC when the demo clock is 2026-10-20T09:00Z). Make three imports in this fixed order, so the first import ids are 1, 2, 3 in a fresh case: `sql-xe` (file `demo-xe-sql-a.xel`), `iis-w3c` (file `demo-access-web-a.log`), `iis-httperr` (file `demo-httperr-web-a.log`). Each at `created.Add(10*time.Minute)`, same pattern as `importOK` (one `c.Import` call, synthetic body, `rep.KnownGaps = []string{"synthetic demo data"}`), with `ImportMeta{Importer: <name>, FileName: <file>, HostLabel: demoHost, TZRule: clock.RuleUTCField, AckGating: true, AckText: demoAckText}`. Write a small `demoMetaFor(importerName, file string) casestore.ImportMeta` and let `demoMeta` call it, so existing callers are unchanged.
4. Events (fixed, no randomness; `EvidenceID` = `evidence.EventID(string(caseID), importer, label, line, 0)` with `label` the file name and `line` = index + 2; `Host: demoHost`; `Unit: "ms"`; `TZRule: clock.RuleUTCField`):
   - SQL: 18 events `Kind: "slow-op"`, one every 3 minutes from `base+5m`, `Start` = that time, `End` = `Start` + `DurationMs`, duration `250 + (i%6)*150` ms, and every sixth event (i%6 == 5) `Severity: "warning"` and duration 1800 ms. No `Attrs`.
   - IIS: 48 events `Kind: "request"`, one every minute from `base+5m`, `Start == End` (point model of WP-20), `DurationMs` = `120 + (i%7)*40`. `Attrs`: `status` `"200"`, `method` `"GET"`, `stem` `"/api/v1/orders/{id}"`; for `i%12 == 11` (4 events) `status` `"503"` and `win32` `"64"`. Use only attribute keys the importer's `AllowedAttrs` lists.
   - HTTPERR: 3 events `Kind: "httperr"`, at `base+20m`, `base+21m`, `base+40m`, `Start == End`, `Attrs` `reason` `QueueFull`, `QueueFull`, `Connection_Dropped`, `status` `"503"`.
5. Note: the spec puts the seeds in `demo_lanes.go`; there is no such file, the seeds live in `demo_cases.go`, so they are added here. Note: a new case adds a row to the start-screen list, so any screenshot taken with `UNRAVIS_DEMO_CASES=1` shows one more case; the capture owner re-takes those pictures after this packet (the older cases themselves are unchanged). The spec's e2e file name for this case could not be verified in the repo, so no e2e file is written here.
6. Tests in `demo_cases_test.go`:
   - Update `TestDemoCasesSeedSixCasesWithFixedIDsTitlesAndTimes`: `len(list)` 7 -> 8 (message text too), and add `want(string(demoCaseTracingID), "Orders API stall, 19 October", demoTestNow.Add(-1*day), nil, 3)` (3 imports).
   - Add `TestDemoTracingCaseFillsSQLAndIISLanes` (uses `seededServer`; POST `/api/cases/<id>/tracing` with the headers of the lanes test at line 636-640, body `sourceIds` `["imp_1","imp_2","imp_3"]`, incident `2026-10-19T09:00:00Z` to `2026-10-19T10:00:00Z`; expect 200, lane keys `sql` then `iis`, both `state` `events`, `origin` `case`, bucket count sums 18 and 51 (48 requests + 3 markers), `iis` events include one with `statusClass` `5xx` (the `kind` marker is WP-19's test, not asserted here), `allLanesStored` true, `mixedConsistency` false)
   - Add `TestDemoTracingCaseIsStable` (two starts on fresh folders give identical `lanes` JSON apart from `asOf` and `requestSeq`)
   - Add `TestDemoTracingCaseHasNoPrivateAttrs` (raw body has no `client-ip`, `referer`, `cookie:`).

## Proof
- `go build ./...` -> no output
- `go vet ./cmd/audit-explorer/...` -> no output
- `go test ./cmd/audit-explorer/... -run "Demo" -count=1` -> `ok` (all older demo tests, including the nothing-read lane day, still pass with 8 cases)
- `go test ./cmd/audit-explorer/... -count=1` -> `ok`
- Manual: `UNRAVIS_DEMO_CASES=1` with an empty `AUDIT_CONFIG_DIR`, `--demo`, POST the tracing body above with `curl` and the demo token; the JSON field names match the WP-14 contract block (Changes step 7) and the WP-15 axis fields; a second POST with no `sourceIds` key returns the same three sources (WP-14 omitted-ids rule).

## Depends on
WP-18b
