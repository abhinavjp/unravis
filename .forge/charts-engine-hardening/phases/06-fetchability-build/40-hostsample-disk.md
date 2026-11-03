# TSK-40 hostsample-disk

risk: low — one optional field copied through.

## Outcome

The engine's `HostSample` carries the recorded disk latency, so the host lane can read D9 (TSK-41).

## Write scope

- `internal/investigate/hostlane.go` (`HostSample` struct only)
- `internal/server/blackbox.go` (`hostSamples` only, ~1049–1068)
- `internal/server/blackbox_test.go` (append, if a `hostSamples` test exists; else a new `TestHostSamplesDisk` in it)

## Must not change

- `HostLane`, `ApplyHost`, any const in `hostlane.go`.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `hostlane.go` `HostSample` (~45–49): add `DiskMs *float64` after `CPU`, with the comment "average ms per disk transfer; nil when not recorded".
2. `server/blackbox.go` `hostSamples` (~1052–1056): after the `CPU` copy add
   ```go
   if s.DiskMs != nil {
       v := *s.DiskMs
       hs.DiskMs = &v
   }
   ```
3. Test: `hostSamples([]blackbox.Sample{{T: t0, DiskMs: ptr(7.5)}, {T: t1}})` → first `DiskMs` 7.5 (a copy, not the same pointer), second nil. Use a local `f := 7.5; &f` if no `ptr` helper exists in the package.
4. gofmt the files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/server/... ./internal/investigate/...
```

PASS; goldens unchanged.

## Depends on

39 (`Sample.DiskMs`).
