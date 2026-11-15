# Wave 12 lane fix — W12-01..W12-06

Branch `feat/blackbox-expansion`, base HEAD `697a7f4`. Fixer pass for the confirmed findings in
`wave-12-adversarial-review.md`. Code + tests only; no commit/push, no service/network operation,
no upload. All work stayed inside the permitted files; every out-of-scope dependency is listed
under Conflicts.

## Per-finding changes

### W12-01 — internal SQL gaps are no longer complete zeros (High)

- `internal/blackbox/reader.go`: `ReadReport` gains optional fields `Corrupt []string`,
  `Capped bool`, `OmittedFiles int`, `Limited bool` (additive; nothing renamed/removed).
  `readFile` now distinguishes a torn tail from a malformed INTERIOR line (a bad line with good
  lines after it marks the file `Corrupt`). The newest-192 file cut sets `Capped`/`OmittedFiles`
  instead of silently truncating. New `CoverageIncomplete()` = Dropped/Missing/Newer/Torn/
  Corrupt/Capped/Limited. `Incomplete()` is deliberately unchanged: `state.go` `DeriveState` and
  the frozen `state_test.go:196` pin torn-tail → Running, so the page-state predicate keeps its
  contract and the lane uses the wider one (see Conflicts).
- `internal/server/tracing_blackbox.go`: the lane tracks accepted SQL observations
  (`bbSQLObs`) and per-instance coverage (an instance that contributed before but is silent now
  makes the sum a lower bound). Coverage envelope is the first/last SQL observation, not the host
  manifest's first/last. A bucket is complete only when known, whole observations tile its whole
  clipped span; internal SQL gaps, optional-group skips, instance loss, rejected numbers,
  restarts and any `CoverageIncomplete()` read leave the affected buckets incomplete with the
  fixed warnings `bbLaneGapWarning` / `bbLaneCoverageWarning`. A rejected number (> `bbSQLMaxCount`,
  negative, non-finite, over-cap instance) downgrades coverage; it is never an observed zero.
  Overflow bounds (`bbSQLMaxRate`/`bbSQLMaxCount`, float-before-int64) are preserved.

### W12-02 — rate integration uses observed spans and clipped support (High)

- `internal/blackbox/manifest.go`: new `Manifest.CadenceSeconds()` (effective > configured >
  default), documented as a latest value, not a history.
- `internal/server/tracing_blackbox.go`: each rate keeps its observation span (previous tick of
  the SAME run → this sample). The read includes the boundary observations (lookback of up to
  2×cadence before the window start, and the observation AT the window end). Counts are
  `rate × observed span`, apportioned by overlap into clipped buckets; a span across a restart,
  a gap beyond `LateIntervals×cadence`, or an unproven first span contributes no count and marks
  its stretch unknown. The configured `IntervalSeconds` no longer scales counts. Integrated
  counts are labelled estimates: new optional lane field `Estimates bool` (`estimates,omitempty`)
  plus the fixed `bbLaneEstimateNote` ("not query durations or concurrency").

### W12-03 — selection honoured, richer evidence preserved (High)

- `internal/server/tracing.go` (`caseTracing`): SQL chain reordered to
  **held Query Store day → qs-file → Blackbox → api-seen** (documented in `tracing_blackbox.go`
  and `tracing_apiseen.go`). `blackboxSQLLane` now receives `sel`.
- `bbLaneEntry`: an explicit `sourceIds` selection qualifies ONLY a selected blackbox `cs_` tag;
  an unselected Blackbox is never opened (proved by a counting `Open` seam in the tests). A
  default call keeps the first-tag rule.
- Query Store duration evidence (held/layered/qs-file) outranks machine-wide batch throughput and
  is never displaced. When Blackbox fills the lane, imported XE markers stay as separately
  attributed events (sources name both; `bbLaneMarkersNote`), never summed into the bars. A
  PARTIAL Blackbox never replaces an imported lane that already counted activity
  (`importedScanned > 0`). Import coverage fields (`ImportedFrom/To/Partial`) are preserved.

### W12-04 — bounded reads and admission (High)

- `internal/blackbox/reader.go`: cumulative budgets per `ReadSamples` call —
  `maxReadTotalBytes` (128 MiB) and `maxReadTotalSamples` (100 000 decoded objects); a read stops
  at limit+1 and reports `Limited` (incomplete, never clean EOF). Cancellation is checked in the
  decode loop (every 32 lines). Samples are merged/deduped incrementally per file instead of
  accumulating every file before sorting. Bounded admission: `readAdmit` (8 concurrent reads,
  global) and `osAdmit` (16 outstanding OS calls, global) plus a per-`FolderSource` pool
  (4). Helper goroutines hold their slot until the OS call returns — including calls whose caller
  gave up — so outstanding uncancellable helpers are bounded; acquisition honours ctx. Late
  handles are closed exactly once via a mutex-guarded handoff (no extra closer goroutine).
- `readSQLStatus` (internal/blackbox/server.go): the reported unbounded `os.ReadFile` does NOT
  exist at this HEAD — it is already `os.Open` + `Stat` size check + `io.LimitReader(…, 4097)`
  (W8-04). No edit needed; see Conflicts.

### W12-05 — containment / no-follow (Medium)

- `internal/blackbox/reader.go`: `DirAccess` opens (manifest AND hourly files) now validate every
  ancestor and the final object with `Lstat`, refuse non-regular objects and reparse aliases
  (Windows junctions Lstat as `ModeIrregular`, symlinks as `ModeSymlink` — both refused; verified
  against the go1.27.3 windows runtime), then verify the OPEN HANDLE against a fresh `Lstat`
  (`os.SameFile`) and re-check ancestor identities after the open. A final-component link opened
  by `os.Open` yields the target's identity ≠ the entry's, so it is caught. This is
  race-resistant, not race-free (no openat in the stdlib): the residual ancestor-swap window is
  documented in the code. Explicit trusted-root policy: `DirAccess.Trusted` (off by default,
  never set silently by the app). Refusals surface as fixed `ReadBadData` text, no paths.
- `Server.handleFile` is out of scope — see Conflicts.

### W12-06 — bounded dump scan (Medium)

- `internal/blackbox/dumps.go`: `filepath.WalkDir` replaced by batched `ReadDir(64)` on a
  directory handle (`dumpReadDir` seam). The shared 500-visit budget is checked BEFORE the next
  root is opened; a bounded min-heap keeps the newest 200 across all visited roots. Symlinks AND
  junctions/irregular entries are skipped (dirs also Lstat-checked before descent). New
  `DumpScanStatus{Visited, Truncated, Unreadable}` returned by `catalogueDumps` and exposed via
  `dumpsGroup.ScanStatus()`. Metadata-only access preserved (no dump content open). The legacy
  entries-only `scan` seam is kept so the frozen `budget_composition_test.go` still compiles.
- Wire-level status on `DumpsGroup` needs `groups_types.go` — see Conflicts.

## Coverage / interval contract (summary)

- Unreadable/missing/unobserved is never 0. A bucket is `complete:true` only when known, whole SQL
  observations tile its entire clipped span and the read is `CoverageIncomplete()==false`.
- Counts are throughput estimates (`rate × observed span`, overlap-apportioned), labelled
  `estimates:true`; never durations or concurrency; never summed with Query Store counts or XE
  markers.
- Source precedence: held QS > qs-file > Blackbox (selection-aware) > api-seen. Explicit selection
  never reads an unselected Blackbox; partial Blackbox never erases counted import evidence.
- Fixed codes/text only in public responses; JSON shapes additive (`estimates`, report fields).

## Exact commands and results

Go added to PATH: `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin`
(go1.27.3 windows/amd64). Run from the repo root.

| Command | Result |
| --- | --- |
| `go build ./...` | OK (no output) |
| `go vet ./internal/server/ ./internal/blackbox/` | OK (no output) |
| `GOOS=linux go vet ./internal/server/ ./internal/blackbox/` | OK (no output) |
| `go test ./internal/server/ -count=1 -timeout 600s` | 534.7s; ONLY `TestAsyncImportCommitsInTheBackground` failed (known load-sensitive flake). A full re-run (454.9s) failed the SAME single known flake and nothing else. Passes in isolation. |
| `go test ./internal/blackbox/ -count=1 -timeout 400s` | ok 143.7s (exit 0) |
| `go test ./internal/server/ -run "TestAsyncImportCommitsInTheBackground\|TestOwnKeyArchiveRefusesATypedPassphrase" -count=1` | ok 2.9s (both known flakes pass in isolation) |

Known flakes not chased (per instruction): `TestOwnKeyArchiveRefusesATypedPassphrase`,
`TestAsyncImportCommitsInTheBackground`. An earlier blackbox run also showed
`TestClumsyWrongReadKeyIsRefusedEveryWay` ("server never listened", a 5s real-listen timeout under
load); it passed in isolation and in the full re-run above. No other failures.

Note: during this pass a parallel agent landed disjoint out-of-scope edits in the same working
tree (`internal/bbsql/*`, `internal/blackbox/{clr,groups,groups_types,iis,proc10,procio,server}.go`
— W3 identity work). This pass did not touch, revert or commit those files; the verification
above ran against the shared tree and compiled/passed with them present. The files changed by
THIS pass are exactly: `internal/blackbox/{reader,dumps,manifest}.go` + `{reader,dumps}_test.go`,
`internal/server/{tracing,tracing_apiseen,tracing_blackbox}.go` + `tracing_blackbox_test.go`,
the new `internal/server/lanegap_test.go`, and this report.

gofmt: the repo is CRLF with no trailing newline, so `gofmt -l` flags ALL 204 server files
(including untouched ones). Verified each changed file has the SAME gofmt issue count as its
`HEAD` original (only the pre-existing trailing-newline/alignment quirks); no new formatting
deviation introduced. CRLF preserved on every existing file; the one new file
`internal/server/lanegap_test.go` is LF and gofmt-clean.

New tests: `internal/server/lanegap_test.go` (internal gap/skip, instance loss, corrupt/newer
interior lines, 193 overlapping hourly entries, 5/15/30/60s known-rate totals, cadence change,
jitter, restart, clipped minute edges, selected-import-only, full-QS+partial-Blackbox,
XE+Blackbox, XE+partial-Blackbox, explicit Blackbox selection). Added to `reader_test.go`
(coverage report, corrupt interior, 193-file cap, byte/sample budgets at limit+1, decode-loop
cancellation, bounded outstanding OS calls per-source/global, file-symlink/junction-ancestor/
junction-entry/non-regular/manifest-link refusal, trusted-root policy) and `dumps_test.go`
(batched enumeration bound, stop-before-further-roots, newest heap across roots, unreadable
status, link-directory skip). Existing `tracing_blackbox_test.go` expectations updated to the
honest contract (estimate note; rejected-number downgrades; observed-span totals).

## Conflicts that stopped an edit (out-of-scope files)

1. `internal/blackbox/server.go` `handleFile` (W12-05): still plain `os.Open` + `ServeContent`,
   follows links and serves the raw target regardless of NDJSON. NOT editable → left as-is.
   The reader-side containment is implemented; the HTTP handler needs the same no-follow policy.
2. `internal/blackbox/server.go` `readSQLStatus` (W12-04): the reported unbounded `os.ReadFile`
   is already fixed at this HEAD (bounded open/stat/LimitReader, W8-04). No edit required.
3. `internal/blackbox/groups_types.go` `DumpsGroup` (W12-06): cannot add a wire truncation/
   unreadable field. Status is exposed in-process (`catalogueDumps` return + `dumpsGroup.ScanStatus`);
   the sample wire shape is unchanged. A wire flag needs this frozen file.
4. `internal/blackbox/state.go` + `state_test.go` (W12-01): `DeriveState`/`Incomplete()` semantics
   are pinned (torn → Running). Added `CoverageIncomplete()` rather than widening `Incomplete()`.
5. `internal/server/blackbox.go` `readHost` (W12-01/04): merges only the old report fields; the
   host lane does not surface Corrupt/Capped/Limited. NOT editable → host lane unchanged (the
   finding targets the SQL lane).
6. `internal/blackbox/groups.go` `runGroup` (W12-06): the 1s group budget still measures only
   after `Collect` returns; it cannot interrupt a scan. NOT editable → the bounded batched scan
   reduces the cost, but the budget enforcement point is unchanged.

## Live-only items (not provable with fakes here)

- PDH/perfmon parity of the integrated batch estimates on a real SQL host; real cadence-change
  and restart recordings.
- Deployed NTFS link/junction ACLs and reparse behaviour under the service account; the
  residual ancestor-swap TOCTOU window on the installed runtime.
- Recorder footprint / host-tick delay of a large-directory dump scan on a loaded host
  (CPU/RSS/wall vs the 0.5%/50 MB/1s budgets).
- Real >192-file (8-day) recordings and dead-share admission under sustained load.
- Browser rendering of the new `estimates` flag and warnings (web/ untouched).
- SQL-service folder/snapshot scope promise (cross-wave item 6) — unchanged here.