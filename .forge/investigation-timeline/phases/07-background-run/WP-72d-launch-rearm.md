# WP-72d Re-arm the launch code of a running server
risk: medium    size: S    wave: 1    model: sonnet
## Outcome
`server.Rearm` lets the app give a running server a fresh one-time launch code, so the tray ("Open Unravis") and a second launch can open a new browser tab long after the first code was used, expired, or never minted (a `--background` start arms none). The old code stops working at once. The token still travels only in the URL fragment after `/launch?c=<code>`.

## Write scope
- `internal/server/launch.go`
- `internal/server/launch_rearm_test.go` (new)

## Must not change
- The behaviour of `WithLaunchCode`, `launchState.take`, the 256-character limit, the single-use rule, response headers and every other file.
- No code ever reaches a log line from this function. (Registering the code with `applog.RegisterSecret` is the caller's job, see `mintLaunchCode` in WP-70c.)

## Changes
1. `launch.go`: add `func Rearm(h http.Handler, code string, until time.Time) bool`, a package-level function in the same style as `CancelImports` in `import_jobs.go`. `server.New` returns the `*handler` unwrapped, so `Rearm` does `hh, ok := h.(*handler)`; it returns false when `!ok`, `hh == nil`, or `code` is shorter than 16 or longer than 256 characters. Otherwise it sets `hh.launch.code` and `hh.launch.until` under `hh.launch.mu` and returns true (`hh.launch` is a value field, so it always exists; a server built without `WithLaunchCode` has an empty code and is re-armed the same way). The previous code is discarded.
2. Tests in `launch_rearm_test.go`, built the way `secfix_headers_test.go` builds its handler (`New(&fakeRepository{}, testAssets(), testToken, false, WithLaunchCode(...))`) and using `launchRequest(h, "?c=...")` and `wantGonePage`:
   - `TestRearmReplacesTheCodeAndTheOldOneStopsWorking`.
   - `TestRearmedCodeWorksOnceThenIsGone`.
   - `TestRearmWorksOnAServerStartedWithoutALaunchCode` (built with no `WithLaunchCode`).
   - `TestRearmRejectsAHandlerThatIsNotTheServer` (a plain `http.NewServeMux()` gives false).
   - `TestRearmRejectsEmptyShortAndLongCodes` (0, 15 and 257 characters give false and leave the current code working).
   - `TestRearmWithPastExpiryGivesAGonePage` (an `until` already in the past is rejected by `take`).

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/server -run "TestRearm" -count=1 -v` -> 6 tests `PASS`.
- `go test ./internal/server -count=1` -> `ok`; `go vet ./internal/server` -> no output.
- `Select-String -Path internal/server/launch.go -Pattern "log\.(Print|Printf|Println)"` -> no output.

## Depends on
none
