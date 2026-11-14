# Wave 5/12 residual fix — same-second identity, bounded SQL status, no-follow file serving

Scope: `internal/blackbox/groups.go`, `groups_types.go`, `clr.go`, `proc10.go`,
`procio.go`, `iis.go`, `server.go` and one new test file
`identity_residual_test.go`. No other file was edited. No commit/push. No live
service/process operation, no ACL change, no upload. Both build tags compile.
Pre-existing files keep CRLF; the new test file is LF. All touched files are
gofmt-clean (modulo CRLF).

## 1. Same-second PID reuse at the join

**Changes**

- `groups.go`: `ResolvedTarget` gains `StartTicks int64` (additive; raw FILETIME
  creation ticks, mirroring `ProcInfo.StartTicks`). `Start` stays unix seconds
  for display/wire compatibility. `ResolvedTarget` is an internal resolution
  type with no JSON tags and is never serialized to recordings, so the wire
  shape is unchanged.
- `procio.go`: new shared join helpers.
  - `targetHasCreation(t)`: ticks or seconds present; neither never joins.
  - `targetCreationConfirmed(creationTicks, t)`: when `t.StartTicks != 0` the
    PDH creation must match those ticks within `pdhCreationSlack` (16 ticks,
    the float64 formatting bound) — **no second window**; two same-PID
    lifetimes inside one unix second are distinct and only the matching one
    confirms. When `StartTicks == 0` it falls back to the existing
    `creationConfirmed` second window (seconds-only views keep working).
- `clr.go`, `proc10.go`, `procio.go`: the per-target joins now call
  `targetCreationConfirmed` / `targetHasCreation` instead of the second-window
  slack. Captured-once-before-Collect views, ambiguity fail-closed, and
  selected-only engine filtering are unchanged.
- `iis.go`: `selectedPoolsFrom` accepts ticks-or-seconds; the in-process
  `acceptWorker` self-check uses `targetCreationConfirmed`; each emitted
  `IISWorker` is stamped with its **own verified** creation
  (`pidCreation[pid]` from the same atomic query) when readable.
- `groups_types.go`: `IISWorker` gains `StartTicks int64
  json:"startTicks,omitempty"` (additive, optional, no rename). A recycled
  worker PID is distinguishable downstream even when two lifetimes share a
  unix second. Old rows decode to 0; zero is omitted on the wire.

**Identity contract**

- Reading side: PDH `\Process(*)\Creation Time` (FILETIME float64) — lossless
  up to float64 formatting (±16 ticks at the current epoch).
- Target side: `ResolvedTarget.StartTicks` when present is THE identity; the
  join is `|creation − StartTicks| ≤ 16` ticks. Two lifetimes milliseconds
  apart (≥ 10^4 ticks) can never conflate. Sub-16-tick (1.6 µs) separation is
  below float64 resolution and remains indistinguishable — a PDH formatting
  limit, not a window.
- `StartTicks == 0` (view without lossless identity): fallback to the
  `[Start·1e7+epoch, (Start+1)·1e7+epoch)` window ± 16 ticks — the pre-existing
  seconds-only behaviour, which cannot disambiguate same-second reuse.
- Unreadable creation (no ticks, no seconds) never confirms; missing stays
  missing, never 0.

**Seam that could not be reached (forbidden file)**

The shared per-tick view does **not** yet carry `StartTicks`. The resolver
confirms identities losslessly internally (`identityTicks`, `instBinding`,
`svcProc.start`), but the three `ResolvedTarget` constructors drop the ticks:

- `resolve_map.go:435` (`matchPool`, out-of-process child):
  `ResolvedTarget{Key: "pool:"+name, PID: c.PID, Start: c.Start, ...}`
- `resolve_map.go:443` (`matchPool`, worker):
  `ResolvedTarget{Key: "pool:"+name, PID: w.PID, Start: w.Start, ...}`
- `resolve_map.go:461` (`matchService`):
  `ResolvedTarget{Key: "svc:"+name, PID: p.PID, Start: p.Start, ...}`

Exact fix needed there: add `StartTicks: identityTicks(c)` / `identityTicks(w)`
/ `identityTicks(p)` to those three literals (and, for completeness, compare
`StartTicks` in `targetsChanged`, `resolve_map.go:372`; generation correctness
already holds because `keysOf`/`processSetChanged` use ticks). `resolve*.go`
and `host.go` are out of scope, so no group-side code can repopulate the lost
ticks — the information is dropped at those literals. Until that seam is
closed, production joins use the seconds fallback; the lossless join is fully
wired and proven by tests that supply `StartTicks` through the existing
targets-func fake seam.

**Tests** (`identity_residual_test.go`, fake sources only)

- `TestTargetCreationConfirmedLossless`: exact/slack/beyond-slack, wrong
  same-second lifetime rejected although it sits inside the old seconds
  window, seconds-only fallback preserved, no-creation never confirms,
  ticks-only identity usable.
- `TestProcIOSameSecondPIDReuseNotConflated`,
  `TestProc10SameSecondPIDReuseNotConflated`,
  `TestCLRSameSecondPIDReuseNotConflated`,
  `TestIISSameSecondWorkerReuseNotConflated`: same PID, same unix second,
  lifetimes 0.5 s apart — only the exact lifetime joins, in all four groups.
- `TestIISWorkerCreationDistinguishesRecycledPID`: PID 100 recycled within one
  second emits distinct `startTicks` per generation; a stale captured identity
  against the recycled worker joins nothing.
- `TestIISWorkerCreationIsTheWorkersOwn`: out-of-process row carries the
  parent worker's creation, never the child's.
- `TestIISWorkerCreationOmittedWhenUnreadable`,
  `TestIISWorkerWireBackwardCompatible`: zero omitted; old JSON decodes
  unchanged; round trip preserves the stamp.

## 2. W12-04 residual — readSQLStatus bounded

**Finding on arrival:** the unbounded `os.ReadFile` described in the residual
was already replaced (W8-04, commit `2d156d3`): `os.Open` → `f.Stat()` size
check **before** any allocation → `io.ReadAll(io.LimitReader(f,
sqlStatusMaxBytes+1))` (4096-byte cap). The public C11 field set is exactly
`{enabled,state,lastOK,failures}`.

**Change:** hardened the Stat gate with `!fi.Mode().IsRegular()` so a
non-regular object (directory/device/pipe named `status.json`) is refused
before any read, not discovered mid-read. Oversized/unreadable still returns
the whole fixed off object; no error text, no partial record.

**Tests:** `TestSQLStatusHugeFileOff` (256 MiB file created via `Truncate`,
fresh heartbeat → whole off object via HTTP and the direct seam; refusal is
from Stat alone, before the body is allocated/read), `TestSQLStatusSizeBoundary`
(exactly 4096 bytes of valid JSON served; 4097 wholly off),
`TestSQLStatusNonRegularOff` (directory → off). Existing W8-04 tests
(stale/future/strict-document/oversized) unchanged and green.

## 3. W12-05 residual — handleFile containment / no-follow

**Change** (`server.go`): `handleFile` keeps the basename/shape validation and
the read-key guard untouched, but opens through `openRecordingFile(dir, name)`:

1. `os.Lstat(dir)` — the root must be a real directory; a symlink/junction root
   (Go 1.23+ reports junctions `ModeIrregular`, symlinks `ModeSymlink`) is
   refused before pinning (`os.OpenRoot` would follow links in the root name).
2. `os.OpenRoot(dir)` pins the directory **handle**; substituting an ancestor
   of the folder afterwards cannot redirect the open.
3. `root.Open(name)` validates every component relative to that handle. On
   Windows this is `NtCreateFile` with `OBJ_DONT_REPARSE` (Go 1.27.3
   `internal/syscall/windows.Openat`, with a handle-inspecting
   `FILE_OPEN_REPARSE_POINT` fallback on builds without `OBJ_DONT_REPARSE`):
   **any** symlink/junction/mount-point alias — final object or ancestor — is
   refused outright, inside the same handle-relative open (no TOCTOU gap
   between check and open). On Unix no component may escape the root.
4. `f.Stat()` on the **opened handle** (never the path) must be a regular
   file; directories/devices/pipes with a valid basename are refused.

All refusals return the fixed 404 `"no such recording file"` (no path, no
provider text); a Stat failure on an already-open regular file keeps the
existing fixed 500. `ServeContent` range behaviour is unchanged.

**Known limits (reported, not weakened):** NTFS hardlinks are regular files
and indistinguishable by any handle policy — a hardlink to a same-volume
private file would still be served (same precondition as W12-05: write access
to the recordings folder). On Unix an in-root symlink to an in-root regular
file is followed (containment holds); Windows refuses all reparse aliases.
The Lstat root check is path-based: an attacker who can swap the root's parent
entry in the window between Lstat and OpenRoot still needs write access above
the recordings folder; the pinned handle closes every window after the open.
`FolderSource.OpenFile`/`DirAccess.Open` (reader.go) have the same lexical-only
weakness but are a forbidden file — seam reported, not edited.

**Tests** (temp dirs owned by the test; junctions via `mklink /J` — no
privilege needed; symlinks with graceful skip when not permitted; no real
private data touched):

- `TestHandleFileRefusesLinkEntry`: valid basename → private file symlink:
  401 without key (guard first), 404 fixed message with key, no private bytes,
  target untouched.
- `TestHandleFileRefusesJunctionEntry`: valid basename → junction to a private
  folder: 404, no private bytes.
- `TestHandleFileRefusesNonRegularEntry`: real directory with a valid
  basename: 404, no path in the body.
- `TestOpenRecordingFileRefusesAncestorSubstitution`: junction/symlink
  ancestor inside the root, and a link leaf under a real ancestor: refused.
- `TestOpenRecordingFileRefusesLinkRoot`: the root itself a junction/symlink:
  fixed refusal.
- `TestOpenRecordingFileServesRegular`, `TestHandleFileStillServesRegularRecording`:
  positive controls (no over-refusal; end-to-end download intact).

## Exact commands and results

Go: `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin/go.exe`
(go1.27.3 windows/amd64), on PATH; `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`.
Run from the repo root on the live working tree (other workers' concurrent
edits to forbidden files were present and compiling throughout):

- `go build ./...` → exit 0 (re-checked after all edits: exit 0).
- `go vet ./internal/blackbox/` → exit 0.
- `GOOS=linux go vet ./internal/blackbox/` → exit 0.
- `go test ./internal/blackbox/ -count=1 -timeout 500s` → **PASS,
  `ok api-audit-explorer/internal/blackbox 133.073s`** (full package,
  including all wave-5 identity tests and the new residual tests; no flake
  re-run was needed).
- New tests alone (`-run` list of the 19 residual tests) → PASS, 3.5 s.
- `gofmt -l` on the eight touched files → clean modulo CRLF (verified by
  byte-comparing gofmt output with CRLF normalized); existing files keep
  CRLF, `identity_residual_test.go` is LF.

## Live-only items (still need a real server)

- Production same-second disambiguation end to end: requires the
  `resolve_map.go` seam above; until then the live join is seconds-only.
- `Process\Creation Time` FILETIME semantics/rounding on the deployed PDH,
  English vs non-English paths; CLR/W3SVC_W3WP instance layout; web-garden
  overlap; out-of-process parent creation cannot be cross-checked against the
  view (the view carries the child's identity) — the worker's own stamp now
  rides the wire instead.
- `OBJ_DONT_REPARSE` behaviour on the deployed Windows build (the Go fallback
  path for pre-10240-style builds), NTFS junction/symlink ACLs in the installed
  recordings folder, and hardlink exposure under the installed ACL.
- Footprint of the containment open (one extra directory handle per file
  request) and of the whole resolver+groups pass against the <0.5% core /
  <50 MB / 1 s budgets at a stated host population.