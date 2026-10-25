# Phase 03: master-key
risk: high — reasons: key handling and a change to how every case opens (security, data, reversibility); a wrong move can lock the user out of their cases.

## Outcome
One master key (a passphrase the user chooses for this PC) protects every case key. Cases sealed by Windows DPAPI keep opening and can be re-wrapped. A case can be exported to one encrypted file and restored on another PC, always as a new case (R3, R15, R16). The backend exposes status, set-up, unlock, lock and re-wrap routes, and refuses case routes with 423 while locked. No UI yet (phase 04).

## Tasks
| Task | Risk | Depends on |
|---|---|---|
| TSK-09 passphrase protector (Argon2id + AEAD, session keyring) | high | — |
| TSK-10 key file v2 and per-case re-wrap | high | TSK-06 (shared `sqlite` package), TSK-09 |
| TSK-11 master-key HTTP API and lock gate | high | TSK-10 |
| TSK-12 case-file export and restore | high | TSK-10, TSK-11 |

## Edges
TSK-11 and TSK-12 add routes in separate files; TSK-12 goes second because its routes use TSK-11's lock gate; both call TSK-10's open/re-wrap functions. TSK-10 edits `internal/casestore/sqlite/` after TSK-06 has landed there; do not run them at the same time.

## Review focus
- Wrong, empty or very long passphrase; the same answer and timing class for every failure (no oracle).
- Key bytes in logs, errors, panics, JSON, URLs, temp files or the crash dump path.
- Re-wrap interrupted halfway (power cut, disk full): the old key file must still open the case.
- Export file tampered, truncated, replayed into a different case, or restored twice (must create two cases, never overwrite).
- A locked app must not leak case titles or counts.
- Passphrase typed with different Unicode normalisation (NFC vs NFD) must still unlock.

## Phase proof
`go test ./internal/sealedfile/... ./internal/casestore/... ./internal/server/...` green, including a v1 (DPAPI) fixture that still opens, a re-wrap round trip, and an export → restore round trip into a second data folder.
`-race` needs gcc and is not available here: report it as UNMEASURED, never as passed.
Commit: one commit for the phase, after a stop. Never push.
