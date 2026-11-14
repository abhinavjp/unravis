# BBX-1-05 Group registry and budgeted group collector
Backlog: BL-122/121 | Group: 1 | Model: performance | Reason: wiring logic that enforces the per-group budget and keeps missing≠0; not mechanical.
Depends on: BBX-1-01, BBX-1-02, BBX-1-04 | Blocks: BBX-1-07, BBX-1-09, all counter groups

## Goal
Add a group registry (each counter group registers a factory from its own file's `init()`) and a `groupCollector` that wraps the existing host Collector, runs each registered group under the budget, and writes results into the new Sample fields. A skipped/slow/failed group is recorded in `Missing`, never zero-filled.

## Context
- New file `internal/blackbox/groups.go`, test `internal/blackbox/groups_test.go`. Package `blackbox`.
- Uses: `Budget` (BBX-1-01, `budget.go`), `Engine`/`CounterSource`/`ResolvedTarget` (engine.go BBX-1-02; ResolvedTarget is defined by the resolver BBX-1-08 — for this packet define a minimal placeholder ONLY if needed; prefer `Targets func() []ResolvedTarget` and add `type ResolvedTarget struct{ Key string; PID uint32; Start int64; Runtime uint8; Pool string }` here if the resolver packet has not landed — coordinate: the resolver packet BBX-1-08 owns the final shape; to avoid a clash, this packet defines `ResolvedTarget` and BBX-1-08 reuses it).
- Uses: `Collector` interface (`sample.go:70-73`: `Collect(now time.Time) (Sample, error)`, `Close() error`), `Sample` + group fields (BBX-1-04).
- Rules that apply: **each counter group within 1 s else skipped and marked "skipped: slow"**; **unreadable means missing, never 0**; **no text**.
- Existing host collector stays unchanged (`HostCollector`, collector.go). This wraps it.

## Do
1. In `groups.go` define:
   ```go
   type ResolvedTarget struct {
       Key     string // "pool:<name>" or "svc:<name>"
       PID     uint32
       Start   int64  // unix creation time, part of the identity
       Runtime uint8  // 0 unknown, 1 .NET Framework (clr.dll), 2 .NET 10 (coreclr/hostfxr)
       Pool    string // app pool name when Kind=appPool
   }
   type GroupDeps struct {
       Source  CounterSource
       Engine  *Engine
       Targets func() []ResolvedTarget // nil when no targets selected
       Config  Config
       Cores   int
       ProcessSetChanged func() bool // nil until resolver wired; wildcard groups re-expand only when this returns true
   }
   type Group interface {
       Name() string
       Optional() bool
       Collect(now time.Time, dt float64) (any, error)
   }
   type GroupFactory func(GroupDeps) (Group, error)
   ```
2. Registry: a package-level `map[string]registration` (name → {optional bool, factory GroupFactory}) guarded by a mutex; `func RegisterGroup(name string, optional bool, f GroupFactory)` (called from each group file's `init()`); `func BuildGroups(d GroupDeps) []Group` instantiates all registered factories (skip ones whose factory errors, record nothing). Provide `func registeredNames() []string` for tests.
3. `func applyGroup(s *Sample, name string, v any)` — a type switch assigning the concrete group pointer to the matching Sample field: "http"→s.HTTP, "iis"→s.IIS, "diskCpu"→s.DiskCPU, "sqlHost"→s.SQLHost, "clr"→s.CLR, "proc10"→s.Proc10, "procIO"→s.ProcIO, "events"→s.Events, "tcp"→s.TCP, "aspnet"→s.ASPNET, "memPressure"→s.MemPressure, "vm"→s.VM, "dumps"→s.Dumps. Unknown name or wrong type → ignore (no panic).
4. `type groupCollector struct { inner Collector; groups []Group; budget *Budget }`. `func NewGroupCollector(inner Collector, groups []Group, b *Budget) Collector`.
   - `Collect(now)`: call inner.Collect → base Sample (propagate its error). Compute dt from the previous collect time (store last time; first dt=0). For each group: if `budget.GroupSkipped(g.Name(), g.Optional())` → append `g.Name()` (or `"skipped: slow"` when in SlowGroups) to `s.Missing`; if it was skipped because it is slow, call `budget.NoteSkipped(g.Name())` so the group is re-probed next cycle (consumes the one-cycle slow mark); continue. Else time `g.Collect(now, dt)` with `time.Now()`/monotonic; call `budget.GroupTiming(name, dur, g.Optional())`; on error append name to Missing; on success `applyGroup(&s, name, result)`. Return s.
   - `Close()`: close inner; groups that implement `io.Closer` get closed.
5. Deduplicate Missing entries (a name appears once).

## Do not
- Do not run groups in parallel goroutines (budget is single-tick; keep serial, no new goroutine).
- Do not zero-fill a group that failed or was skipped — leave the field nil and add to Missing.
- Do not let one group's panic crash the tick: recover per group, mark Missing (a panic is a failure, not a crash of the service).
- Do not capture text in Missing beyond fixed group names / "skipped: slow".

## Tests to write first (fakes)
- `TestRegistryBuildsAndNames` — register two fake factories, BuildGroups returns both, registeredNames sorted.
- `TestGroupCollectorAppliesResult` — fake group returns &TCPGroup{...}; Collect → s.TCP set.
- `TestGroupCollectorSkippedOptional` — budget.SkipOptional true → optional group's field nil, name in Missing, mandatory group still runs.
- `TestGroupCollectorSlowGroupMarked` — budget marks a group slow → Missing contains "skipped: slow" (or name), field nil.
- `TestGroupCollectorErrorMissingNotZero` — group returns error → field nil, name in Missing.
- `TestGroupCollectorPanicRecovered` — group panics → tick survives, name in Missing.
- `TestGroupCollectorMissingDeduped` — same name not duplicated.
- `TestApplyGroupUnknownIgnored` — unknown name does not panic.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: no new goroutine or ticker; groups run serially in the existing tick
- [ ] Missing reading is omitted, never 0 (TestGroupCollectorErrorMissingNotZero, TestGroupCollectorSkippedOptional)
- [ ] No text captured (Missing holds fixed names only)
- [ ] Per-group 1 s budget enforced via Budget.GroupTiming/GroupSkipped (tests above)
- [ ] Existing collector/recorder tests still pass

## Live-check needed?
No. Logic with fakes. Real per-group timing is measured by BBX-1-09 and on the owner's server.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-05.md`.