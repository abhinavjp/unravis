# BBX-1-06 PDH counter source (Windows) with handle reuse
Backlog: BL-121/122 | Group: 1 | Model: performance | Reason: Windows PDH API, query-handle lifetime and wildcard re-expansion; correctness-critical.
Depends on: BBX-1-02 | Blocks: all counter groups, BBX-1-07

## Goal
Implement `CounterSource` (from engine.go) over PDH: one query handle, English counters added once, wildcards expanded once and re-expanded only when told the process set changed. Every reading carries its PDH status so the engine can do the valid-status check. Provide a non-Windows twin.

## Context
- New files `internal/blackbox/pdh_windows.go` (build tag windows) and `internal/blackbox/pdh_other.go` (build tag !windows). Test `internal/blackbox/pdh_windows_test.go` (windows-only; must NOT require real counters — test the path/spec bookkeeping with a fake or skip the live parts).
- Follow the existing lazy-DLL pattern in `internal/blackbox/extras_windows.go:26-31`: `windows.NewLazySystemDLL("pdh.dll")` and `NewProc` for `PdhOpenQueryW`, `PdhAddEnglishCounterW`, `PdhCollectQueryData`, `PdhGetFormattedCounterValue`, `PdhCloseQuery`. Reuse those declarations if exported, else re-declare locally. Add `PdhExpandWildCardPathW` for wildcard expansion.
- `pdhFmtDouble = 0x200` (PDH_FMT_DOUBLE). The formatted value struct is `{ CStatus uint32; _ pad; DoubleValue float64 }` — match the layout already used in extras_windows.go.
- `CounterSource` interface (engine.go, BBX-1-02): `Collect() ([]CounterReading, error)`, `Close() error`. `CounterReading{Path string; Value float64; Status uint32; OK bool}`. NOTE: the type is named `CounterReading` (not `Reading`) because `blackbox.Reading` already exists in reader.go — BBX-1-02 landed this rename; use `CounterReading` everywhere.
- Rules that apply: **English counter names** (PdhAddEnglishCounterW, never localized); **add specific counters once and reuse the query handle; re-expand wildcards only when the process set changes** (BL-122); **valid-status check** (return CStatus, set OK = CStatus==0); **unreadable means missing** (a counter that fails is returned with OK=false, not 0).

## Do
1. `pdh_windows.go`:
   - `type PDHSource struct` holding the query handle, a slice of added counters `{path string; handle uintptr; wildcard bool}`, and the expanded instance paths.
   - `func NewPDHSource(paths []string) (*PDHSource, error)` — PdhOpenQueryW; for each path: if it contains `*` or `(*)`, PdhExpandWildCardPathW to instance paths and PdhAddEnglishCounterW each expanded path; else PdhAddEnglishCounterW the path directly. Do ONE PdhCollectQueryData to prime. Return error (and close the query) if open/add fails.
   - `func (p *PDHSource) Collect() ([]CounterReading, error)` — PdhCollectQueryData once; for each counter handle PdhGetFormattedCounterValue(PDH_FMT_DOUBLE) → CounterReading{Path: counterPath, Value: double, Status: CStatus, OK: CStatus==0}. A per-counter failure yields OK=false (do not abort the whole collect). Return the slice.
   - `func (p *PDHSource) RefreshInstances() error` — re-expand ONLY the wildcard counters (remove their old handles via PdhRemoveCounter if available, else rebuild the query), re-add expanded paths. Called by the resolver wiring only when the process set changes — NOT every tick.
   - `func (p *PDHSource) Close() error` — PdhCloseQuery once; idempotent.
   - `func (p *PDHSource) Paths() []string` — current expanded paths (for groups to build engine specs).
2. `pdh_other.go` (!windows): `NewPDHSource` returns `nil, errors.New("PDH counters are Windows only")`; a stub type with the same methods returning that error so callers compile cross-platform.
3. No goroutines, no tickers. All syscalls happen inside Collect/Refresh/Close called from the tick loop.

## Do not
- Do not use localized counter names (PdhAddCounterW) — English only (PdhAddEnglishCounterW).
- Do not re-open the query or re-add counters every tick (handle reuse, BL-122).
- Do not re-expand wildcards every tick — only RefreshInstances does that.
- Do not return Value=0 with OK=true for a failed counter; set OK=false.
- Do not pause/attach to any process; PDH only reads counters.

## Tests to write first
- `pdh_windows_test.go`: `TestPDHSourceOtherTwin` is covered by cross-platform vet. For windows, test bookkeeping without live counters: `TestNewPDHSourceBadPathReturnsError` (a clearly invalid path returns an error and does not leak a query — assert Close is safe). Mark any test that needs real counters with `t.Skip("needs live PDH; owner runs on a real server")` and list it as a live check.
- A pure bookkeeping test: construct the counter slice via a small internal helper and assert wildcard vs direct classification (factor the path-classification into a testable func `isWildcard(path string) bool`).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes (live-counter tests skipped, not failed)
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: one query handle reused; no per-tick re-add; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (failed counter → OK=false; engine omits)
- [ ] No text captured (counter paths are fixed identifiers)
- [ ] English names only (PdhAddEnglishCounterW)
- [ ] Wildcards re-expand only via RefreshInstances (code review + test)

## Live-check needed?
YES — on a real Windows server: build and run a tiny collect of `\Processor(_Total)\% Processor Time` twice and confirm a non-zero rate with CStatus 0. Exact group paths are verified per group packet with `typeperf -q`. This packet's API usage is standard PDH; the owner confirms on real hardware.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-06.md`.