# TSK-12 Case-file export and restore

## Status

DONE. `go vet ./...` clean. `go test ./... -count=1` all ok (one run showed a failure in `internal/sampler` `TestWindowsSamplerRunsAndWritesABatch`, a live Windows counter test unrelated to this work; it passed on the immediate rerun and in the second full run). `gofmt -l` is clean for every new file. `-race` was NOT run: it is unavailable here (no gcc). Nothing committed or pushed.

A real second PC is UNMEASURED. The substitute is a second data folder with its own session folder, its own master key and its own passphrase, in the same process.

## Files changed

New:
- `internal/casestore/casefile.go` - container format, `WriteCaseFile`, `OpenCaseFile`, `ExtractDB`, `HoldCaseDB`, `RebindCaseDB`, `ExportCaseFile`, `RestoreCaseFile`.
- `internal/casestore/casefile_test.go` - 13 tests (below).
- `internal/server/case_file.go` - the two handlers.
- `internal/server/case_file_test.go` - 8 tests (below).

Edited (additive):
- `internal/server/routes.go` - two route rows just before `/api/cases/{id}` (restore must be ahead of it).
- `internal/server/routes_test.go` - two `routeCases` rows.

Nothing else touched (CRLF of the two edited files preserved).

## Routes

Both need the session token and local Origin like every route. Both sit under `/api/cases`, so the TSK-11 lock gate answers `423 {"error":"locked","reason":"locked"}` while the key is locked, before any handler runs. Request bodies: `Content-Type: application/json`, at most 16 KiB, only the fields `path` and `passphrase`.

Failure body (as the other case routes): `{"error":<message>,"reason":<code>}`. The master-key style failures (`no-master-key`, `master-key-unavailable`, `throttled`) use `{"error","reason","state"[, "retryAfterSeconds"]}` like TSK-11.

### POST /api/cases/{id}/export
Request `{"path":"<absolute path>","passphrase":"<the master key passphrase of this PC>"}`.
`path` is either a full file name ending `.unravis-case`, or an existing folder; for a folder the file is `<caseId>.unravis-case` inside it. The case code is the 26-character case id.
- 200 `{"path":"<absolute file written>","name":"<caseId>.unravis-case","bytes":N}`
- 400 `bad-request`, `bad-case-id`, `bad-path` (relative, UNC, device prefix, `..`, alternate stream, reparse point, missing or non-folder target, parent not a folder, or inside the cases folder)
- 401 `wrong-key` (wrong, empty or over-long passphrase; identical body)
- 404 `case-not-found`
- 409 `no-master-key`, `file-exists` (never overwrites), `case-changed` (the db kept changing through 3 attempts), `case-busy`, `busy` (another export or restore is running)
- 413 `too-large`, 415 `bad-content-type`
- 423 locked (gate, or a lock arriving mid-request)
- 429 `throttled` (5 wrong passphrases, then 1 s doubling to 60 s; `Retry-After` header)
- 500 `export-failed` (fixed text, no detail)
- 503 `master-key-unavailable`, `cases-unavailable`, `case-file-unavailable`

The passphrase is checked against this PC's `masterkey.dat` (`masterkey.Unlock`), so a file can only be sealed with the key the user actually holds.

### POST /api/cases/restore
Request `{"path":"<absolute path of a .unravis-case file>","passphrase":"<passphrase of the PC it was exported from>"}`.
- 201 the new case, same shape as `POST /api/cases`: `{"id","title","created","closed","bytes","imports"}`. The id is NEW. `created` is the restore time.
- 400 `bad-request`, `bad-path` (shape, reparse point, or not named `*.unravis-case`)
- 409 `no-master-key`, `busy`
- 413, 415 as above
- 422 `bad-case-file`, message exactly `That file could not be opened with this master key.` One identical body for: wrong passphrase, empty or over-long passphrase, tampered or truncated file, trailing bytes, wrong magic or version, bad KDF parameters, hash mismatch, unusable database, case-id mismatch, missing file, a folder. It counts toward the restore throttle.
- 423 locked
- 429 `throttled`
- 500 `restore-failed` (fixed text)
- 503 as above

Restoring the same file twice makes two cases. Restoring on the source PC makes a copy. The source case is never touched.

## Container format (version 1)

41-byte plaintext prefix, bound as AEAD associated data of the header: magic `UNRVCASE`(8), version BE(2)=1, KDF id(1)=1 Argon2id, time(4), memory KiB(4), threads(1), salt length(1)=16, salt(16), header ciphertext length(4). Then the header ciphertext, then the raw `case.db` bytes (already encrypted by SQLite). The file size must equal `41 + n + dbBytes` exactly.

The header key is Argon2id(passphrase, fresh random salt). The header (JSON, sealed with `masterkey.SealWith`) carries `caseId`, `title`, `created`, `closed`, `dbKey`, `pseudoKey`, `dbBytes`, `dbSha256`. No plaintext and no machine-bound secret: the only things in clear are the magic, version and KDF parameters. The case key travels inside the sealed header, so DPAPI-sealed cases export too and the file opens on any PC given the passphrase.

## Case-id rebinding spike - finding

The case id is bound in four places:
1. `meta.case_id` in the encrypted `case.db`.
2. The `events.evidence_id` prefix `ev:<first 8 chars of id>:<importer>:<hash>`. This is the events table primary key, and `evidence.CasePrefixes` strips ledger references by it on erase.
3. `case.key` JSON `caseId`.
4. `manifest.dat` `caseId`.

No other id-bound rows exist (checked every table). Rebinding is feasible and is implemented: `RebindCaseDB` updates `meta.case_id` and rewrites the `events` prefix in batches of 20000 inside ONE `BEGIN IMMEDIATE` transaction (all or nothing). `case.key` is written fresh for the new id. The manifest is not copied: `Open` rebuilds it from the db. Verified by tests: 120 events equal after restore, prefix rewritten, series and pseudonyms equal, a mismatched-id file is rolled back.

## Safety properties

- Export is read-only for the source and works on an open case: a second read-write connection runs `wal_checkpoint(TRUNCATE)` (retry on busy up to 5 s) and then holds a read transaction while the db is hashed and copied. The db is hashed twice (hash pass, then copy pass) and retried up to 3 times if it changed.
- Overwrite refusal is atomic (`O_EXCL` reservation of the destination). The partial file uses a `unravis-case-*.tmp` name and is renamed with `sealedfile.Replace`.
- Restore stages the db to `<root>/restore-<hex>.part`, verifies the SHA-256, and only then creates the case folder. A tampered file therefore creates no case folder. After `Create`, any failure runs `Erase` (with `RemoveAll` as a fallback).
- Streaming copy only. Measured: a 1 GiB synthetic container write + open + extract took 3.6 s and allocated 35 MiB in total (18 MiB heap in use afterwards). Sizes are capped (`MaxCaseFileDB` 8 GiB, file size must match the header exactly), so there is no decompression and no archive path to zip-slip.
- The passphrase is decoded straight into a `[]byte` (`unquoteSecret`), the request buffers and copies are wiped, derived keys are zeroed. Not logged, not returned, not in a URL. Error text never echoes input or paths. Tests assert the passphrases do not appear in any response or in the log.
- Routes are 423 while locked; restore needs the destination PC's master key unlocked (it re-wraps under it, decision D-1).
- One export or restore at a time per server (409 `busy`).

## Tests

`internal/casestore/casefile_test.go` (13): round trip into a second data folder (new id, 120 events equal, prefix rewritten, kind master key, pseudonyms equal, source untouched); restored twice gives two cases (also into the source folder); case file of an open case; overwrite refusal and bad name; no plaintext in the file; about 25 tamper files plus wrong passphrase, wrong case, empty passphrase, missing file and a folder all give the same error and leave nothing behind; locked master key; unusable db rolled back; mismatched id rolled back; rebind is all or nothing; cancellation; format constants; streaming of large dbs (64 MiB default, 1 GiB with `UNRAVIS_SCALE=1`, asserts allocation under 160 MiB).

`internal/server/case_file_test.go` (8): key states (503 without a session, 409 with no key, 423 locked, 423 after lock); bad bodies and bad paths (415, 400, 413, UNC, device prefix, `..`, ADS, missing folder, a file as folder, inside the cases folder; answers do not echo the path or the passphrase); wrong export passphrase gives 401 and then 429; export then restore on another "PC" with a different master key (reply shape, `<id>.unravis-case` name, no plaintext in the file, no temp files left, source intact, wrong destination passphrase refused, restore twice, restore on the source PC, no passphrase in logs, named-file export, overwrite refused and file unchanged); 12 restore failure kinds with an identical body, no leftovers, no case created, then 429; DPAPI-sealed source case (via the `caseFileDPAPI` override, locked when DPAPI is unavailable); one transfer at a time; methods and token. Existing `TestRouteTableCoversEveryRoute`, the route-method tests and the lock-gate table walk pass with the two new rows.

## Deviations

1. The manifest is not copied; the header stores title, created, closed and case id, and `Open` rebuilds the manifest.
2. Export verifies the typed passphrase against `masterkey.dat` with `masterkey.Unlock`, which costs one extra Argon2 run per export.
3. `internal/casestore` now imports the ncruces SQLite driver (for `HoldCaseDB` and `RebindCaseDB`).
4. `ExportCaseFile` takes a `params` argument (zero means `masterkey.DefaultParams`) so tests can use cheap Argon2.
5. `Root()` is reached through an optional interface on `h.cases` (the SQLite store has it; any other store gives 503 `case-file-unavailable`).
6. Bad path shape is answered (400) before the passphrase is tried; existence checks (folder exists, file exists, inside the cases folder) happen only after the passphrase is right, so the folder is not probed by someone without the key.
7. Package variables `caseFileDPAPI` and `caseFileParams` exist as test seams.

## Concerns and unmeasured

- A real second PC is UNMEASURED. A real install, real DPAPI and a real copy by USB or network share are not exercised.
- `-race` is not run (no gcc). Concurrency is covered by the busy token, the held read transaction and the cancellation test, not by the race detector.
- Rebind cost on very large dbs is not measured. The primary-key update rewrites the events indexes and grows the WAL; the 1 GiB test used a synthetic db without events.
- No disk-free pre-check. A full disk becomes a write failure plus cleanup (`export-failed` or `restore-failed`).
- A crash between the `case.db` replace and the `case.key` write during restore leaves a half-made case folder that the user can erase; there is no automatic sweep of `restore-*.part` leftovers after a crash.
- Long exports and restores run inside one HTTP request. If the server has request or write timeouts, a multi-GB transfer could be cut off; not measured.
- The restored case's `created` time is the restore time (the original is kept in the file header but not applied).
- Opening the source read-write for the checkpoint may touch its WAL and `-shm` files, not the logical content.
- The sampler test `TestWindowsSamplerRunsAndWritesABatch` failed once in a full run and passed on rerun; unrelated to this task, but flaky.
