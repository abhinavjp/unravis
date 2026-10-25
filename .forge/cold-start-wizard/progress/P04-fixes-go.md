# P04 Go-side fixes (F2, F5, F7, F10 server, F11)

Status: DONE. `go vet ./...` clean, `go test ./... -count=1` all ok (no -race). Nothing committed. No web/ files touched.
All four `TestBug_*` renamed `TestP04Fixed_*` (strengthened, not weakened). Failure body shape is unchanged everywhere:
`{"error":<text>,"reason":<code>,"state":"none|locked|unlocked|damaged","retryAfterSeconds":N?}`.

## State: new value `damaged`
`GET /api/master-key/status` -> `{"state":"none|locked|unlocked|damaged","dpapiCases":N}` (dpapiCases is 0 when damaged).
`damaged` = something is at `masterkey.dat` that no passphrase can open: zero bytes, > 4 KiB, not JSON, unknown version/kdf, out-of-bounds Argon2 params, bad salt/verifier, or not a regular file (directory, symlink). A well-formed file with a wrong passphrase is still `locked` + 401 `wrong-key`. Plain I/O errors (sharing violation) are NOT reported as damaged.
An unlocked session stays `unlocked` even if the file is damaged behind it; it shows `damaged` after the next lock/restart.

## Behaviour while `damaged`
- `POST /api/master-key/unlock` -> **409 `key-file-damaged`** (state `damaged`). No Argon2, never counted by the unlock throttle. (Was: 401 wrong-key + throttled.)
- `POST /api/master-key/setup` -> **409 `key-file-damaged`** (was 409 `already-set`, or `none` for a directory). The file is never overwritten.
- All `/api/cases*` routes -> 423 `{"error":"locked","reason":"locked"}` (same as locked; the web lock signal fires, then a status call shows `damaged`). New cases cannot be created.
- `POST /api/master-key/rewrap` -> 423. `POST /api/master-key/lock` -> 200 with `state` = current state (`damaged` rather than a hard-coded `locked`).
- Error text: "The master key file on this PC is damaged or has been replaced, so no passphrase can open it."

## Recovery route (new): `POST /api/master-key/reset-damaged`
Separate from setup, token + local Origin like every route. Request `{"acknowledged":true}` (only that field; `Content-Type: application/json`).
- 200 `{"state":"none","dpapiCases":N,"keptAs":"masterkey.dat.damaged-<UTC timestamp>"}`
- 409 `not-damaged` - state is not `damaged` (checked first, under the op lock, so a good key file, or an unlocked session, is never touched; also returned for a second call)
- 400 `acknowledgement-required` (missing/false) ; 400 `bad-request` (unknown field, wrong type, trailing data) ; 415 `bad-content-type`
- 500 `reset-failed` (rename failed) ; 503 `master-key-unavailable`
Design: nothing is deleted. The damaged file/folder is renamed to `keptAs` in the same data folder (name only, no path in the reply), so a file that was in fact fine (restored from a backup) can be put back under its old name. After reset the state is `none` and the normal `setup` route creates a new key. Cases sealed with the OLD master key stay on disk but unreadable under the new one (new salt). The web UI must show this warning before sending `acknowledged:true`, e.g. "Cases protected by the old master key will stay unreadable. The damaged file is kept aside, not deleted." Also tell the user that DPAPI-sealed cases are unaffected.
No throttle on this route (it answers no secret question).

## F5 blank / invisible passphrases (setup)
New reason **400 `blank`**: fewer than `MinVisibleLen = 8` visible characters (after NFC). Visible = not space/control/format (Zs Zl Zp Cc Cf), not surrogate/private/unassigned, not a variation selector, not a filler (U+115F, 1160, 3164, FFA0, 2800, 180E, 034F, 17B4, 17B5). Total length stays >= 12 runes (`too-short`). So 12 spaces / ZWSP / U+3000 / bidi marks are refused, `"a b c d e f g h"` (8 visible) is accepted. Constant lives in `masterkey.MinVisibleLen`; one-line change if you want a different bar. Web should mirror: count runes that are not whitespace/format/control, need >= 8.
Order of setup validation: ack -> mismatch -> invalid-characters -> too-short -> blank -> too-long.

## F11 invalid text (setup)
New reason **400 `invalid-characters`**: the NFC passphrase is not valid UTF-8 or contains U+FFFD (that is what a lone `\ud800`/`\ude00` escape decodes to, so two different typed strings used to collide). Raw invalid UTF-8 bytes or a bad `\u` escape on the wire still give 400 `bad-request` (a browser cannot send those). Unlock with such text just fails as `wrong-key`. The web client should refuse lone surrogates (`/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/`) and U+FFFD before sending.

## F7 setup throttle
Setup validation failures (`acknowledgement-required`, `mismatch`, `too-short`, `blank`, `invalid-characters`, `too-long`) are no longer counted. Only a failed key-file write (`setup-failed` 500) counts. The `429 throttled` shape is unchanged.

## F10 server side
Restore/export `path` longer than 520 bytes -> **400 `path-too-long`** ("That location is too long. Use a shorter path.") instead of generic `bad-request`. Web: use limit 520 (bytes of UTF-8; ASCII = chars) and map `path-too-long` to a message.

## Go API (package masterkey)
`ErrDamaged`, `ErrBlank`, `ErrInvalidChars`, `Inspect(dir) FileState` (`FileAbsent|FileUsable|FileDamaged`), `CheckPassphrase`, `Visible`, `MinVisibleLen`. `Unlock` now returns `ErrDamaged` (not `ErrWrongKey`) for a structurally damaged file, before any key derivation; `Setup` returns `ErrDamaged` over a damaged slot and the new refusals. Server const `MasterKeyDamaged = "damaged"`.

## Tests changed
- `internal/server/p04_clumsy_test.go`: TestBug_* -> `TestP04Fixed_InvisibleOrBlankPassphraseIsRefused`, `_LoneSurrogatePassphrasesAreRefused`, `_DamagedKeyFileIsItsOwnState`, `_KeyFileAsDirectoryIsDamagedAndRecoverable`; added `_SetupValidationFailuresAreNotThrottled`, `_DamagedKeyFileKeepsCasesClosed`, `_ResetDamagedKeyFileIsExplicitAndKeepsTheOldFile`.
- `internal/server/masterkey_test.go`: `TestSetupThrottleCountsFailures` -> `...CountsKeyWriteFailures` (drives the counter directly since validation no longer counts). `routes_test.go`: new route row.
- `internal/server/case_file_test.go`: 600-char path now expects `path-too-long`.
- `internal/masterkey/masterkey_test.go`: damaged-file tests expect `ErrDamaged` (was `ErrWrongKey`, the exact conflation F2 reports); added Inspect, directory and blank/invalid-passphrase tests.

## Files
`internal/masterkey/masterkey.go`, `internal/server/masterkey.go`, `internal/server/case_file.go`, `internal/server/routes.go` (+ tests above).
