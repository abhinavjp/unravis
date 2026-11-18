# TSK-04 Read-only drives route
Risk: medium (new route; must not leak OS text or paths). Worker size: medium. Reason: Windows API reuse, a route and table tests. Gated by OD-5.

## Outcome
`GET /api/blackbox/this-pc/drives` returns contract C-3: letter, label, type, system, free and total bytes for this PC.

## Write scope
- `internal/server/blackbox_drives.go` (new: handler and answer type)
- `internal/server/blackbox_drives_windows.go` (new: GetLogicalDrives, GetDriveType, GetVolumeInformation, GetDiskFreeSpaceEx)
- `internal/server/blackbox_drives_other.go` (new: empty list)
- `internal/server/blackbox_drives_test.go` (new)
- `internal/server/blackbox.go`: one route line next to `/this-pc/targets` (line 157). Nothing else in this file.

## Must not change
Other routes, `archive_path_windows.go` behaviour (reuse its helpers, do not edit them unless exported reuse needs it; record a deviation if so), any collector or agent.

## Changes
- GET only, through `bbRoute`/`bbExact`/`bbM` like the other this-pc routes.
- A seam (like `bbTargetsFake` in tests) so tests inject a drive list.
- Unreadable free space → `null`, never 0. No OS error text in the answer.
- `system` is true for the drive holding `%SystemRoot%`.

## Proof
`go test ./internal/server/...`. Tests: GET returns the faked list; POST is refused; null free space survives; no extra fields.

## Depends on
None.
