# TSK-01: Verify every finding before it is offered
risk: high — a gate on what the user trusts; touches `Run` for every detector.

## Outcome
After detectors run, each importable finding (kind log-file / log-set / event-channel, importer non-empty) is verified: the importer named by the finding reads a bounded head sample of each of its paths (reusing the bounded local reader the import uses) and recognises it. Findings that pass get `verified: true`. Findings that fail are not dropped: they keep their row but `importer` is cleared, `verified` is false, and `unreadable` carries a wording key explaining why in plain words (cannot open, empty, not the format it was named for). The scan therefore never offers a tickable row it cannot read (R6). Findings that were never selectable (remote-only, export-needed) are unchanged.

## Write scope
- `internal/discover/verify.go` (new), `internal/discover/verify_test.go` (new)
- `internal/discover/discover.go` (add `Verified bool` json `verified` and `Unreadable string` json `unreadable,omitempty` to `Finding`; call the verify pass in `RunWith` after `checkFinding`)
- `internal/discover/wording.go` (the new reason keys only)
- `internal/discover/testdata/verify/` (new fixtures: good, empty, binary, wrong-format)

## Must not change
- `Finding.paths` stays unexported; no path reaches JSON.
- The import path (`postDiscoverImports`) and all detector outputs other than the new fields.
- Existing Limits, time budget and caps: verification counts against the scan's time budget and file-count caps and must not extend them.

## Changes
1. `verifyFinding(f Finding, env Env) (ok bool, reasonKey string)`: open at most the first 4 KiB of up to 3 paths via the existing localread helper; ask the registered importer's sniff/header check; for `event-channel` findings confirm the channel exists through the existing env function, not by reading content.
2. In `RunWith`, run verification with a worker bound (reuse the scan's concurrency limit) and honour context cancellation; a finding not verified before the time budget ends is demoted with reason `verify-timeout`, never silently kept ready.
3. Reason wording keys in `wording.go`: plain English, no jargon; copylint rules stay satisfied.

## Proof
- Test: good file → `verified` true. Empty file, binary file, wrong-format file (an IIS file named as log4net) → `verified` false, `importer` empty, `unreadable` set.
- Test: file deleted between detect and verify → demoted, no panic.
- Test: 200 findings finish inside the time budget (fake clock) and cancel on ctx.
- `go test ./internal/discover/...` green; existing detector tests unchanged.

## Depends on
none
