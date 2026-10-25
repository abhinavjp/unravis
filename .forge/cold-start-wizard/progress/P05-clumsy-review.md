# P05 clumsy-user review (create wizard: shell, sources step, review/create/import)

Branch: feat/cold-start-wizard. Reviewer stance: hostile, hurried user and a hostile local page. No source files were edited.
Scope: TSK-17 (shell), TSK-18 (sources step), TSK-19 (review, create, import).

## Test state

- Web: `npm test` -> 1297 tests, 1290 pass, 7 fail. All 7 are the `bug:` tests in `web/tests/p05.clumsy.test.cjs`. No pre-existing failures. The same file holds 17 passing `p05:` tests.
- Go: not run. There is no `go` on PATH or in the usual install folders on this machine, so `go vet ./...` and `go test ./... -count=1` could not be executed. No server defect was found by reading `internal/server` (case_sources.go, server.go addSource and writeRepoError), so no Go test file was added. Run both commands where Go is installed before merging.
- New file only: `web/tests/p05.clumsy.test.cjs`. Production code, `%LOCALAPPDATA%\APIAuditExplorer` and any running instance were not touched. No server was started and no browser was driven (see I9).
- How the tests run: the store files (`wizard.svelte.ts`, `wizardSources.svelte.ts`, `wizardCreate.svelte.ts`) load with `$state(v) => v`, one shared module cache, and a fake server that remembers what it was told even when a reply is lost.

## Severity counts

- High: 0
- Medium: 1 (F1 scan lost, Create still runs)
- Low: 5 (F2 to F6)
- Info: 9 (I1 to I9)

---

## Findings

### F1 (Medium) "Scan again" (or a cancelled or failed rescan) with scan sources chosen: Create makes the case, then fails every scan source with "No period was chosen to read."

- Repro: scan, tick a log set, press "Scan again", go to Review (Next is not blocked while a scan runs), tick the notice, press Create. Same with a cancelled or failed rescan.
- Cause: `startScan` sets `scanId = ''` and `phase = 'scanning'`. `blockers()` only reports `scan-stale` when the phase is `'done'`, so it stays empty. `buildInput()` passes `scanId: ''`, so `importAll` marks every scan row `no-window` ("No period was chosen to read."). The case and the tags already exist by then. Once the case is made, Back and Leave are disabled and the imports have no retry button. When the rescan finishes, `reconcileWithScan` silently drops chosen sources the new scan no longer offers. The only notice for that is an `info` line shown in the gallery, which the person has left.
- Evidence: tests `bug: a rescan in progress, with scan sources chosen, must stop Create (blockers() is empty)` and `bug: Create during a rescan makes the case, then fails every scan source with "no-window"`. Observed calls: `createCase, tag:W3SVC...`, failure `[{"step":"import","reason":"no-window"}]`.
- Fix: add a blocker (for example `scan-busy`) when scan sources are chosen and the phase is not `done` or `scanId` is empty. Show its hint on the Review step. Show the "taken off your list" note on Review as well, or keep the chosen sources through a rescan until the new scan completes. Give `no-window` an honest line when the cause is a missing scan.

### F2 (Low) A lost reply to "create the case", then Create again, makes a second case

- Repro: the server commits the case, the reply is lost (timeout, network). `createError` is set, `caseMade()` stays false, the Review step shows the error and Create is open. A second click calls `createCase` again.
- Cause: `submitWizard` has no way to learn that the first call landed. The title is the only identity and titles may repeat. `titles` is read once on mount, so the repeat warning does not appear either.
- Evidence: `bug: a lost reply to createCase, then a second click on Create, makes a second case on the server` (server holds 2 cases). Rare on localhost, but it leaves a duplicate case that holds nothing.
- Fix: before a retry of the case step, list cases and reuse one with the same title created in the last minute (or send a client-made idempotency key). Refresh `titles` after a failed create.

### F3 (Low) A server error from the connection test or the audit-source add is told as "The server could not be reached."

- Repro: test several Mongo connections in step 2 (the server allows 10 tests a minute, shared with the submit step), then Create. The submit-time test, or any of the later adds past 20 a minute, answers 429 or 400.
- Cause: `reasonOf` returns `error.reason` only for `CaseApiError`. `api.testConnection` and `api.addSource` throw `ApiError` (a different class), so every such failure becomes `'unreachable'`. A 423 `ApiError` would not be treated as locked either, though the server does not return 423 on those two routes today.
- Evidence: `bug: an ApiError (429 too many connection tests) from the tag or audit step is reported as "unreachable"`. The tag and the audit source of that database then fail, and there is no retry once the case exists.
- Fix: map `ApiError` in `reasonOf` (429 to a "wait a minute" reason, 400 to a validation reason, a network failure to `unreachable`). Cover the lock case for completeness.

### F4 (Low) The header Lock on the create screen returns to the start screen, not to the case being made

- Repro: open the create guide, fill it in (or start Create), choose Lock in the header menu, unlock. You land on the start screen.
- Cause: `lockCases` sets `unlockOrigin = screen === 'running' ? 'running' : 'start'`. The 423 path (`noteLocked`) keeps `create`, and `afterUnlock('create')` returns `create`. The two lock routes disagree.
- Evidence: `bug: Lock on the create screen returns to the start screen, not to the case being made`. The draft is not lost: it is resumed when "Create a new one" is chosen again, and a made case then shows progress.
- Fix: `unlockOrigin = screen === 'running' || screen === 'create' ? screen : 'start'`.

### F5 (Low) `hostLabel` can show the tail of a password

- Repro: type `mongodb://db1:27017/?tlsCertificateKeyFilePassword=hu@nter2` as the address. The connection card shows `nter2`.
- Cause: `hostLabel` takes everything after the last `@` in the whole string, before it looks for the end of the authority. An `@` inside an option in the query string moves the "user-info end" into the query.
- Evidence: `bug: hostLabel shows the tail of a password that holds an @ inside a connection option` (label was "nter2").
- Fix: find the authority end first (the first `/`, `?` or `#` after the scheme), then take the part after the last `@` inside that authority only. Keep handling a password that holds an unencoded `/`.

### F6 (Low) A database name with a doubled space or a format character is tagged under a different label

- Repro: a Mongo database named `audit  main` (two spaces). Classify it as audit and Create.
- Cause: `makeSource` runs `cleanTag(label)`, which collapses whitespace and removes format characters, so the tag POST sends `audit main`. The server only accepts the exact tested name (`mongoTests.has(label)`) and answers `database-not-tested`. The audit-source add uses the raw name and would succeed, so the two halves disagree.
- Evidence: `bug: a database name with a doubled space is tagged under another label than the one the server was tested with`.
- Fix: for `mongo-database`, keep the database name as the label (still refuse secret-looking text), or send it exactly as tested. Show a short line when a name cannot be tagged.

---

## Info

- I1. After the case exists, a failed tag, audit source or import has no retry button. Back and Leave are disabled and the only action is "Open case" with a hint to use Add more data. This is by design (retry only after a lock), but it makes every failure above permanent for the run.
- I2. `resetWizard()` during an in-flight run would let a second case be made. It is unreachable today because every exit is disabled while `busy`, so keep that guard if anyone adds a "Cancel" or an auto-reset.
- I3. `submitWizard` does not dedupe `input.sources` itself. The draft store dedupes by `sourceKey` (tested), so it is only a risk for a future caller. Read, not run.
- I4. A held Mongo password stays in module memory while a draft is parked with Home, and also when the test returned no databases. It is released by `resetWizard`, by removing the connection and after a created case opens. No secret reached the draft snapshot, the run state, the sources view or any error text in the tests.
- I5. `redactMessage` skips secrets shorter than 3 characters, and a URI-embedded password is only removed as part of the whole address. A short password echoed alone by a server would show. The `://` pattern sweep covers the usual case.
- I6. `titles` (for the "same name" warning) is read once per wizard mount, so a case made elsewhere meanwhile is not warned about.
- I7. The auto-open after a clean run is an `$effect` inside `CreateProgress`. If that component is unmounted before the run ends, the case does not open by itself. No path does this today.
- I8. A tag POST retry duplicates a non-mongo tag if a reply is lost and the step is run again (the server does not dedupe). Retry only happens after a lock, where the server refused the call, so it is latent.
- I9. No browser run was done. Keyboard-only order, screen-reader announcements and mobile width are source-reviewed only (see "Verified to hold" for what the source shows). The 8-source analysis limit is applied by the app (`info.limits.maxSources`) and the server accepts more audit sources than one analysis reads; this was not exercised through the UI.

---

## Verified to hold

Each item is a `p05:` test unless marked "read".

- Double click on Create: one case, the second call returns null.
- 423 while creating the case: the draft is kept and the retry makes the case once. 423 during the imports: unlock and retry make no second case and repeat no tag.
- The same finding ticked twice is one source. Case files, unverified findings and findings without an importer cannot be ticked.
- The draft holds at most 200 sources, the 201st is refused and a repeat collapses to the first.
- Titles: blank is refused, emoji and RTL text are accepted and counted by character, format and control characters are refused, 500 characters is too long.
- Environment names: at most 32, at most 60 characters, repeats collapse without regard to case, the chevron and control characters are removed.
- The address box refuses `@`, `/`, `password=`, a scheme and a bad port, and takes a plain host and port.
- A failed Mongo test shows no password, user name or address. A 5000-entry, odd-typed or null database list is bounded (at most 200 names of at most 120 characters) and tolerated.
- `resetWizard` clears title, sources, run state, the scan, the notice and every held connection.
- The draft snapshot, run state, sources view and result hold no connection secret, even when the server text carries one. A failed step stores a reason code, never the server text or a local path.
- No wizard component uses `{@html}` or `innerHTML`, so a label or path with markup is only printed as text (source check of the eight wizard components).
- While a case is made: Back, Next, the step buttons and Leave are disabled, Escape only keeps working, and a reload is warned about (`beforeunload`). Browser Back leaves the page only through that warning, as the screens are not history entries (read).
- The progress box is `role="status"` and takes focus at the end, failures are in `role="alert"`, and the Create button is gone once the case exists. Long labels wrap (`overflow-wrap: anywhere`) and the layout uses wrapping flex and grid (read, for the mobile width).
- An empty selection makes an empty case without the notice.
