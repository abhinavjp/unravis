# TSK-12: Case-file export and restore (always a new case)
risk: high — moves encrypted case data between machines and changes the case id.

## Outcome
`POST /api/cases/{id}/export` writes one file (`<caseCode>.unravis-case`) the user can copy to another PC. `POST /api/cases/restore` reads such a file and creates a new case with a new id on this PC; it never overwrites an existing case, even if the same file is restored twice (R3, R16). The file holds no plaintext data and no secret tied to the exporting machine.

## Write scope
- `internal/casestore/casefile.go`, `internal/casestore/casefile_test.go` (new: container format, `Export`, `Restore`)
- `internal/server/case_file.go`, `internal/server/case_file_test.go` (new routes)
- `internal/server/routes.go` (route entries)

## Must not change
- The source case (export is read-only, and the case may stay open); case id format; `case.db` encryption.
- Never write the export or the restore to a location the user did not choose: export takes a destination path from the request, restore a source path; both go through the existing path-safety checks used by file imports, and the export refuses to overwrite an existing file.

## Changes
1. Container: magic bytes, format version, then an authenticated header (AEAD) holding the export salt, Argon2id parameters, the case's DB and pseudonym keys, the manifest, the original title and the SHA-256 of the database bytes; then the `case.db` bytes as they are (already encrypted under the DB key). The header key comes from the **exporting PC's master key passphrase and a fresh per-file salt**. A case file therefore needs the master key it was exported with, not the destination PC's salt.
2. Restore asks for that passphrase in the request body, derives the header key, verifies the header and the database hash, creates a new case folder with a new id, re-wraps the keys under the **destination** PC's protector (master key must be set and unlocked there), and stores `case.db`.
3. Spike first (limit: half a day): the key file binds to `CaseID` and `case.db` meta holds the id. Find out whether the id sits inside the encrypted database in a way that must be rewritten on restore. If it does, restore opens the copied database with the DB key and rewrites the meta id and any id-bound rows in one transaction, then runs the existing integrity check; if it cannot be done safely the task records a blocker with the finding instead of shipping a restore that reuses the old id.
4. Streaming copy for large databases (no whole-file read into memory); partial files are written to a temp name and renamed only after the hash check passes.
5. Any failure (wrong passphrase, tampered header, truncated file, bad hash, unknown version) gives one message: "That file could not be opened with this master key."

## Proof
Tests: export → restore into a second data folder gives a new id, equal event counts and equal query results; restore twice gives two cases; wrong passphrase, flipped byte in the header, flipped byte in the database part, truncated file, wrong magic → same error and no case folder left behind; export refuses to overwrite; the exported file contains no plaintext marker strings from the case (scan for known titles and event text); a 1 GB-class synthetic database round trips without exceeding a memory bound (skip with a clear message when disk is short). `go test ./internal/casestore/... ./internal/server/...` green. A real second PC is UNMEASURED; the second data folder is the substitute and the report must say so.

## Depends on
TSK-10, TSK-11
