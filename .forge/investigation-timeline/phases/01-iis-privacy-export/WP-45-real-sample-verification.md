# WP-45 Verify IIS and Query Store assumptions on real samples (VF-01)
risk: medium    size: M    wave: 1    model: sonnet

## Outcome
Each VF-01 claim (a to h) has its own recorded line in `internal/importer/iis/verify_notes.go` with a status `CONFIRMED`, `REFUTED` or `UNMEASURED` and a reason, and a test run proves the notes agree with the code constants. With no real sample on this machine every line is `UNMEASURED` with a stated reason and the conservative behaviour of WP-04, WP-20 and WP-59 applies (TR-31, TR-22).

## Write scope
- `internal/importer/iis/verify_notes.go` (new)
- `internal/importer/iis/verify_test.go` (new)
- `internal/importer/iis/w3c.go` (the `w3cTimeIsEnd` constant and its comment only, and only if VF-01b is `REFUTED`)

## Must not change
- `TestW3CTimeIsEndGap`, `gapW3CTimeEnd`, `gapW3CTimeStart`, `gapW3CResolution`: even when VF-01b is `CONFIRMED` the gap text stays (dropping it is a decision for WP-20 and the owner, not this packet).
- `httperr.go` and the HTTPERR `#Fields` requirement: if VF-01c shows real files have no `#Fields` header, that is an ESCALATION in `progress.md`, not an edit here.
- `internal/importer/sqlserver/querystore.go`: WP-59 adds the comment from the VF-01h line.
- No real sample file, path, host name, IP, cookie or line is copied into any committed file, test log output or note. `.dev/` is git-ignored; never `git add` it. Tests print counts and verdicts only.
- No new dependency; no network access; no `.unravis.db`.

## Changes
1. Sample location (decided here, the spec leaves it open). A test helper `sampleDir(t) string` in `verify_test.go` returns `os.Getenv("UNRAVIS_VF01_SAMPLES")` when set, else `<repo root>/.dev/samples/vf01` (find the repo root by walking up from the test's working directory to the folder holding `go.mod`). Return "" when the folder does not exist. Expected files: `w3c*.log` (IIS W3C), `httperr*.log` (HTTP.sys), `querystore*.csv` (Query Store export), and optional sidecars `paired-w3c.json` and `paired-querystore.json` (formats in step 5). As found today the folder does not exist, so every measurement skips.
2. `verify_notes.go`: `type VerifyStatus string` with consts `VerifyConfirmed = "CONFIRMED"`, `VerifyRefuted = "REFUTED"`, `VerifyUnmeasured = "UNMEASURED"`. `type VerifyNote struct { ID, Claim string; Status VerifyStatus; Reason string }`. Exported package variables `Vf01a`, `Vf01b`, `Vf01c`, `Vf01d`, `Vf01e`, `Vf01f`, `Vf01g`, `Vf01h` of that type, and `func VerifyNotes() []VerifyNote` returning them in that order. One line per check, never merged. Claims (copy exactly): a "IIS time-taken includes network time to the last send completion"; b "W3C time is the request end"; c "HTTPERR files carry no #Fields header"; d "Trailing HTTPERR field is s-queuename and varies by IIS version"; e "First W3C row of a file has an inflated time-taken"; f "Mongo system.profile needs a write to enable"; g "dm_os_wait_stats is cumulative"; h "Query Store durations are microseconds".
3. Initial values when no sample exists (the expected outcome): a, b, c, d, e, h `UNMEASURED` with reason `no real sample or paired capture on this machine (looked in UNRAVIS_VF01_SAMPLES and .dev/samples/vf01)`; f and g `UNMEASURED` with reason `needs a live Mongo or SQL Server and a deliberate read-only check; not a file sample`. If samples do exist, the implementer runs the measurement (step 5) and records the true verdict and a one-line reason with counts only (for example `31 paired requests, 30 within 1 s of client end`).
4. `verify_notes.go` also holds the named constant `verifiedTimeTakenScope = false` (unexported, bool: true only when Vf01a is `CONFIRMED`) with a one-line comment; WP-20 and the tooltip text read it later. Add a file comment saying what each status means and that `UNMEASURED` keeps today's conservative code.
5. `verify_test.go` (package `iis`; helpers `fixture`, `mustParse`, `baseOpts` from `iis_test.go`):
   - `TestVerifyNotes` (always runs): exactly 8 notes, ids `VF-01a` to `VF-01h` in order; every status is one of the three consts; every reason is non-empty; `Claim` is non-empty; no reason contains a path separator or an IP-like string (regexp `\d+\.\d+\.\d+\.\d+`); `verifiedTimeTakenScope == (Vf01a.Status == VerifyConfirmed)`.
   - `TestVerifyW3CTimeIsEndMatchesNote` (always runs): if `Vf01b.Status == VerifyRefuted` then `w3cTimeIsEnd` must be false; if `VerifyConfirmed` or `VerifyUnmeasured` it must be true. Failure message names the note and the constant.
   - `TestVerifyMeasureW3C` (skips with `t.Skip("no VF-01 samples: set UNRAVIS_VF01_SAMPLES")` when `sampleDir` is ""): parses each `w3c*.log` with `NewW3C()` through `mustParse`-style collection (read the file with `os.ReadFile`, never log content). Check e: for each file with at least 50 rows compare the first row `time-taken` with 5 times the median of the rest; record `CONFIRMED` when this holds in at least 2 of at least 3 files, `REFUTED` when in none of at least 3 files, otherwise `UNMEASURED` (fewer than 3 files or mixed). If `paired-w3c.json` exists (`{"requests":[{"method","stem","status","startUtc","endUtc","clientTotalMs","rttMs"}]}`, UTC RFC 3339 strings) and at least 30 rows match a log row by method, stem and status in order: check b by counting rows where the logged time is within [end - 2 s, end + 1 s] (end reading) versus within [start - 2 s, start + 1 s] (start reading); 95 percent or more under one reading gives `CONFIRMED` (end) or `REFUTED` (start), else `UNMEASURED`. Check a: median of `clientTotalMs - time-taken` within `max(50, 2 * rttMs)` gives `CONFIRMED`, a median above that bound gives `REFUTED`. These thresholds are this packet's proposal; the owner confirms them with the first real sample. The test compares each measured verdict with the recorded note and fails with `update verify_notes.go: <id> measured <status>, recorded <status>` if they differ; it never rewrites the file.
   - `TestVerifyMeasureHTTPERR` (same skip rule, files `httperr*.log`): check c: count files whose first non-blank line starts with `#Fields:`; check d: for those files the last name in `#Fields` is `s-queuename`. Same compare-and-fail rule. Files without `#Fields` are counted only (the parser quarantines them today).
   - `TestVerifyMeasureQuerystore` (same skip rule, files `querystore*.csv` plus `paired-querystore.json` `{"queries":[{"queryId":1,"serverMs":1250.0}]}`): with at least 10 queries, check h: median of `serverMs / (avg_duration / 1000)` within [0.95, 1.05] gives `CONFIRMED`, a ratio near 1000 or near 0.001 gives `REFUTED`, else `UNMEASURED`. Skips with a named reason if the sidecar is absent.
6. `w3c.go`: only if the recorded `Vf01b` is `REFUTED`: set `w3cTimeIsEnd = false` and rewrite its comment to state the measured reading. Otherwise do not touch the file.
7. Fallbacks while a line is `UNMEASURED` (written into the notes' reasons only where they apply, for the other packets to read): WP-04 stores `client-ip` only when the file declares `c-ip` and records a gap otherwise; WP-20 draws a point at the logged timestamp and shows `time-taken` as a separate observed value, never as processing time; WP-59 keeps the division by 1000 as coded.

## Proof
- `go test ./internal/importer/iis/ -run "TestVerifyNotes|TestVerifyW3CTimeIsEndMatchesNote" -count=1 -v` -> `PASS` for both.
- `go test ./internal/importer/iis/ -run "TestVerifyMeasure" -count=1 -v` -> `SKIP` with the reason `no VF-01 samples: set UNRAVIS_VF01_SAMPLES` (no sample here), or `PASS` with samples.
- `go test ./internal/importer/iis/... -count=1` -> `ok` (includes `TestW3CTimeIsEndGap` unchanged).
- `go vet ./internal/importer/iis/` -> no output.
- `git diff -- internal/importer/iis/w3c.go` -> empty unless VF-01b is `REFUTED`.
- `rg -n "samples/vf01|UNRAVIS_VF01" --glob "!*_test.go" --glob "!verify_notes.go" internal` -> no output (the location is named only in the notes and the test).

## Depends on
none
