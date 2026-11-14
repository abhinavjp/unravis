# BBX-1-08 Resolver: process inventory, (PID, creation) keying, runtime detection
Backlog: BL-105 | Group: 1 | Model: performance | Reason: process access + module enumeration + change detection; correctness-critical for targeting.
Depends on: BBX-1-03, BBX-1-05 | Blocks: BBX-1-11, group 3

## Goal
Build the process-inventory half of the resolver: enumerate processes keyed on (PID, creation time), detect each process's runtime by loaded modules (clr.dll = .NET Framework, coreclr.dll/hostfxr.dll = .NET 10), find out-of-process .NET 10 children of w3wp, and re-detect only when the process set changes. Name→PID mapping (pools/services) is the next packet (BBX-1-11).

## Context
- New files `internal/blackbox/resolve_proc_windows.go` (windows) + `internal/blackbox/resolve_other.go` (!windows). Test `internal/blackbox/resolve_test.go` (pure helpers, no Windows needed).
- Reuse the enumeration pattern from `internal/sampler/source_windows.go:137-189`: `CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0)` + Process32First/Next (gives PID, parent PID, image name); `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` + `GetProcessTimes` for creation time. For modules use `CreateToolhelp32Snapshot(TH32CS_SNAPMODULE, pid)` + Module32First/Next (needs SYSTEM; the Blackbox runs as SYSTEM).
- `ResolvedTarget` is defined in groups.go (BBX-1-05): `{Key, PID, Start, Runtime, Pool}`. Runtime: 0 unknown, 1 Framework, 2 .NET 10.
- Rules that apply: **key processes on (PID, creation time)** so recycles/PID reuse/w3wp#N shifts are safe; **detect runtime by loaded modules, not config**; **handle in-process and out-of-process .NET 10**; **never pause/restart/attach/dump a target** (module snapshot is a read-only enumeration, not an attach); **re-check only when the process set changes**.

## Do
1. Pure helpers (in resolve_proc_windows.go but OS-independent, tested directly):
   - `type ProcInfo struct { PID, ParentPID uint32; Start int64; Name string; Modules []string }`.
   - `func classifyRuntime(modules []string) uint8` — case-insensitive base-name match: `clr.dll` → 1; `coreclr.dll` or `hostfxr.dll` → 2; both present → prefer 2; neither → 0.
   - `type procKey struct { pid uint32; start int64 }`; `func processSetChanged(prev, cur []procKey) bool` — true if the set of keys differs (order-independent).
   - `func dotnetChildren(procs []ProcInfo, parentPID uint32) []ProcInfo` — children whose runtime==2 (out-of-process .NET 10 under a w3wp parent).
2. OS layer (windows):
   - `func snapshotProcesses() ([]ProcInfo, error)` — toolhelp process snapshot; for each, OpenProcess+GetProcessTimes for Start; skip entries that fail to open (keep PID+Name, Start=0, mark unknown). Do NOT read modules here for every process every tick.
   - `func loadModules(pid uint32) ([]string, error)` — TH32CS_SNAPMODULE; return base names; on access denied return error (caller treats runtime as unknown, never guesses).
   - `type Resolver struct { last []procKey; cached map[procKey]uint8 }`; `func (r *Resolver) Inventory() ([]ProcInfo, error)` — snapshot; if processSetChanged, refresh; detect runtime (loadModules) only for keys not in cache or whose start changed; reuse cached runtime otherwise. Return ProcInfo with Runtime filled via classifyRuntime.
3. `resolve_other.go`: `snapshotProcesses`/`loadModules` return `errors.New("process resolution is Windows only")`; Resolver methods compile and return that error; pure helpers live in a shared file `resolve.go` (no build tag) so they test cross-platform. (Put classifyRuntime/processSetChanged/dotnetChildren/procKey/ProcInfo in `resolve.go`.)

## Do not
- Do not open processes with PROCESS_VM_READ beyond what module enumeration needs; never write/allocate in a target, never suspend, never dump.
- Do not detect runtime from config or process name alone — modules only.
- Do not re-enumerate modules for unchanged (PID, start) keys every tick.
- Do not treat a PID alone as identity (PID reuse) — always (PID, start).
- Do not store command lines or environment variables.

## Tests to write first (pure, no Windows)
- `TestClassifyRuntime` — clr.dll→1; coreclr.dll→2; hostfxr.dll→2; both→2; none→0; case-insensitive; full paths reduced to base name.
- `TestProcessSetChanged` — same keys different order → false; added/removed/recycled (same PID new start) → true.
- `TestDotnetChildren` — picks runtime==2 children of the given parent, ignores others.
- `TestResolverCacheReuse` — using a fake module loader (inject `loadModulesFn`), unchanged keys do not re-load modules; a recycled PID (new start) does.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: module load only on process-set change; no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (access-denied → runtime unknown 0, not guessed)
- [ ] No text captured (module base names are runtime identifiers; no command lines/env)
- [ ] Identity is (PID, creation time) (TestProcessSetChanged, TestResolverCacheReuse)
- [ ] Both runtimes detected by modules (TestClassifyRuntime)
- [ ] No attach/suspend/dump of any target (code review: only snapshot + query reads)

## Live-check needed?
YES — owner confirms on a real server: a .NET Framework w3wp shows clr.dll (runtime 1) and a .NET 10 pool shows coreclr/hostfxr (runtime 2), for both in-process and out-of-process hosting. `tasklist /m clr.dll` and `tasklist /m coreclr.dll` cross-check.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-08.md`.

## Fix round 1 (batch review finding — MUST apply)
LOW — identity degenerates to PID-alone when creation time is unreadable. Unopenable processes are keyed (pid, 0); the cache then reuses that key, so a recycled PID that is also unopenable inherits a stale runtime (e.g. Framework counters aimed at a .NET 10 target). FIX: do NOT cache runtime for keys with Start==0 — re-read modules for Start==0 keys on every Inventory (treat them as always-changed). Keep caching for keys with a real Start. Add a test `TestResolverNoCacheWhenStartZero`: an unopenable process (Start 0) re-loads modules each Inventory even when the PID is unchanged, so a runtime change is picked up. Re-run the full checklist and append the fix output to the report. Do NOT touch other files; do NOT git commit.