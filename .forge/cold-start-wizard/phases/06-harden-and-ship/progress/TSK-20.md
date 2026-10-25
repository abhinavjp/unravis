# TSK-20 report: migration, key re-wrap and back-compat proof

Status: done (automated part). No defect found. Production code unchanged; nothing committed.
Files: `internal/casestore/sqlite/migrate_v3v4_test.go` (new, tests only). No `testdata/` fixture: a helper builds the v3 case.

## How the old case is made
`buildV3Case` creates a case with the DPAPI stand-in protector (`storetest.RandomProtector`, so nothing is Windows-only), imports 4 + 6 synthetic events, then pins it to what the old build wrote: `case_sources` and `case_envs` dropped, `schema_version`=3 (same technique as `TestVersionThreeCaseMigratesToFourOnce`). It asserts case.key and manifest.dat are v1 (no `{` header, `FileKind` = dpapi). The baseline snapshot (Info, Imports ledger with hashes/quality/gaps, all Events, Quarantines, Series over two bucket widths and three selectors incl. duration field) is read on that same handle after the downgrade, as JSON.

## Tests (all pass)
1. `TestV3CaseOpensWithNoMasterKeyAndMigratesOnce`: opens with a DPAPI-only store (Protectors nil, no master key): version 4 with exactly one version row, tag tables present, snapshot identical to baseline, Info Envs/Sources 0, CaseKind still dpapi, case.key bytes unchanged by the open. Then SetEnvs, a new import and two `AddSource` tags (one linked to an old import, one to the new one). Two further reopens: version 4 / one row, schema objects identical to after first migration, envs/sources/Info/event count (13) kept, case.key still byte-identical, still DPAPI.
2. `TestV3CaseRewrapsToMasterKeyAndKeepsItsDatabase`, two subtests:
   - `rewrap-before-first-open-on-this-build`: case never opened on this build (case.db hash equal to the hash after build), Setup + Unlock master key, `Store.Rewrap`; case.db sha256 identical, file set identical, case.key and manifest.dat now start with `{"v":2,"p":"masterkey"}\n` plus a blob and differ from before, CaseKind masterkey. A fresh store (other DPAPI stand-in) with the keyring opens it, migrates to 4 (one row), snapshot identical to baseline.
   - `migrated-then-rewrapped`: opened first with no key (positive control: migration changed case.db), then the same re-wrap checks (case.db hash identical across the re-wrap).
   Both then lock the keyring and check three wirings (locked keyring, no keyring, DPAPI-only old wiring): `Open` returns an error matching both `casestore.ErrLocked` and `masterkey.ErrLocked`, no panic (recover), nil handle, nothing registered in `Store.open`, case folder files byte-identical, no `case.db-wal` created (database never opened). Unlocking again (`masterkey.Unlock`) opens and reads identically.
3. `TestV3CaseStillOpensWhenAMasterKeyExistsButIsNotApplied`: master key set up but case not re-wrapped: opens via DPAPI with the keyring unlocked and locked, migrates, 10 events, stays dpapi.

## Results
- `go.exe vet ./internal/...` clean.
- `go.exe test ./internal/casestore/... -count=1`: casestore ok, casestore/sqlite ok (storetest has no tests).

## Notes and limits
- "Ledger" is read as the import ledger (`Imports()` records) plus `Quarantines()`; the baseline has no quarantines, so that read is only proven empty-equal.
- "Exactly once" is shown by the version staying 4 with one row, the schema objects staying identical, and data/tags added after migration surviving two reopens (a re-run that rebuilt the tag tables would drop them). The tests do not hook the migrate function to count calls.
- v3 is made by pinning on this build's code, not by running the real `master` binary, so an old-build quirk not reproduced by this pinning would not be caught. That is the point of the manual step below.
- `gofmt -l` flags nearly every file in the package (line-ending style); not caused by this task.

## UNMEASURED
Manual step 3 (copy a real local case folder to a scratch dir, open with this build on a spare port, compare overview numbers with the master build, then set a master key in the scratch data folder, re-wrap, restart, unlock, compare again). Not done by this task. Real DPAPI sealing (Windows API, not the AES stand-in) is also covered only by that manual step.

## Defects
None. No `TestBug_` tests.
