# P04 clumsy-user review (master key, case files, start screen)

Branch: feat/cold-start-wizard. Reviewer stance: hostile, hurried user. No source files were edited.

## Test state

- Go: `go test ./internal/... -count=1` -> every package ok except `internal/server`, where exactly 4 `TestBug_*` fail on purpose. All `TestP04_*` (12) pass.
- Web: `npm test` -> 1179 tests, 1168 pass, 11 fail. All 11 are the `bug:` tests in `web/tests/p04.clumsy.test.cjs`. No pre-existing failures.
- New files only:
  - `internal/server/p04_clumsy_test.go`
  - `web/tests/p04.clumsy.test.cjs`
- Live check: `.dev/unravis-test.exe --port 18765 --no-browser`, scratch `AUDIT_CONFIG_DIR`, scratch token. The server has been stopped. Nothing under `%LOCALAPPDATA%\APIAuditExplorer` was touched.

## Severity counts

- High: 0
- Medium: 2 (F1 focus loss, F2 stuck on damaged key file)
- Low: 9 (F3 to F11)
- Info: 9 (I1 to I9)

---

## Findings

### F1 (Medium) Focus is not returned after a failed unlock, a bad code, or a refused restore

- Repro: keyboard only. Open the unlock screen, type a wrong key, press Enter. The error shows but the cursor is gone. The same happens with a code that matches no case and with a refused restore (key or path field).
- Cause: `busy` is cleared only in `finally`. `focus()` runs in the catch path while the input is still `disabled={busy}`, so the focus call is a no-op on a disabled element.
- Evidence: tests `bug: after a wrong key on the unlock screen...`, `...after a code that does not open a case...`, `...after a refused restore...` (source detector `focusRunsWhileDisabled`, from `busy = true` to `finally`).
- Fix: clear `busy` before focusing (or `await tick()` after clearing it), or use `readonly` instead of `disabled` while busy.

### F2 (Medium) A damaged, empty or replaced `masterkey.dat` strands the user

- Repro (verified live): lock the app, overwrite `masterkey.dat` with `garbage`. Status says `locked`. Unlock with the correct passphrase gives 401 `wrong-key`. Setup gives 409 `already-set`. The wrong-key counter then throttles the user too. The screen says "That is not the master key for this PC", which is untrue, and there is no way forward in the UI.
- Variant: `masterkey.dat` is a directory. Status says `none` but setup answers 409 `already-set`.
- Evidence: `TestBug_DamagedKeyFileLooksLikeAWrongPassphrase`, `TestBug_KeyFileAsDirectoryStatusAndSetupDisagree`.
- Fix: distinguish "file unreadable or corrupt" (new `reason`, for example `key-file-damaged`, 500 or 409) from a wrong key. Make status and setup agree (status should read `damaged`). Offer a documented recovery path in the UI (restore from case files, or reset key with a warning that DPAPI cases stay unreadable).

### F3 (Low) Unlock screen blames the key for failures that are not a wrong key

- Network failure, 429 (the try was never judged), 409 `not-set` and 500 all show "That is not the master key for this PC."
- Evidence: tests `bug: the unlock screen blames the key when the try was never judged...` and `bug: an unlock that the server refuses for a reason other than a wrong key...`.
- Note: the existing `masterkey.test.cjs` pins the single-sentence behaviour, so this is a design call. If the single sentence is deliberate, update that test.
- Fix: keep the wrong-key sentence for 401 `wrong-key` only. Use "Could not reach the app" for network errors, wait text only for 429, and a generic "Something went wrong" for 409 and 5xx.

### F4 (Low) Double-click on Lock resets where unlock returns to

- Repro: double-click Lock while working. The second reply sets `unlockOrigin` to `start`, so after unlocking the user lands on the start screen and loses their place.
- Cause: `lockCases` has no re-entry guard and sets `unlockOrigin` after the await.
- Evidence: test `bug: a double-click on Lock sends two locks...`.
- Fix: guard re-entry, or set the origin before the await and only when currently unlocked.

### F5 (Low) Invisible or blank passphrases are accepted as the master key

- 12 spaces, 12 ZWSP or 12 U+3000 pass client and server (>= 12 runes after NFC) and become the key. Such a key cannot be seen or remembered.
- Evidence: `TestBug_InvisibleOrBlankPassphraseIsAccepted`, `bug: the set-up form accepts twelve blanks...`.
- Fix: reject passphrases that are blank after trimming whitespace and format characters (Unicode categories Z*, Cf, Cc), or require some minimum count of non-blank runes.

### F6 (Low) Client length check counts code points before NFC

- Twelve decomposed code points normalise to 6. The button enables, then the server answers `too-short`.
- Evidence: `bug: the set-up button counts a decomposed passphrase before the server normalises it`.
- Fix: normalise to NFC in `canSubmitSetup` before counting.

### F7 (Low) Setup validation failures count toward the setup throttle

- Five typos (mismatch or too short) give a 429 with growing waits, which locks a fresh user out of setting up. Found when the boundary test hit 429 on its twelfth-emoji success.
- Fix: count only failures of an actual secret check, or use a looser budget for validation errors.

### F8 (Low) Bidi controls are not stripped from pasted codes

- U+202A-202E, U+2066-2069, U+061C (chat apps and Explorer's "copy as path" add these) make a valid code fail.
- Evidence: `bug: bidi marks copied from a chat or File Explorer stop a code from matching`.
- Fix: add these ranges to the `IGNORED` regex in `normaliseCode`.

### F9 (Low) A pasted restore path with a leading U+202A is refused

- Explorer's "Copy as path" in some locales adds U+202A at the start. The card then says "not a full path".
- Evidence: `bug: a path copied with its leading direction mark (U+202A)...`.
- Fix: strip bidi and format characters in `normalisePath`.

### F10 (Low) Client and server path limits differ

- The form allows 521 to 2048 characters (`MAX_PATH_CHARS` 2048), but the server body limit is 520. The server answers 400 `bad-request`, which maps to the generic "other" message.
- Evidence: `bug: the form lets a path of 521..2048 characters through...`.
- Fix: share one constant (520) and add a "path too long" message.

### F11 (Low) Lone-surrogate passphrases collide

- In JS strings, `\ud800` x12 and `\ud801` x12 both become U+FFFD when serialised, so they are the same key. Two different typed passphrases unlock each other.
- Evidence: `TestBug_LoneSurrogatePassphrasesCollide`.
- Fix: reject U+FFFD / invalid UTF-8 in `masterkey.Setup` and the client.

---

## Info (not a defect or unproven)

- I1: Export has an existence oracle for token holders. A known id with a wrong passphrase gives 401, an unknown id gives 404. Behind the session token, so low risk. Logged by `TestP04_ExportExistenceOracleIsBehindTheToken`.
- I2: A missing restore file still counts toward the restore throttle.
- I3: A restored closed case becomes open, and the title may be duplicated (not verified).
- I4: A lock during an export does not stop the export.
- I5: Passphrase inputs use `autocomplete="off"`. `new-password` / `current-password` would let password managers help and are accepted by browsers.
- I6: `/api/master-key/rewrap` 423 is not signalled to the lock signal. Routes under `/api/master-key/*` are not gated, so this is unreachable today.
- I7: A crash can leave an orphan `restore-*.part`. No cleanup on start (unverified).
- I8: Focus is lost after unlock, restore or "Open" because the component unmounts. The new screen should move focus to its heading.
- I9: The code hint says "26 letters and numbers" but only a-z and 2-7 match.

## Verified to hold (regression tests kept)

- Lock gate: odd spellings, methods and cases leak nothing and give no 5xx (`TestP04_LockedGateOddSpellingsLeakNothing`).
- Concurrent unlock double-click: all succeed.
- Restore double-click: one case, no debris. Lock racing a restore ends in a clean 201, 423 or 409.
- Passphrase length boundaries: 11 vs 12 characters, 1024 vs 1025 bytes, 12 emoji.
- NFC/NFD passphrase equivalence across PCs for case files.
- No passphrase or path in logs on case-file failures.
- Windows device names refused by path shape. `.ext` case and spaces, forward-slash paths, zero-byte key file (no panic).
- Live checks: 11-character setup gives 400; no token 401; bad Origin and bad Host 403; 20 KB body 413; locked list 423; wrong or empty unlock 401; right unlock 200.
- Web: passphrase only in the body (never URL, header, storage, console). Key cleared before the request is sent. Double-submit guards. The screen-move table forbids bypassing unlock. Locked start view shows nothing. Empty and huge lists fine. No `{@html}`. `truncateTitle` is surrogate-safe. 401 `wrong-key` keeps the session, other 401 forgets it. 423 raises the lock signal.
