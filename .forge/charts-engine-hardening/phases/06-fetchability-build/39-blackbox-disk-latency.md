# TSK-39 blackbox-disk-latency

risk: medium — Windows PDH calls through `syscall`; a wrong counter handle or an extra collect corrupts the disk-queue reading that exists today.

## Outcome

The Blackbox sampler records disk latency `diskMs` (`\PhysicalDisk(_Total)\Avg. Disk sec/Transfer` × 1000) next to the disk queue, from the same PDH query and the same collect. A failure leaves it out and names `diskMs` in `Missing`.

## Write scope

- `internal/blackbox/collector.go`
- `internal/blackbox/extras_windows.go`
- `internal/blackbox/sample.go`
- `internal/blackbox/collector_test.go`
- any other file in `internal/blackbox/` that implements `Extras` (grep `) DiskQueue() (` first; today only the three above)

## Must not change

- `diskQueuePath`, the order of calls in `DiskQueue()`, `Network()`.
- The JSON name of every existing `Sample` field.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `collector.go` `Extras` (~14–18): add `DiskLatencyMs() (float64, error)` with the comment "average seconds per transfer of all disks, in ms, from the same collect as DiskQueue; call it right after DiskQueue". `NoExtras` (~21–25): add `func (NoExtras) DiskLatencyMs() (float64, error) { return 0, errors.New("not available") }`.
2. `extras_windows.go` const block (~34–40): add ``diskLatencyPath = `\PhysicalDisk(_Total)\Avg. Disk sec/Transfer` ``.
3. `winExtras` (~49–53): add `latency uintptr` and `latencyOK bool` and `lastCollect error`.
4. `newWinExtras` (~55–74): after adding the queue counter (~66–70) and before `w.opened = true`, add the latency counter on the **same** query:
   ```go
   lp, _ := windows.UTF16PtrFromString(diskLatencyPath)
   if r, _, _ := procPdhAddEnglishCounter.Call(w.query, uintptr(unsafe.Pointer(lp)), 0, uintptr(unsafe.Pointer(&w.latency))); r == 0 {
       w.latencyOK = true
   }
   ```
   A failure here does not fail the queue counter.
5. `DiskQueue` (~76–91): store the collect result: replace the collect `if` with
   ```go
   r, _, _ := procPdhCollectQueryData.Call(w.query)
   if r != 0 {
       w.lastCollect = fmt.Errorf("PdhCollectQueryData: 0x%x", r)
       return 0, w.lastCollect
   }
   w.lastCollect = nil
   ```
   The rest is unchanged.
6. Add `DiskLatencyMs`:
   ```go
   // DiskLatencyMs reads the latency counter from the collect DiskQueue just made;
   // it never collects again, so the two readings share one interval.
   func (w *winExtras) DiskLatencyMs() (float64, error) {
       if !w.opened { return 0, w.err }
       if !w.latencyOK { return 0, errors.New("disk latency counter not available") }
       if w.lastCollect != nil { return 0, w.lastCollect }
       var v pdhFmtCounterValue
       if r, _, _ := procPdhGetFormattedCtrValue.Call(w.latency, pdhFmtDouble, 0, uintptr(unsafe.Pointer(&v))); r != 0 {
           return 0, fmt.Errorf("PdhGetFormattedCounterValue: 0x%x", r)
       }
       if v.CStatus != 0 { return 0, fmt.Errorf("disk latency counter status 0x%x", v.CStatus) }
       return v.Double * 1000, nil
   }
   ```
   Add `"errors"` to the imports if missing.
7. `sample.go` after `DiskQueue` (~34): `DiskMs *float64 \`json:"diskMs,omitempty"\` // average ms per disk transfer, all disks`.
8. `collector.go` `Collect` (~117–121): right after the `DiskQueue` block add
   ```go
   if ms, err := h.extras.DiskLatencyMs(); err == nil {
       s.DiskMs = &ms
   } else {
       s.Missing = append(s.Missing, "diskMs")
   }
   ```
9. `collector_test.go`: `fakeExtras` (~53): add a `latErr error` field and `func (f *fakeExtras) DiskLatencyMs() (float64, error) { return 4.5, f.latErr }`. In the existing "all missing" test (~123) add `"diskMs": true` to `want`, and `` `"diskMs":0` `` to the `bad` list (~131). Add `TestCollectDiskLatency`: with `fakeExtras{}` → `*s.DiskMs == 4.5`; with `latErr` set → `DiskMs == nil` and `Missing` holds `diskMs` while `DiskQueue` is still set.
10. gofmt the four files.

## Proof

```
.dev\toolchains\go\bin\go.exe vet ./internal/blackbox/...
.dev\toolchains\go\bin\go.exe test ./internal/blackbox/...
.dev\toolchains\go\bin\go.exe build ./...
```

PASS. UNMEASURED: a real PDH reading on this machine (no live sampler run in this packet).

## Depends on

Phase 02 complete (ordering only).
