# TSK-21 L6: locate workers no longer pile up

Finding: `runWithin` returned after 10 s but its worker kept running inside a slow file system call, so repeated locate calls (30/min) stranded workers.

## Change (internal/server/discover_locate.go)
- `locateSlots`: package-level semaphore, `maxLocateWorkers = 4`. `runWithin` takes a slot without blocking before starting the worker. The worker frees it in a defer when it returns (also on panic), not when the request returns.
- No free slot: 429 with the fixed `{"error": "..."}` body from `writeError`, the same shape as the existing "Too many folder checks" 429. Text: "Other folder checks are still running. Wait a minute, then check again." No path or OS text.
- 429 reason string: none. The existing locate 429 has no `reason` field either; the web client already maps any 429 to its `import-busy` wording through `classifyError` (status 429). No new reason, nothing to map on the web side.
- `runWithin` now builds a context that ends at the 10 s deadline (or when the request ends) and passes it to the worker. `locateWork` / `locateFiles` check it before and after each file system step (Final, Lstat, ReadDir, every 256 directory entries, before each importer check, before each file identify). A cancelled worker returns the existing `timed-out` outcome and stops at the next step. A worker stuck inside one call still holds its slot until that call returns.
- Reasons stay a closed set; no new reason added.

## Tests (internal/server/discover_locate_test.go)
- `TestLocateWorkerCeilingHoldsAndFreesWhenWorkersFinish`: slots shrunk to 2, blocked FS fills them, third call is 429 (fixed body, no path), slots free once workers finish, next call is 200.
- `TestLocateDeadlineStopsTheWalkerBetweenSteps`: after the deadline the blocked Final returns; Lstat and ReadDir are never called.
- `TestRunWithinReleasesTheSlotWhenTheWorkerEndsEvenOnPanic`.
- Existing locate tests unchanged and green.

## Checks
gofmt (CR stripped) clean on both files, `go vet ./...` clean, `go test ./internal/server/ -count=1` green.

Not done: the second half of L6 (existence oracle in the threat model) is a docs note, not code.
