# TSK-03: Locate a log4net / NLog folder the scan could not derive
risk: medium — accepts a user-typed path on the server.

## Outcome
A finding the scan could only half-identify (config says "log4net, folder unknown") can be completed: `POST /api/discover/scans/{scan}/findings/{finding}/locate` with `{path}` verifies the typed folder or file (R8). On success the finding in that scan gets its paths set, `verified` true and an importer; on failure a plain reason is returned and the finding is unchanged. Repeatable (idempotent).

## Write scope
- `internal/server/discover_locate.go` (new), `internal/server/discover_locate_test.go` (new)
- `internal/server/routes.go` (one new route entry)
- `internal/discover/verify.go` (export a small `VerifyPaths` wrapper only; no behaviour change to TSK-01)

## Must not change
- Local-only: the path must be absolute, pass `ValidLogPath`, and resolve (symlinks/junctions) to a regular file or a directory of log files on a local fixed drive; UNC/network paths are refused (consistent with the scan's confinement).
- No directory listing is ever returned; only counts and the verdict.
- Session-token check and rate limit as the other discover routes.

## Changes
1. Handler `locateFinding`: validate input, resolve and confine, call `VerifyPaths`, update the in-memory scan store under its lock, return `{verified, files, reason}`.
2. Route entry mirroring the existing `/api/discover/scans/{scan}` pattern.

## Proof
- Tests: good folder → ok; folder of unrelated files → reason, finding unchanged; relative path, UNC path, `..` traversal, junction pointing outside, 4 KiB-long path, missing token → refused; repeat call → same result; expired scan → 404.
- `go test ./internal/server/...` green.

## Depends on
TSK-01
