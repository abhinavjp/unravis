# TSK-21 Go fixes

Six Go-side fixes from the TSK-21 security and clumsy-user reviews. Go only: nothing under `web/`, `sql/`, `.claude/` or `.mcp.json` was touched, nothing was committed.

## What changed, per item

### 1. Security L1: one throttle for every master-passphrase check
- `internal/server/masterkey.go`: new `checkPassphrase` (caller holds `op`). It waits on the shared `unlockTh`, rejects empty or over-long input, runs Argon2 on a cloned buffer that is cleared, then calls `fail()` on a wrong key or `reset()` on success. `masterKeyUnlock` uses it.
- New `MasterKeySession.VerifyPassphrase(pass)` takes `op`, calls `checkPassphrase` and locks the proving ring at once, so it never changes the session. New `throttleWait()` is a cheap early check.
- `internal/server/case_file.go`: the export re-auth is now `h.mk.VerifyPassphrase`. `wait > 0` answers `masterKeyThrottled`, `ErrWrongKey` answers 401 `wrong-key`, anything else 500 `export-failed`. `exportTh` is gone. `restoreTh` stays: restore judges a case file's own passphrase, a different target.
- Unlock while already unlocked still judges nothing, so it cannot feed the counter.

### 2. Security L5: probe blocklist
`internal/server/probe.go`: `probeBlockedAddr` also blocks 168.63.129.16 (Azure WireServer). For IPv6 it re-checks the embedded IPv4 of `64:ff9b::/96` (last 32 bits), `2002::/16` (bits 16 to 47) and `64:ff9b:1::/48` (every RFC 6052 position: /32, /40, /48, /56, /64, /96). Every address in `64:ff9b:1::/48` is also blocked by the /32 reading (0.x.x.x), which is the conservative result. Private ranges stay allowed, as before.

### 3. Security L2: masterkey `readDoc`
`internal/masterkey/masterkey.go`: `Lstat` (a link or reparse point is not regular, so damaged), `os.Open` once, `f.Stat()` must be regular and `os.SameFile` with the `Lstat` result, then `io.ReadAll(io.LimitReader(f, fileMax+1))`. Length 0 or over `fileMax` is damaged. No size check by path can be outrun now.

### 4. Security L3: reset-damaged re-inspects before the rename
`masterKeyResetDamaged` calls `masterkey.Inspect(dir)` immediately before `os.Rename` and answers 409 `not-damaged` if the file is no longer damaged. Rename by path cannot be made atomic with that check, so a window of microseconds remains. The re-inspect shrinks it from the whole request to a pair of syscalls. No test races it. The existing refuse-when-not-damaged test still covers the earlier state check.

### 5. Clumsy M1: half re-wrapped case no longer answers 500
Cause: a power cut between the two replaces of a re-wrap leaves the manifest under the master key and `case.key` still under Windows. Open refused that as `ErrKeyFile` (500).
- `casestore.Rewrap` was already idempotent (resumable); `TestP06_RewrapRecoversFromACutAtEveryStep` guards it.
- `internal/casestore/keyfile.go`: new `ErrNeedsRewrap`, which wraps `ErrKeyFile` so old `errors.Is(ErrKeyFile)` callers still match.
- `internal/casestore/sqlite/sqlite.go`:
  - `load` calls `healHalfRewrap` before `readCaseFiles`. It finishes the re-wrap itself when the case has exactly that shape, the master key is unlocked and a Windows protector exists. Failure is only logged.
  - `Store.Rewrap` now ends in the new `rewrapDir` (`load` holds `s.mu`, so it cannot call `Rewrap`).
  - `readCaseFiles` returns `ErrNeedsRewrap` for manifest=master and key=DPAPI. Other mixes stay `ErrKeyFile`.
  - New `Store.NeedsRewrap(id)` reads headers only.
- `internal/server/cases.go`: `writeStoreError` maps `ErrNeedsRewrap` to 409 `{"error": needsRewrapText, "reason": "needs-rewrap"}`.
- When the key file itself cannot be unsealed (a different Windows protector), the answer stays a plain `ErrKeyFile`: that is a genuinely unreadable key, not something a re-wrap mends.

#### API shape for the web side
- `GET /api/master-key/status` returns `{"state", "dpapiCases", "needsRewrap"}`. `needsRewrap` is always present and is a subset of `dpapiCases`. Both are 0 while locked or none.
- Opening a case self-heals while unlocked, so normally the user sees nothing. Status counts drop (`dpapiCases` -1, `needsRewrap` -1) after the open.
- If healing cannot happen, case routes answer `409 {"error": "This case was only partly protected with the master key. Protect older cases to finish it.", "reason": "needs-rewrap"}`.
- The client's recovery is the existing action: `POST /api/master-key/rewrap {"all": true}` (or `{"caseId": ...}`), which finishes the case. The screens should show "Protect older cases" when `dpapiCases > 0` (unchanged) and may treat `needsRewrap > 0` as the urgent variant.
- `TestStatusRevealsOnlyStateAndCount` was renamed `...Counts` and now expects 3 fields.

### 6. Clumsy L1: stale `restore-<hex>.part` files
`sqlite.Store.Sweep` first calls `sweepRestoreStaging(now)`. It removes only regular files named `restore-<16 lowercase hex>.part` in the cases folder whose mtime is more than 1 hour old (`restoreStagingMaxAge`). A file still open on Windows cannot be deleted, so an in-use file is kept; a fresh one is kept by age. Directories and other names are left alone. Sweep already runs at startup (`cmd/audit-explorer/cases.go`).

## Tests

Flipped (renamed, comments updated, now passing):
- `TestBug_HalfRewrappedCaseIsUnopenable500` is now `TestP06_HalfRewrappedCaseOpensBySelfHealing`.
- `TestBug_RestoreStagingLeftoverIsNeverSwept` is now `TestP06_RestoreStagingLeftoverIsSwept`.

Changed: `TestP06_ExportThrottleAfterFiveWrongKeys` restarts with `p06Restart` and unlocks, because the throttle now lives in the session.

New:
- `internal/server/p06_gofixes_test.go`
  - `TestP06_ExportFailuresCountAgainstUnlock`
  - `TestP06_UnlockFailuresCountAgainstExport`
  - `TestP06_RightExportPassphraseResetsTheSharedCount`
  - `TestP06_VerifyPassphraseLeavesTheSessionAsItWas`
  - `TestP06_ProbeBlocksMetadataAddressesInEveryCostume`: 24-row table.
  - `TestP06_HalfRewrappedCaseShowsInStatusThenSelfHeals`
  - `TestP06_HalfRewrappedCaseFinishedByTheRewrapRoute`
  - `TestP06_NeedsRewrapIsA409WithAReason`
- `internal/casestore/sqlite/p06_gofixes_test.go`
  - `TestHalfRewrappedCaseHealsOnOpen`
  - `TestHalfRewrappedCaseNeedsRewrapWhenItCannotHeal`
  - `TestNeedsRewrapIsFalseForWholeAndMissingCases`
  - `TestIsRestoreStagingMatchesOnlyTheExactShape`
  - `TestSweepRemovesOnlyStaleRestoreStaging`
  - `TestSweepKeepsAStagingFileThatIsInUse` (Windows only)
- `internal/masterkey/p06_gofixes_test.go`
  - `TestP06_ReadDocCapsTheFileAtFileMax`: exactly `fileMax` is usable, `fileMax+1` and 1 MiB are damaged.
  - `TestP06_ReadDocRefusesALink`: SKIPPED on this machine (creating a symlink needs a privilege). The oversized-file and file-is-a-directory cases were already covered by `TestDamagedFilesGiveDamaged` and `TestKeyFileAsDirectoryIsDamaged`.

Evidence (this machine, Windows):
- `gofmt -l` on every changed or new Go file with CR stripped: clean. The working tree mixes LF and CRLF, so plain `gofmt -l` flags most of the repo; that is not from these edits.
- `go vet ./...`: clean.
- `go test ./... -count=1`: every package ok, including `internal/sampler` on the final run and `internal/server`, `internal/casestore/sqlite` and `internal/masterkey`.

## Notes
- Restore keeps its own throttle, as asked.
- The L3 check cannot be atomic with a rename by path (see item 4).
- New test files use CRLF, matching the repo.
