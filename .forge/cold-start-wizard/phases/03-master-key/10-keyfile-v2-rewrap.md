# TSK-10: Key file v2, protector selection and per-case re-wrap
risk: high — changes how every case opens; a mistake can make a case unreadable.

## Outcome
`case.key` and `manifest.dat` record which protector sealed them (key file version 2: `{"v":2,"p":"dpapi"|"masterkey",...}`; version 1 means DPAPI). Opening a case picks the protector from the file: DPAPI cases open as today; master-key cases need an unlocked `Keyring` and otherwise return `ErrLocked`. A new `Rewrap(caseID, from, to Protector)` moves a case from DPAPI to the master key (and back is NOT offered). Only `case.key` and `manifest.dat` are rewritten; `case.db` is never touched (R13, R15).

## Write scope
- `internal/casestore/keyfile.go`, `internal/casestore/keyfile_test.go`
- `internal/casestore/sqlite/sqlite.go`, `internal/casestore/sqlite/case.go` (protector selection at open; no schema change)
- `internal/casestore/rewrap.go` (new), `internal/casestore/rewrap_test.go` (new)
- `internal/casestore/sqlite/testdata/` (a v1 DPAPI key-file fixture, if tests cannot create one off Windows)

## Must not change
- `case.db` bytes, schema, case id format; the v1 reader (a v1 file must still be read and must still be what DPAPI-only builds write when no master key exists, so rolling the binary back keeps working for cases not yet re-wrapped).
- `ErrKeyFile` never carries key bytes.
- The Protector interface and DPAPI behaviour off Windows (still fails closed).

## Changes
1. `keyFileDoc` gains `P string`; the reader accepts v1 and v2, rejects unknown versions and unknown protector names with `ErrKeyFile`.
2. `Options` gains a `Protectors` lookup (kind → Protector) instead of a single `Protector`; the single-protector field stays as the DPAPI default so existing callers compile.
3. `Rewrap`: read with the old protector, write the new `case.key` and `manifest.dat` to temp files in the case folder, verify each by reading it back with the new protector and comparing the key bytes, then replace both with `sealedfile.Replace` under `sealedfile.Lock`. If any step fails the originals are untouched and the temp files are removed. Idempotent: re-wrapping a case already on the master key is a no-op success.
4. A case whose manifest is on a different protector than its key file is treated as damaged (`ErrKeyFile`), not silently fixed.

## Proof
Tests: v1 fixture opens unchanged; v2 round trip with a fake Protector; locked → `ErrLocked`; re-wrap then reopen with only the master key; simulated failure after the first file is replaced (inject an error) leaves the case openable by one of the two protectors and a second `Rewrap` completes it; unknown `p` value rejected; a v2 file opened by the v1-only reader path (copy of old code in a test) is refused rather than misread; `case.db` hash identical before and after re-wrap. DPAPI-specific tests run on Windows only; off-Windows runs use the fake. `go test ./internal/casestore/...` green.

## Depends on
TSK-06 (same `sqlite` package), TSK-09
