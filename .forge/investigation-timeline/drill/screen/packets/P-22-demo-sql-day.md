# P-22 Demo case has a saved Query Store day

model: sonnet, effort: medium. Reason: seed data must match the real saved shape exactly.
risk: low. Review batch: RB-2. Wave: W1. Requirement: R-17 (demo path).

## Outcome
The demo case "Orders API stall, 19 October" has one saved SQL lane day with a Query Store series around the stall, so the database lane draws in the demo and in e2e.

## Write scope
- `cmd/audit-explorer/demo_cases.go`
- `cmd/audit-explorer/demo_cases_test.go` (existing or new)

## Must not change
- Other demo cases. `internal/server/demo_lanes.go`. Real code paths: the seed must go through the same save function a real lane read uses, not a side door. No credential-like string in the seed (database name `AuditDb`, no server name, no login).

## Changes
1. Read `demo_cases.go` around `laneDayNothingRead` (line 472) to see how a lane day is seeded today (a day where nothing was read) and which save function it calls.
2. Add a helper `laneDayQueryStore(...)` beside it that builds a `dbhealth.LaneResult` with: the checks the real read produces for a healthy read (copy the ids from `internal/dbhealth/lane.go`, at least `querystore` in its passing state), `QueryStore: &dbhealth.LaneQueryStore{State: "on", FirstData: <demo now − 7 days>, IntervalMinutes: 15}`, and a `Series` of `LaneSeriesPoint` every 15 minutes for the 4 hours around the demo stall. Values: quiet blocks `Execs 1200, AvgMs 8, MaxMs 60`; the two blocks that cover the stall `Execs 900, AvgMs 240, MaxMs 9000`.
3. Times come from the demo clock the file already uses (`UNRAVIS_DEMO_NOW`; e2e pins it to `2026-10-20T09:00:00Z`). Place the stall where the demo audit data and the seeded `sql-xe` events of this case already put it; find that time in the file, do not invent a second one.
4. Seed it only for the stall case. The other demo cases keep `laneDayNothingRead`.
5. Test: load the demo cases, find the stall case, assert it has exactly one saved lane day whose result has `QueryStore.State == "on"` and at least 16 series points, and that another demo case still has none.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./cmd/audit-explorer/` → no output.
- [ ] `go test ./cmd/audit-explorer/ -count=1` → `ok`.
- [ ] `git diff -- cmd/audit-explorer/demo_cases.go | Select-String -Pattern 'password|pwd=|token|Server='` → no match.
- [ ] Demo only by nature. Says so in the report.

## Depends on
P-16 (type `LaneQueryStore`).
