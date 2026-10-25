# TSK-21 L4: a planted Windows sealed case folder after the master key exists

## Finding

Once a master key exists, a same-user process could write a complete case
folder whose `case.key` is a header-less DPAPI blob (the old kind). It opened
without the passphrase and listed beside real cases. A real DPAPI case (made
before the master key and not yet re-wrapped) is legitimate, so DPAPI cases
cannot simply be refused.

## Design

A "legacy set" records which DPAPI cases were on this PC when the master key was
set up. While a master key exists, a DPAPI case opens only if its id is in the set.

- File: `legacyset.dat` next to `masterkey.dat`. Content is one blob sealed with
  the unlocked Keyring (`Keyring.Seal`, AAD label `unravis/masterkey/legacyset/v1`,
  AES-256-GCM), holding `{"v":1,"ids":[...]}`. Written with
  `sealedfile.WriteAtomic`. A process without the passphrase cannot add an id, and
  ids are not readable on disk.
- Filled at set-up (the `setup` route enumerates the DPAPI cases that exist then,
  before the Keyring is installed) and at the first unlock of a master key that
  has no set file yet (migration for a master key made by an older build).
- Shrinks: a successful re-wrap calls `LegacyDone(id)`, which removes the id from
  memory and rewrites the file. A failed write is harmless (the stale id names a
  case that is now master-key sealed).
- Enforced in the store through two new `sqlite.Options` callbacks:
  `LegacyCheck(id)` (nil = allow, `ErrUntrustedLegacy` = refuse, `ErrLocked` =
  cannot decide now) and `LegacyDone(id)`. The store stays decoupled from the
  session. With no callback (no master key wired) behaviour is exactly as before.
  `casestore.ErrUntrustedLegacy` is the new typed error.
- Where the gate applies (only when the key file header says DPAPI):
  - `Open`/`load`: before `healHalfRewrap` and before any key or database read.
  - `readCaseFiles`: checked again by the kind actually opened, under the folder
    lock, so a key file swapped in after the first check cannot slip through (keys
    are zeroed on refusal).
  - `CloseCase` and `Rewrap`: a planted case cannot be laundered into a
    master-key case by "Protect older cases".
  - `List`: a refused case is listed, flagged `Untrusted` with the fixed title
    "(untrusted case)"; its manifest is not read. Only `ErrUntrustedLegacy` flags a
    case; a locked check (`ErrLocked`) leaves `List` as it was, so the startup
    `Sweep`, which runs before unlock, still reads DPAPI manifests.
  - `Erase` is not gated: the person can still erase a planted folder.
- Half re-wrapped real cases: a cut re-wrap leaves a master-key manifest and a
  DPAPI key file. If the id is off the set but the manifest opens under the
  master key and names the id, only the owner can have written it, so the case is
  allowed and heals as before.
- Fail closed: the session keeps the set in memory (`lgIDs`); `nil` means "not
  loaded or unusable" and refuses every DPAPI case. This happens when the file is
  damaged (does not open under the Keyring, truncated, wrong key, not a regular
  file), cannot be read, or cannot be written at init. Nothing in a case folder is
  ever deleted or rewritten by this logic, and a damaged `legacyset.dat` is left
  as found. `lock()` clears the set. While locked, `LegacyCheck` returns
  `ErrLocked` and the cases routes already answer 423.
- Cases made after set-up or restored from a case file are master-key sealed and
  never consult the set.

## Files

- `internal/masterkey/legacyset.go` (new): `WriteLegacySet`, `ReadLegacySet`,
  `LegacySetPath`, `LegacyState`.
- `internal/casestore/store.go`: `CaseInfo.Untrusted`, `ErrUntrustedLegacy`.
- `internal/casestore/sqlite/sqlite.go`: `Options.LegacyCheck/LegacyDone`,
  `legacyGate`/`legacyCheck`, gate points above, `LegacyDone` after `rewrapDir`.
- `internal/server/masterkey.go`: set state and `LegacyCheck`/`LegacyDone`/
  `loadLegacy` on `MasterKeySession`; `setup` and `unlock` load it before the
  Keyring is installed; `legacyCandidates`; `dpapiCaseCount` returns an
  untrusted count; `rewrapOne` reason `untrusted-case`.
- `internal/server/cases.go`: `writeStoreError` maps `ErrUntrustedLegacy`;
  `caseDTO.untrusted`.
- `cmd/audit-explorer/cases.go` (outside the three named packages, a one-line
  wiring edit; without it the gate would never run in the real app):
  `LegacyCheck: session.LegacyCheck, LegacyDone: session.LegacyDone`.
- Test wiring (one line each): `case_file_test.go`, `e2e_wizard_test.go`,
  `masterkey_test.go`, `p06_clumsy_test.go` now pass the two callbacks to the
  stores they build, so the existing server tests run through the gate.

## API effect

- `GET /api/cases` entries gain `"untrusted": true` (omitted otherwise) for a
  refused folder, with title `(untrusted case)`.
- Any route that opens or closes a refused case answers
  `409 {"reason":"untrusted-case"}` with a plain message. Erase still works.
- `GET /api/master-key/status` gains `untrustedCases` (omitted when 0, always while
  locked). Untrusted cases are not counted in `dpapiCases`, so "Protect older
  cases" never offers them.
- `POST /api/master-key/rewrap`: a refused case reports `status:"failed"`,
  `reason:"untrusted-case"` and is not changed.
- Status codes otherwise unchanged; locked/damaged still 423.

## Tests

New (`TestP06_`):

- `internal/masterkey/legacyset_test.go`: round trip, file holds no id or JSON key
  in plain, absent / garbage / truncated / flipped / other-key / empty / directory
  are absent or damaged, locked and nil ring give `ErrLocked`.
- `internal/casestore/sqlite/p06_legacy_test.go`: planted case refused by
  Open/Rewrap/CloseCase with a poisoned database (never read), listed as
  untrusted, kind unchanged, still erasable; listed case opens, re-wraps, calls
  `LegacyDone`; half re-wrapped case off the set still heals; no `LegacyCheck` =
  unchanged; locked check neither flags nor opens.
- `internal/server/p06_legacy_test.go`: set up through the route, planted case
  after it is refused on GET/close, flagged in the list, counted untrusted, not
  laundered by rewrap-all, erasable, real case re-wraps and opens; set survives a
  restart, shrinks on re-wrap, bytes hold no id; damaged set fails closed and
  touches nothing; session with no set refuses and with no master key allows; no
  master key = legacy behaviour.

Run (toolchain `.dev\toolchains\go\bin\go.exe`): `gofmt` clean on all touched files
(CR stripped for the CRLF working copy), `go vet ./...` clean,
`go test ./... -count=1` all packages ok (`internal/sampler` passed first time).

## Deviations and residual risk

- "Missing set while a master key exists fails closed" is only partly met. A
  missing file at unlock is indistinguishable from a master key made before this
  file existed, and the design requires recording the present DPAPI cases at first
  unlock for those. So a process that deletes `legacyset.dat` and plants a case
  before the next unlock gets the planted case recorded. A damaged or unreadable
  file, a failed write, and a session that never loaded a set all fail closed.
  Closing the gap needs a marker the attacker cannot drop (for example a flag in
  `masterkey.dat`, a format change left out of this fix).
- A planted case made before the master key was set up, or between a legacy
  master key's creation and its first unlock after this build, is recorded as
  legitimate; nothing can tell it from a real old case.
- Recovery from a damaged set: real old cases stay refused until the person removes
  the damaged `legacyset.dat` (the next unlock then re-records the DPAPI cases
  present). No folder is lost meanwhile. A UI hint was not added (Go only).
- The set holds ids only; an id is not secret once the folder exists, but its
  presence is not readable by a process without the passphrase.
