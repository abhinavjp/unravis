# Wave 2 PDH fixes — W2-02, W2-03, W2-04

Branch `feat/blackbox-expansion`. Scope limited to the five owned files:
`internal/blackbox/pdh_windows.go`, `pdh_other.go` (unchanged, still compiles),
`extras_windows.go`, `pdh_windows_test.go`, and the new
`internal/blackbox/pdh_bounds_test.go`. No other file was edited. No commit,
no push, no live service operation, no upload.

Pre-existing state preserved: `engine.go` `CounterStatusOK` (accepts
VALID_DATA=0 and NEW_DATA=1) and `readingFrom` are unchanged; `Engine.Reconcile`
and `CounterSpec.Volatile` are untouched; `RefreshInstances` still expands every
pattern BEFORE touching the query, keeps unchanged handles, and primes only when
something changed.

## W2-02 — NOCAP100 at the formatting boundary (High)

`Collect` now passes `pdhFmtDouble|pdhFmtNoCap100` to the formatted-value proc
([pdh_windows.go:422](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L422)).
The constant is declared beside `pdhFmtDouble` in
[extras_windows.go:40](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/extras_windows.go#L40).

**Value correction.** The task text suggested `0x1000`. That is wrong: `0x1000`
is `PDH_FMT_NOSCALE`. The correct `PDH_FMT_NOCAP100` is **`0x00008000`**, verified
against the installed Windows SDK header
`C:/Program Files (x86)/Windows Kits/10/Include/10.0.22621.0/um/pdh.h`
(`#define PDH_FMT_NOCAP100 ((DWORD) 0x00008000)`). The Microsoft
`PdhAddEnglishCounterW` page was fetched read-only to confirm the W2-03
procedure; the `format-constants` URL 404'd, so the header is the authority for
the bit value. `TestCollectFormatsWithNoCap100` pins the constant to `0x8000`
and asserts the exact combined bits `0x8200` reach the native call.

**Seam.** A package-level `pdhFormatCall` var (default wired to the real
`procPdhGetFormattedCtrValue`) lets a fake record the flags. `Collect` calls it;
production never reassigns it. The same seam pattern covers collect, remove,
add-English, add-localized, get-counter-info and expand.

## W2-04 — named bounds before allocation / addition (High)

Constants in [pdh_windows.go:30-47](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L30):

| Constant | Value | Reasoning |
|---|---|---|
| `pdhMaxExpandChars` | `512*1024` UTF-16 units (1 MiB) | A Process path is ~60 chars; covers ~8000 instances/pattern, ~10x a real host. Rejects a hostile provider size before `make`. |
| `pdhMaxInstancesPerPattern` | `4096` | Caps retained handles from one pattern; thousands of processes with margin. |
| `pdhMaxCountersPerSource` | `8192` | Whole query (direct + expanded). proc10+procio add ~11 counters/process, so ~700 processes fit; keeps native state well inside the 50 MiB budget. |
| `pdhMaxCounterInfoBytes` | `64*1024` | Bounds the `PdhGetCounterInfoW` buffer (a full path is a few KiB). |

Chosen to comfortably cover a large real host (hundreds of processes x ~10
counters ≈ 5500) with headroom, while giving a deterministic over-cap outcome.
Fixed sentinel errors (`errPDHExpandTooLarge`, `errPDHTooManyInstances`,
`errPDHTooManyCounters`, `errPDHCounterInfoTooLarge`) are returned instead of
allocating. Enforcement points:

- `expandWildCard`: size checked before `make([]uint16, n)`; instance count
  checked during the multi-string walk before appending.
- `pdhLocalizedPath`: info size checked before allocation; the returned
  `szFullPath` pointer is validated to lie inside the buffer and NUL-terminated.
- `NewPDHSource`: `roomFor` before each add; closes the query on over-cap.
- `RefreshInstances`: `direct+len(order)` checked BEFORE any removal, so an
  over-cap refresh cannot strip working counters.

Tests (fake expansion/info seams): oversized size rejected before allocation
(exactly one native call); over-cap instance count rejected and at-cap accepted;
oversized counter-info rejected; total cap enforced in both constructor and
refresh; partial-refresh failure and over-cap refresh leave previously working
counters (paths + handles) intact and never remove/add/collect; incremental
refresh keeps unchanged handles, removes the departed, adds the new, primes once.

## W2-03 — documented localization sequence, English identity kept (Medium)

`expandLocalized` ([pdh_windows.go:262](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L262))
implements the exact Microsoft procedure for `PdhAddEnglishCounterW` wildcards:

1. `PdhAddEnglishCounterW` with the English wildcard (temporary handle).
2. `PdhGetCounterInfoW` on that handle to read the localized full path
   (`szFullPath`, still containing the wildcard) — two-call size pattern.
3. `PdhRemoveCounter` on the temporary translation handle.
4. `PdhExpandWildCardPathW` on the **localized** pattern.
5. `PdhAddCounterW` on each resulting **localized** path.

`PDH_COUNTER_INFO_W` is mirrored in Go (`pdhCounterInfo`) with the union sized to
its largest member (`max(sizeof(CounterPath), sizeof(DataItemPath))`) so offsets
hold on 386 and amd64; layout verified against the same SDK header. Only
`szFullPath` is read.

**English-identity guarantee.** `pdhCounter.path` stays the ENGLISH logical path.
`englishPathFor` ([pdh_windows.go:348](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L348))
rebuilds it by taking object + counter from the original English pattern and the
instance from the localized expansion. Instance names are data (process image
names, disk numbers, pool names), not translated text, and PDH path structure
(`\`, `(`, `)\`) is locale-invariant, so the result equals what an English
expansion produces. `Paths()` and `Collect()` therefore keep returning English
paths; the group English-prefix parsers and engine specs are unaffected. Only
the native handle is localized. **No file outside the owned set needed editing**
— the constraint was satisfiable within `pdh_windows.go`, so there is no conflict
to report. `TestWildcardLocalizationCallOrder` proves the exact call order
(addEnglish → getInfo×2 → removeTemp → expand(localized)×2 → addLocalized×2 →
collect) and that `Paths()`/`Collect()` still yield English identities;
`TestEnglishPathFor` covers partial wildcards and parentheses in instance names.

## Exact commands and results

Go added to PATH from `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin`
(`go version go1.27.3 windows/amd64`).

- `go build ./...` → **BUILD-OK** (no output).
- `go vet ./internal/blackbox/` → **VET-WIN-OK**.
- `GOOS=linux go vet ./internal/blackbox/` → **VET-LINUX-OK**.
- `go test ./internal/blackbox/ -count=1 -timeout 400s` → **ok ... 240.085s**
  (full package green; an earlier run was 102.377s — variance is machine load).

gofmt applied to all four touched Go files; `gofmt -l` reports none. Pre-existing
files kept CRLF (`pdh_windows.go`, `extras_windows.go`, `pdh_windows_test.go`,
`pdh_other.go`); the new `pdh_bounds_test.go` is LF. Both build tags compile.

**Pre-existing assertions updated:** none. No existing test encoded the old
capped/English-only behaviour; all prior PDH tests pass unchanged.

**Transient concurrent-worker breakage (not mine, not edited):** during
verification another worker's in-flight edits to `sqlhost.go` / `dumps.go` /
`budget_composition_test.go` momentarily broke `go vet`
(`undefined: errSQLHostUnavailable`, then a `DumpScanStatus` signature mismatch).
Per instructions I re-ran rather than touching their files; once their edits
settled, vet and the full package returned green. A `TestClumsy...` "server never
listened" flake appeared under full-suite load and passed in isolation and on the
final full run; it is unrelated to PDH.

## Conflicts

None. The English-identity constraint was met without editing any non-owned file.

## Live-only open items (NOT claimed verified)

- **Non-English Windows image:** the localization sequence is proven only through
  fake seams. Collection on a real localized server is an **Unverified
  Assumption** until the owner runs it on one supported non-English image.
- **Multi-core >100%:** `TestCollectFormatsWithNoCap100` proves the flag bits and
  that a 350 value passes the formatter seam, but a live selected process above
  100% compared against an independent measurement is still owner-authorized live
  work.
- **Footprint/handle caps on a live host:** the byte/instance/handle ceilings are
  enforced and unit-tested; measuring working set <50 MiB and native handle count
  at maximum real cardinality remains a live check.
- The two existing live PDH tests stay `t.Skip`ped (owner runs on a real server).