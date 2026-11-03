# TSK-01 thresholds-fields

risk: medium — mechanical, but any slip breaks the fingerprint tests that pin the table.

## Outcome

`internal/investigate/thresholds.go` has `Version = "2026-11-02"` and 8 new threshold fields with values, listed in `Entries()`. `thresholds_test.go` knows the new version and pins the new values.

## Write scope

- `internal/investigate/thresholds.go`
- `internal/investigate/thresholds_test.go`

## Must not change

- Existing fields, values, comments, and the order of existing `Entries()` rows.
- `testdata/**` (done in TSK-02), `internal/investigate/*.go` other than the two files above.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

All in `thresholds.go`:

1. Change the const `Version` from `"2026-10-27"` to `"2026-11-02"`.
2. In `type Thresholds`, directly after the field `OriginBucketSeconds int \`json:"originBucketSeconds"\`` (line ~143) and before the closing `}`, add these 8 fields, each with a one-line comment above in the file's style (comment names what it decides). Exact names, types, tags:
   - `GateRefMinEvents int` tag `gateRefMinEvents` — fewest reference events before a rate is compared.
   - `GateRefMinEventsP95 int` tag `gateRefMinEventsP95` — fewest reference events before a reference P95 is compared.
   - `GateIncMinEvents int` tag `gateIncMinEvents` — fewest incident events before a rate is compared.
   - `GateSpreadMinShare float64` tag `gateSpreadMinShare` — share of covered reference minutes that must hold an event.
   - `GateSpreadMaxMinuteShare float64` tag `gateSpreadMaxMinuteShare` — largest share of reference events one minute may hold.
   - `GateSpreadSkipBelowPerMin float64` tag `gateSpreadSkipBelowPerMin` — mean events per covered reference minute below which the spread rule is skipped.
   - `LaneClockToleranceMs float64` tag `laneClockToleranceMs` — clock margin used when either tier is a lane (SQL, Mongo, host). Used by Phase 02.
   - `OriginPersistShare float64` tag `originPersistShare` — share of minutes after an onset that must stay above the limit for the onset to count. Used by Phase 02.
3. In `Current()`, after the line `OriginBucketSeconds:   60,` add (gofmt will align): `GateRefMinEvents: 60`, `GateRefMinEventsP95: 100`, `GateIncMinEvents: 20`, `GateSpreadMinShare: 0.5`, `GateSpreadMaxMinuteShare: 0.25`, `GateSpreadSkipBelowPerMin: 1`, `LaneClockToleranceMs: 5000`, `OriginPersistShare: 0.5`.
4. In `Entries()`, after the last row `i("originBucketSeconds", ...)` add rows in the SAME order as above: `i("gateRefMinEvents", t.GateRefMinEvents)`, `i("gateRefMinEventsP95", t.GateRefMinEventsP95)`, `i("gateIncMinEvents", t.GateIncMinEvents)`, `f("gateSpreadMinShare", t.GateSpreadMinShare)`, `f("gateSpreadMaxMinuteShare", t.GateSpreadMaxMinuteShare)`, `f("gateSpreadSkipBelowPerMin", t.GateSpreadSkipBelowPerMin)`, `f("laneClockToleranceMs", t.LaneClockToleranceMs)`, `f("originPersistShare", t.OriginPersistShare)`. (`i` for ints, `f` for floats.)
5. Run `gofmt -w internal/investigate/thresholds.go` (or `.dev\toolchains\go\bin\gofmt.exe -w ...`).

In `thresholds_test.go`:

6. In `TestThresholdsVersionLiteral` (line ~86) change the literal `"2026-10-27"` to `"2026-11-02"` and the message text to `the current table is 2026-11-02`.
7. In `TestThresholdsMatchDecisions` extend the `checks` slice (it holds `{name, got, want float64}` rows) with: `{"R1 gate ref min", float64(c.GateRefMinEvents), 60}`, `{"R2 gate ref min P95", float64(c.GateRefMinEventsP95), 100}`, `{"R1 gate inc min", float64(c.GateIncMinEvents), 20}`, `{"R1 spread min share", c.GateSpreadMinShare, 0.5}`, `{"R1 spread max minute share", c.GateSpreadMaxMinuteShare, 0.25}`, `{"R1 spread skip per min", c.GateSpreadSkipBelowPerMin, 1}`, `{"R6 lane clock ms", c.LaneClockToleranceMs, 5000}`, `{"R5 persist share", c.OriginPersistShare, 0.5}`.

## Proof

```
.dev\toolchains\go\bin\go.exe build ./internal/investigate
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestThresholdsVersionLiteral|TestThresholdsFingerprintMoves|TestThresholdsMatchDecisions|TestThresholdsNames"
```

Both PASS. Expected and NOT a failure yet: `TestThresholdsHistory` and the golden tests FAIL until TSK-02 runs; do not fix them here. Report that in `progress.md`.

## Depends on

None.
