# TSK-21: End-to-end, security review, clumsy-user review, rebuild
risk: medium — verification and release hygiene.

## Outcome
Cross-phase proof from the plan is shown to pass, the new routes survive a security review, a break-the-app review of the start screen and wizard found nothing high left open, and the binary and both instances are rebuilt and restarted.

## Write scope
- `internal/server/e2e_wizard_test.go` (new Go test driving create → environments → sources → import over the HTTP handler with fixtures)
- `web/tests/clumsy.wizard.test.cjs` (new; pure-model clumsy inputs)
- Fixes inside the owning earlier tasks' scopes only, each recorded as a deviation
- `.forge/cold-start-wizard/phases/06-harden-and-ship/progress.md`

## Must not change
Anything not required by a finding; design decisions (filters, drill-down, theme).

## Changes
1. Go e2e: fixture IIS + log4net folders → scan with verification → import selected findings → events carry the tags → unreadable fixture never selectable; unauthenticated calls to every new route refused.
2. Security pass (subagent, read-only): new routes `locate`, `probe/reachable`, environments, sources, mongo-sources, `master-key/*`, `cases/export`, `cases/restore` — SSRF, path traversal (export destination, restore source), rate limits and unlock throttling, error text leakage, credential and passphrase leakage into logs, memory dumps of handlers, the case db or the case file; AEAD misuse (nonce reuse, AAD), Argon2id parameter bounds, tampered case files.
3. Clumsy-user review (per the standing memory rule): double-click everything, back/forward mid-wizard, blank everything, absurd names, close the tab during import, scan returns nothing, backend restarts; master key: mismatched entries, paste with trailing space, wrong key ten times, lock mid-import, close the app mid re-wrap, restore the same file twice, restore with the wrong key, restore a renamed non-case file; write tests for each break found.
4. Rebuild: `cd web; npm run build`, build the binary, restart 18770 and 18771, browser pane pass per `.dev/browser-pass.md` (desktop 1280, mobile 375, light and Terminal themes).

## Proof
`.\build.ps1` green; e2e and clumsy tests green; security findings: none open at high; browser pass notes saved; UNMEASURED list stated explicitly (live IIS/Redis/SQL probes, `-race`, P4c scheduled task, ETW/.nettrace/.blg parsers, a real second PC for case-file restore).

## Depends on
TSK-20
