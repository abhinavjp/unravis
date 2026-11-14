# BBX-5-09 Fixed output structs + row caps (C9, C6)
Backlog: BL-124 | Group: 5 | Model: performance | Reason: the data-shape control that keeps SQL output harmless; high security impact.
Depends on: BBX-5-05 | Blocks: BBX-5-10..5-14
Reviewer: Ultimate (SQL).

## Goal
Define the fixed output structs for every SQL sample, decode rows into them rejecting unknown fields, validate wait-type names, and enforce row caps (stop at n+1, mark "truncated"). Database names off by default.

## Context
- New files `internal/bbsql/output.go` + `output_test.go`.
- C9: one `sql` object decoded into a fixed struct (unknown fields rejected in tests). Allowed: wait types (`^[A-Z0-9_]{1,60}$`), counts, ms, KB, database_id, file_id, enum states. Database names off by default.
- C6: `TOP n` in SQL; the Go reader stops at n+1 and marks "truncated". Do NOT reuse dbhealth's 5000 cap (`mssql.go:157`).
- Output structs (one per sampler; numeric/enum only):
  - Requests (BL-101): `{WaitType string, Tasks uint32, MaxWaitMs uint64, Blocked uint32, HeadBlockers uint32, MaxBlockMs uint64}`.
  - Waits (BL-102): `{WaitType string, DeltaMs uint64, DeltaTasks uint64}`.
  - Schedulers (BL-118): `{CurrentTasks uint32, RunnableTasks uint32, ...}` summed.
  - Semaphores (BL-118): `{Grantees int32, Waiters int32, Forced int32, Timeouts int32}` deltas.
  - Tempdb (BL-118): `{InternalPages uint64, UserPages uint64}`.
  - FileStats (BL-119): `{DatabaseID uint32, FileID uint32, ReadStallMs uint64, WriteStallMs uint64, Reads uint64, Writes uint64}` (no paths/names).
  - AG (BL-119): `{DatabaseID uint32, SyncState uint8}` (no replica server names).
- Rules: **fixed struct, unknown fields rejected**; **wait types regex**; **row caps + truncated**; **no database names by default**; **no text**.

## Do
1. Define the structs above with json tags. A wrapper `type Sample struct { T time.Time; Kind string; ... }` per kind, or a single `SQLSample` with optional pointers (choose fixed per-kind structs; the runner writes one NDJSON line per kind).
2. `func decodeRows(rows []Row, dst interface{}, cap int) (truncated bool, err error)` — decode each row into the fixed struct with DisallowUnknownFields semantics (reject a row carrying an unexpected column); stop at cap+1 and set truncated; validate WaitType against `^[A-Z0-9_]{1,60}$` (reject otherwise). Row is `map[string]any` (the Queryer row type).
3. `var waitTypeRe = regexp.MustCompile("^[A-Z0-9_]{1,60}$")`.
4. Caps per kind: requests 256, waits 20 (TOP 20 in SQL), filestats 256, others small. Define constants.
5. Database names: do NOT include a name field; only database_id. (If a name is ever added later it must be opt-in; not now.)

## Do not
- Do NOT accept unknown columns/fields (reject in decode + tests).
- Do NOT include query text, login/host/program names, file paths, or replica server names.
- Do NOT include database names (ids only).
- Do NOT reuse the 5000 cap; use the per-kind caps and mark truncated at n+1.
- Do NOT accept a wait type failing the regex.

## Tests to write first
- `TestDecodeRejectsUnknownField` — a row with an extra column → error.
- `TestDecodeWaitTypeRegex` — valid "PAGEIOLATCH_SH" ok; "bad;type", lowercase, >60 chars → rejected.
- `TestDecodeRowCapTruncated` — cap+1 rows → truncated true, only cap kept.
- `TestOutputNoNames` — structs have no name/path/login fields (compile-time shape + JSON marshal contains only allowed keys).
- `TestFileStatsIDsOnly` — database_id/file_id present, no physical_name.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: bounded decode; no goroutine/ticker
- [ ] No text captured — numeric/enum/wait-type only (TestOutputNoNames)
- [ ] C9 unknown fields rejected; wait-type regex (tests above)
- [ ] C6 row caps + truncated; not the 5000 cap (TestDecodeRowCapTruncated)
- [ ] Database names off (ids only)

## Live-check needed?
YES — owner confirms on a real SQL Server that decoded samples contain only allowed fields. Fake tests verify shape only.

## Review-pinned requirements (Ultimate SQL gate, wave 7 — MUST apply)
- Empty wait type: `bb:requests` emits `''` for running (non-waiting) requests (ISNULL(wait_type,'')). The C9 regex `^[A-Z0-9_]{1,60}$` rejects `''`, which would reject nearly every requests row on a healthy server and cascade into C7 auto-off. Handle this WITHOUT loosening the general regex: in the requests decode path, map an empty wait type to a fixed sentinel that passes the regex (e.g. `RUNNING`) before validation. Keep the strict regex for all other wait types. Add a test: a `''` wait type row decodes to the sentinel and is NOT rejected.
- FileStats size field: `bb:filestats` selects `size_on_disk_bytes` (BBX-5-05). Add `SizeOnDiskBytes uint64 `json:"sizeOnDiskBytes,omitempty"`` to the FileStats struct so decodeRows does not reject the column (C9 allows KB/bytes; numeric, harmless). Add it to the filestats test rows.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-09.md`.