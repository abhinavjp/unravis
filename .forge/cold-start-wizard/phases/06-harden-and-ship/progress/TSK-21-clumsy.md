# TSK-21 item 3: Phase 06 clumsy-user review (master key and case-file flows)

Scope: master key and case-file flows end to end. The start screen, create wizard and damaged-key flow were covered in P04 and P05 and are not repeated.
Method: code read plus tests that pin each defect. No production code changed.

Evidence files:
- D:\Projects\api-audit-explorer\internal\server\p06_clumsy_test.go
- D:\Projects\api-audit-explorer\internal\masterkey\p06_clumsy_test.go
- D:\Projects\api-audit-explorer\web\tests\clumsy.wizard.test.cjs

## Severity counts

High 0, Medium 1, Low 4, Info 4.

## Findings

### M1 (Medium) A cut during re-wrap leaves a case that answers 500, and the UI offers no second try
- Repro: set a master key with older (DPAPI) cases, start the re-wrap, and have the app die between "manifest-replaced" and "key-replaced". Reopen the app and open that case.
- Cause: after the cut the manifest says master key and the key file is still DPAPI. `Open` fails, and the route answers 500 `store-error`. Calling Rewrap again repairs it (guarded by `TestP06_RewrapRecoversFromACutAtEveryStep`), but the only UI that calls `rewrapCases` is the success panel shown once after the first set-up (`MasterKeySetup.svelte`). After a restart `dpapiCases` is no longer shown anywhere, so the person has no screen from which to retry.
- Evidence: `TestBug_HalfRewrappedCaseIsUnopenable500` (Go, fails) and `bug: no screen other than the first set-up success panel can re-wrap older cases` (web, fails).
- Fix: either make `Open` self-heal a manifest/key mismatch of this one shape (finish the replace), or have the start screen offer "Protect older cases" whenever `dpapiCases > 0` or a case reports `store-error`.

### L1 (Low) Restore staging file is never swept after a crash
- Repro: kill the app during a restore. `restore-<hex>.part` stays in the cases root forever, one per crash, up to the size of the case file.
- Cause: the deferred `os.Remove` only runs on handled outcomes. Nothing removes stale `restore-*.part` files at start or at the next restore.
- Evidence: `TestBug_RestoreStagingLeftoverIsNeverSwept` (Go, fails).
- Fix: sweep `restore-*.part` older than a minute when the case store opens or at the start of a restore.

### L2 (Low) Set-up compares the two key entries as raw strings, not as NFC
- Repro: type or paste `cafe` plus a combining accent in one field and the composed letter in the other (Mac keyboard against a password manager). The two look identical.
- Cause: `setupProblem` uses `pass !== confirm`. The server folds both to NFC and accepts them (`TestP06_SetupAcceptsNFCAndNFDSpellings`), so the button stays off for a pair the server would take, and the screen says the keys do not match.
- Evidence: `bug: set-up treats an NFC and an NFD spelling of the same key as a mismatch` (web, fails).
- Fix: compare `pass.normalize('NFC') !== confirm.normalize('NFC')`, in both `setupProblem` and the `mismatch` derived value in `MasterKeySetup.svelte`.

### L3 (Low) Nothing warns about a space at either end of the key
- Repro: paste a key with a trailing space into both fields. It is accepted and is part of the key; it will not match when typed by hand later.
- Cause: the server never trims (correct, guarded by `TestP06_TrailingWhitespaceIsPartOfTheKey` and `TestP06_WhitespaceAtTheEndsIsNeverTrimmed`), but `strengthHint` is silent about it.
- Evidence: `bug: the strength line does not warn about a space at either end of the key` (web, fails).
- Fix: add one hint line when the key starts or ends with whitespace, saying the space counts.

### L4 (Low) Full-width letters in a case code read as "does not match"
- Repro: type or paste the case code with an IME in full-width mode.
- Cause: `normaliseCode` folds case, spaces, dashes and invisible marks but not compatibility letters. The result is the generic not-found sentence, so the person cannot tell why.
- Evidence: `bug: a case code typed in full-width letters is not recognised` (web, fails).
- Fix: `input.normalize('NFKC')` before the cleaning step (the id alphabet is ASCII, so this cannot make a wrong code match).

### I1 (Info) The rule check lets control characters through
`CheckPassphrase` accepts a key ending in a newline, carriage return or tab (kept as typed, not trimmed). A browser field cannot send one; only a direct API call can. Characterised in `TestP06_WhitespaceAtTheEndsIsNeverTrimmed`.

### I2 (Info) After a backend restart the open tab holds a stale session token
Every call answers 401 and the screens show the "open Unravis from the link it printed" message, by design. The tab recovers only by opening the new link. Not a defect, but worth a line in the hand-over notes.

### I3 (Info) Case id alphabet includes l and o
The id pattern is `[a-z2-7]`, so `l` and `o` are valid and `0`, `1`, `8`, `9` are not. A typed `1` or `0` is refused rather than guessed.

### I4 (Info) The restore endpoint refuses a hostile KDF header before deriving
Oversized memory, zero threads and similar header values are rejected by `params.Validate()` in well under a second (`TestP06_RestoreHostileKDFHeaderIsRefusedFast`).

## What holds (guard tests, all passing)

Go, internal/server:
- `TestP06_RewrapRecoversFromACutAtEveryStep` (3 subtests), `TestP06_RewrapCutBeforeOrAfterTheReplacesStillOpens` (2), `TestP06_RewrapWhileACaseIsOpen`
- `TestP06_ExportThrottleAfterFiveWrongKeys`, `TestP06_UnlockThrottleAndRestartReset`
- `TestP06_SetupAcceptsNFCAndNFDSpellings`, `TestP06_TrailingWhitespaceIsPartOfTheKey`
- `TestP06_RestoreTheSameFileTwice`, `TestP06_RestoreRefusalsLeaveNothing`, `TestP06_RestoreCorruptionSweep` (empty, truncated, bit-flipped, renamed non-case, huge), `TestP06_RestoreHostileKDFHeaderIsRefusedFast`
- `TestP06_ExportOfClosedErasedAndUnknownCases`, `TestP06_ExportDestinationOddities` (existing file, directory, UNC, device names, trailing dots, very long), `TestP06_ExportToAFolderNeverOverwrites`
- `TestP06_OddCaseIDSpellings`, `TestP06_LockDuringExportLeavesNoHalfFile`

Go, internal/masterkey: `TestP06_KeyDerivationFoldsCompositionOnly`, `TestP06_WhitespaceAtTheEndsIsNeverTrimmed`, `TestP06_DamagedKeyFileNeverUnlocksOrHangs`.

Web (15 passing `p06:` tests): case code cleaning and refusal, one sentence for every code refusal, abort passthrough, code field `maxlength` of 64, restore path normalisation and checks, restore and unlock failure mappings, Retry-After parsing, set-up validation order, lock state helpers, screen flow after a restart (a locked key cannot be skipped by a deep link, `unlock` cannot reach `setup`), key cleared on read and double-submit guard.

## Counts

- Go: 2 failing (`TestBug_HalfRewrappedCaseIsUnopenable500`, `TestBug_RestoreStagingLeftoverIsNeverSwept`, both intended); 20 P06 guard test functions (17 server, 3 masterkey) and every other test pass. `go vet ./...` is clean.
- Web: 4 failing (`bug:` tests, intended); 15 passing `p06:` tests. The full suite is 1321 tests: 1317 pass, 4 fail (the four `bug:` ones).

## Not checked

- Real Windows UNC and device paths on a real share, and a real power cut during re-wrap: the cut is simulated through `RewrapOptions.Step`.
- In-browser double-clicks and Back/Forward on the unlock and restore cards: only the screen machine and source guards were tested; no browser or server was started, as instructed.
- A real backend restart with a screen open (new session token): read from `api.ts` only.
- Lock during an import in progress (only lock during export was tested).
- Restore files beyond the corruption sweep sizes (no multi-gigabyte file was made).
