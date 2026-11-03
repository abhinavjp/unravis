# WP-70b Log tee that survives a missing console, and a bounded launch-code list
risk: low    size: S    wave: 1    model: sonnet
## Outcome
`applog.Tee` writes to every writer it is given and keeps going when one of them fails, and `applog.Open` uses it. Today `io.MultiWriter(os.Stderr, file)` stops at the first error, so in a windowless build (where stderr is invalid) nothing would reach the log file. After this packet the log file gets every line whether or not stderr works. The packet also adds `applog.RegisterEphemeralSecret`, which keeps only the last 8 values, so a long-running app that mints a launch code on every Open cannot grow the secret list (and the cost of every log line) without bound.

## Write scope
- `internal/applog/tee.go` (new)
- `internal/applog/tee_test.go` (new)
- `internal/applog/applog.go`
- `internal/applog/sanitize.go`
- `internal/applog/ephemeral_test.go` (new)

## Must not change
- Log line format, rotation, `RegisterSecret` (it still keeps every value for good), what `Sanitize` removes today, the exported names already in `applog.go` and the existing tests.
- Every file outside the write scope.

## Changes
1. `tee.go`: `func Tee(writers ...io.Writer) io.Writer`. Entries that are a nil interface value are dropped when `Tee` is called; when none remain it returns `io.Discard` (it never returns a nil writer). Its `Write(p)` offers `p` to every remaining writer in order and never stops early. It returns `len(p), nil` when at least one writer accepted all of `p` without error; when every writer failed it returns `0` and the first error seen. A short write counts as a failure of that writer. A typed-nil `*os.File` (what `os.Stderr` is in a windowless build) is NOT detected: its `Write` returns `os.ErrInvalid`, which is just a failing writer.
2. `applog.go`: in `Open`, replace `io.MultiWriter(os.Stderr, rw)` with `Tee(os.Stderr, rw)`. Nothing else changes.
3. `sanitize.go`: add `var ephemeral []string` (guarded by `secretsMu`) and `func RegisterEphemeralSecret(value string)`: ignore a value shorter than 8 characters or already in the list; append it, and when the list then holds more than 8, drop the oldest. In `Sanitize`, inside the existing read lock, run the same `strings.ReplaceAll(out, s, redacted)` over `ephemeral` right after `secrets`.
4. Tests in `tee_test.go`:
   - `TestTeeWritesToEveryWriter`: two buffers both get the bytes; result is `len(p), nil`.
   - `TestTeeKeepsWritingWhenOneWriterFails`: first writer always errors, second is a buffer; the buffer gets the bytes and the result is `len(p), nil`.
   - `TestTeeReportsTheFirstErrorWhenEveryWriterFails`: two failing writers with different errors; result is `0` and the first error.
   - `TestTeeIgnoresANilWriter`: `Tee(nil, &buf)` writes to the buffer; `Tee()` and `Tee(nil)` return `io.Discard` and writing to them returns `len(p), nil`.
   - `TestTeeSurvivesATypedNilFile`: `var f *os.File; Tee(f, &buf)` does not panic and the buffer gets the bytes.
   - `TestOpenStillLogsWhenStderrIsBroken`: swap `os.Stderr` for a closed `*os.File` (restore it with `t.Cleanup`), call `Open` on a temp directory the way `TestOpenWritesSanitizedLinesToFile` does, log one line, and assert the line is in the file.
5. Tests in `ephemeral_test.go` (each resets `ephemeral` under `secretsMu` with `t.Cleanup`): `TestEphemeralSecretsAreBounded` (register 100 distinct 16-character values: the list holds exactly the last 8), `TestEphemeralSecretIsRedacted` (`Sanitize` of a text holding a registered value does not contain it), `TestEphemeralOldestIsForgotten` (the 9th value pushes out the 1st, which `Sanitize` no longer replaces), `TestEphemeralIgnoresShortAndDuplicateValues`.

## Proof
- `go` is `.dev\toolchains\go\bin\go.exe` from the repo root with the environment of WP-58 step 3.
- `go test ./internal/applog -run "TestTee|TestOpenStillLogsWhenStderrIsBroken|TestEphemeral" -count=1 -v` -> 10 tests `PASS`.
- `go test ./internal/applog -count=1` -> `ok` (the older tests still pass); `go vet ./internal/applog` -> no output.
- `Select-String -Path internal/applog/applog.go -Pattern "MultiWriter"` -> no output.

## Depends on
none
