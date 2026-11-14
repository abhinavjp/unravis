# BBX-5-14 SQL file I/O stalls + AG state samplers
Backlog: BL-119 | Group: 5 | Model: performance | Reason: file-stats deltas + HADR-gated AG state; high security impact.
Depends on: BBX-5-05, BBX-5-09, BBX-5-10 | Blocks: none
Reviewer: Ultimate (SQL).

## Goal
Every 60 s sample `dm_io_virtual_file_stats` deltas by database_id/file_id (TOP 256, no paths/names) and AG replica state ONLY when IsHadrEnabled=1 (no replica server names).

## Context
- New file `internal/bbsql/filestats.go` + `filestats_test.go`. Two Samplers via init(): "filestats" (60 s, bb:filestats, deltas in Go), "ag" (60 s, bb:ag, gated).
- bb:filestats (BBX-5-05): SELECT TOP 256 database_id, file_id, io_stall_read_ms, io_stall_write_ms, num_of_reads, num_of_writes, size_on_disk_bytes FROM sys.dm_io_virtual_file_stats(NULL,NULL) — NO join to sys.master_files (no paths).
- AG guard: a fixed control statement `bb:hadr` = `SELECT CAST(SERVERPROPERTY('IsHadrEnabled') AS int) AS hadr` (add to allowlist; lint-clean). The ag sampler runs bb:ag only when hadr==1; otherwise it returns nil (omitted). bb:ag reads sys.dm_hadr_database_replica_states (database_id, synchronization_state) — NEVER sys.availability_replicas (names other servers).
- Security review BL-119: database_id, file_id, deltas, TOP 256, no paths/names; AG only when IsHadrEnabled=1, no replica server names.
- Rules: **deltas in Go**; **TOP 256**; **no paths/names**; **AG gated**; **no replica server names**; **missing never 0**; **bounded state**.

## Do
1. filestatsSampler: prev map keyed (database_id,file_id); Decode → deltas (read/write stall ms, reads, writes); first sample nil; reset guarded; bound prev to 256 keys.
2. agSampler: Decode the bb:hadr guard first (the runner can run it as part of this sampler's statement step); if hadr!=1 → nil. Else decode bb:ag rows → []AG{DatabaseID, SyncState}. No server names.
3. Use StatementID; decodeRows caps; no SQL building.

## Do not
- Do NOT join sys.master_files or output physical_name/paths/database names.
- Do NOT query sys.availability_replicas or output replica server names.
- Do NOT run the AG sampler when IsHadrEnabled != 1.
- Do NOT emit 0 on first delta; do NOT let prev grow unbounded; no goroutine/ticker.

## Tests to write first (fake rows)
- `TestFileStatsDelta` — two samples → deltas keyed by (db,file); first nil; reset guarded.
- `TestFileStatsTop256Bounded` — >256 keys bounded; truncated flag.
- `TestFileStatsNoPaths` — a row with physical_name → rejected.
- `TestAGGatedOff` — hadr=0 → nil (bb:ag not decoded).
- `TestAGGatedOn` — hadr=1 → AG rows decoded, no server-name field.
- `TestAGNoReplicaNames` — a row with replica_server_name → rejected.
- `TestFileStatsRegistered` — kinds/intervals/statement ids allowlisted.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: bounded prev; no goroutine/ticker; C13 gate still passes
- [ ] No text captured — ids/deltas/enum only; no paths/replica names (tests above)
- [ ] TOP 256; deltas in Go; AG gated by IsHadrEnabled (tests above)
- [ ] Missing never 0

## Live-check needed?
YES — owner confirms file-stats deltas and (on an AG server) AG state on a real SQL Server; confirms no paths/names appear. Fake tests verify logic only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-14.md`.