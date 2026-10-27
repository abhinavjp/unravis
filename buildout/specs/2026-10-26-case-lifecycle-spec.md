# Case lifecycle and analysis: create, open, archive (spec v7)

Status: v7 (final), 2026-10-27, after review rounds 1 to 5 and the owner rulings of round 6. Rulings are in `2026-10-26-case-lifecycle-decisions.md` (ids R1-n to R6-n are cited in brackets). The untouched first draft is `2026-10-26-case-lifecycle-spec-v1.md`. The plain difference is `2026-10-26-case-lifecycle-v1-to-final.md`.
Wireframe: `buildout/wireframes/case-lifecycle.html` (rebuilt in v7 from the real app's own styles and screens). Whole-workflow walkthrough, cases and analysis step by step (139 screens, including saved connections, the Blackbox recorder, guided versus manual mode, suspects and checklist): `buildout/wireframes/workflow-walkthrough.html`.

## 0. Purpose, scope, words

Unravis is a local tool. An engineer investigates a slow or failing system by gathering evidence (audit logs from MongoDB, IIS logs, app logs, SQL Server and Redis facts, counters) into one **case** and reading the analysis of it.

This spec covers:
1. **Create** a case.
2. **Open** a case (from the list, by code, by restoring a case file, by link, by resume).
3. **Close and reopen**, **archive**, restore from archive, and **erase** (archive is new; nothing like it exists today).
4. Which analyses help, what each needs, and which data is planned but not yet collected.

Words (one meaning each):
- **Case**: one encrypted per-case store on this computer. Id: 26 characters (a-z, 2-7). **Case code**: the id as typed (spaces and dashes ignored). **Short code**: the first 8 characters, typed to confirm an erase.
- **Master key**: protects every case on this computer by default; optional ("Not now" stays). States: `setup` (none yet), `locked`, `unlocked`, `damaged`. **Case key**: an optional key of one case alone, off by default; a case that has one is opened with it instead of the master key (7.6) [R6-2].
- **Source**: one thing a case reads from (an audit database, an IIS log, a file), tagged Environment > Portal > API.
- **Import**: reading one source into the case. **Case file**: one encrypted `.unravis-case` export of a case.
- **Archive record**: the newest item of a case's case-file history; it shows in the case list while the case is Archived (section 7.3).
- **Working copy**: the case's own store on this computer. **Freed**: working copy erased after an archive.
- **Demo mode**: the server runs with sample data and has no case store. Demo and real are separate server runs; there is no switch in flight [R1-9].
- **Evidence**: rows a case holds. **Theories**: saved theories. **Story**: the saved, dated event list of a case.

Out of scope: the Blackbox recorder, remote scanning, multi-user sharing, Linux, changing how imports parse.

## 1. As built today (facts the rest builds on)

- Screens: `loading, unlock, setup, start, create, running, case`. Moves are limited by `screenFlow.ts`; a refused move changes nothing.
- First screen: demo gives `running`; locked or damaged key gives `unlock`; a link with a view, or a remembered tab, gives `running`; otherwise `start`.
- Start screen always shows three cards: use an existing case, enter an existing case, create a new one.
- A case has: id, title, created, closed (a time or none), imports, bytes, untrusted flag.
- API: list and create (`/api/cases`), read and erase (`/api/cases/{id}`; erase body carries the id), close (`/close`), imports and import jobs, events, series, resource, collect, timeline, call-trace, sources and tags, environments, mongo-sources, ledger, workspace, health, sampler, export, and `/api/cases/restore`.
- **Closed** today is one-way: readable, imports refused (`case-closed`, 409), sampler stopped, no reopen, no archive. Erase works.
- **Untrusted** (legacy folder not known when the key was set up): listed, never opened (`untrusted-case`, 409); only erase.
- Restore from a case file always makes a **new** case. A wrong key, path or damaged file gives one fixed sentence.
- A locked key answers 423 on every case route.
- A wrong or malformed case code gives one sentence for every reason.

## 2. Case states and moves

States: **Open**, **Closed**, **Archiving** (transient, section 7.2), **Archived**, **Untrusted** (a flag), **Erased** (gone), and two display states: **Missing** (an Open, Closed or kept Archived case with no mark whose working copy's folder is gone; a Freed case is never Missing; found by a folder check whenever the list loads or a call touches the case) and **Freeing / Erasing** (short marks while the working copy is being removed; every other call on the case answers M19 for Freeing and M5 for Erasing) [R4-2, R4-4, R5-4, R5-6].

| From | Move | To | Rule |
|---|---|---|---|
| (none) | Create | Open | Section 5. A case is never half-made and unnamed. |
| Open | Close | Closed | Asks first. Refused while an import runs [R1-16]. Stops the sampler. |
| Closed | **Reopen** | Open | Allowed [R1-25]. Imports allowed again. The case's saved Mongo link and filters are applied again and the person runs the sampler again when they choose; Reopen never starts it by itself [R6-6]. Written to the story. |
| Open or Closed | Archive | Archiving | Section 7. Imports refused and the sampler stopped while Archiving. Refused if an import runs. Does not close the case first [R2-1]. |
| Archiving | commit | Archived | The one commit point (7.2). Story gets "archived". The record's closed time is none if the case was Open [R3-3]. |
| Archiving | Cancel, failure, lock, crash | the state it was in (Open or Closed) | Nothing is written to the story except "archiving stopped". The sampler is started again if it was running before Archiving [R2-1, R5-3]. |
| Archived (kept) | Restore | Closed | In place, same id; always Closed, even if the case was Open before. Closed time = the restore time; the archive record keeps its own copy of the earlier value, and the list sorts by the restored time [R3-3, R4-1]. |
| Archived (kept or freed) | Restore from the case file | a new case, Closed, new id | Always a new case; may be repeated [R1-4]. |
| Archived (kept) | Free the space | Archived (freed) | Section 7.5. |
| Open / Closed / Archived | Erase | Erased | Section 8. Short code typed. Not while Archiving. |
| Untrusted | Erase | Erased | The only move. |

Every move writes a story entry: created, closed, reopened, archiving stopped, archived, restored, imports stopped [R1-25]. Closing means "stop adding", no longer "forever"; Reopen is the escape from a mistaken Close.

One lock per case on the server serialises every move, Cancel and the archive commit. The first to take it wins; the other gets a 409 "changed" (9.1) [R2-2].

A **stale mark** (an Archiving, Freeing or Erasing mark with no live job, after a crash or a key lock) is repaired by **recovery**. For Archiving the case returns to its prior state (the sampler is started again if it was running before) and its `.part` is removed; a Freeing or Erasing mark is finished (7.5, 8). If an Archiving job left a complete renamed file, its name and folder move to an **unresolved files** list in the index (section 7.7, A13). Recovery runs at start-up, at unlock before the first reply, and, under the lock, at the start of a call that touches a case which carries a mark (so a lock or failure with no restart never strands a mark) [R5-1]; a plain read of a case with no mark runs nothing and writes nothing. The list repairs only the marked rows of the page it returns. A job still unwinding after a key lock counts as live until it exits. So nobody sees a stale mark: a row says "Archiving, n%" only for a live job; Cancel on a recovered case answers M2; Erase and Open act on the state it returned to [R3-1, R4-5, R4-7].

## 3. Preconditions that every flow checks

In this order; the first failure decides the screen:
1. **Server reachable.** If not: "Unravis could not be reached", with Try again; nothing else pretends to work.
2. **Demo mode.** No case store and no master key: the key state is never read, so demo never shows unlock or setup, even if a real key is damaged. Create, open, archive and erase are hidden; analysis opens on sample data; a case link or remembered case tab is ignored and the sample opens [R1-9, R3-8].
3. **Master key.** `setup`: only "Set up the master key". `locked` or `damaged`: the unlock screen; after the right key the person returns to where they were (`afterUnlock`). A key that locks mid-flow keeps the flow's state; nothing is made twice on return. A case with its own key asks for that key when it is opened, and locking it closes only that case; other cases stay as they are [R6-2].
4. **Case store health.** If the list cannot load: say so; Create and Enter stay usable.

## 4. Start screen

- Three cards always: Use an existing case, Enter an existing case, Create a new one. A locked screen names no case and shows no count.
- **Use an existing case**: the list is paged, 50 at a time by the server [R1-20]. Row: title (or "Untitled case"), state chip (Open, Closed, Archived), imports, sources, and one date: "Opened <date>", "Not opened", or "Archived on <date>" [R1-19]. Sort: last activity (latest of created, opened, closed, archived, restored) by server wall clock, newest first [R2-15]. An Archiving row shows "Archiving, n%". Archived cases sit behind an "Archived (n)" toggle: n is the total number of archived cases on the server (kept and freed), not the number loaded, and that list is paged 50 at a time too, with its own sort. Untrusted cases are listed last and do not count toward the 50. A missing case shows "Working copy missing" with Erase [R3-15].
- Zero cases: "No cases yet. Create a new case to begin."
- An untrusted row shows its sentence and Erase only.
- Focus lands on the heading on arrival; Back returns with focus on the card used.

## 5. Create a case

### 5.1 Steps
1. **Name it.** 2 to 80 characters; Next disabled until valid. Hint: no passwords or customer names. Duplicate names are allowed; the two get different codes and the screen says so.
2. **Look around.** Scan of this computer (names and sizes only). Findings with chips: ready, needs a connection, needs a file. States: scanning, finds things, finds nothing (calm, can continue). A scan older than an hour (elapsed server time, not wall clock [R1-19]) is stale and must be repeated.
3. **Connect.** Add connections: MongoDB, SQL Server, Redis, IIS logs, app logs, a file. Each is tested before it counts. Passwords live in memory only. A Mongo connection asks "does it hold API audit logs?"; each database is set to API audit logs, other data, or not used.
4. **Review.** Name, environments, every source with its tags, what will happen to each (read from this computer, read from a file, named only), the how-far-back window, and the notice to tick. Button: "Create case", or "Create empty case" with no sources.

Back is always free; Forward only when the step is valid. Nothing is saved until the final button. Leaving says "nothing is saved until the last step".

### 5.2 Submission order and partial failure
1. Make the case (failure: nothing saved, button stays, "try again").
2. Save environment names.
3. Save each source's tags.
4. Add audit sources to the analysis.
5. Start each import (async jobs, with percent).

The page sends a **request id** (random) with step 1; the server returns the same case for a repeated id [R1-17]. The id is made once per wizard run and kept in the tab's session storage, so a reload keeps it; a new tab or a new run makes a new one. A retry after a failure at step 1 reuses it. The wizard drops its id when the case opens. The server remembers an id for the life of its case; a repeated id returns the case if it is Open or Closed, answers M18 and makes nothing if it is Archiving or Archived, and is forgotten once the case is erased, after which a repeated id makes a new case [R2-13, R3-9, R5-5]. After step 1 the case is **kept** whatever happens. Each later failure names the source and the reason; the case opens with a "needs another try" list in Add more data. That list is stored in the case on the server and survives a reload; each item has its own retry (tags, add to analysis, import). On a Closed case the list shows "Reopen to try again"; on an Archived case it is not shown. The list is part of the case, so it travels in a case file and returns on any restore [R2-14, R3-10]. Nothing is made twice on a retry. A retry of a connection source needs its password typed again; passwords are never stored [R1-21]. The re-run after a reload or restart (C3) never repeats a connection source: its password is gone, so that import is listed as needs another try. If the case has become Closed meanwhile, every remaining step is listed as "Reopen to try again" [R5-5]. An import is one transaction, so a stop or restart in the middle of a file commits nothing: a half-imported file never exists, and the case may only hold fewer files than planned, which the re-run lists [R6-5].

### 5.3 Create scenarios
| # | Situation | Behaviour |
|---|---|---|
| C1 | No sources chosen | Allowed; empty case. |
| C2 | Key locks mid-create | Progress says locked; Continue after unlock resumes without repeating finished steps. |
| C3 | Server stops mid-create | "Unravis could not be reached". On return the page repeats the same request id; the server returns the case from step 1 if it is Open or Closed, answers M18 if it is Archiving or Archived, and makes a new case if it was erased meanwhile. The wizard keeps its plan with the request id in session storage and **re-runs steps 2 to 5**, each idempotent (nothing is made twice), except that connection-source imports are listed as needs another try (their passwords are gone) and a case found Closed gets every remaining step listed as "Reopen to try again" [R5-5]; if the plan is lost (a new tab), the person is offered Open it, and sources not yet added are added from the case home [R1-17, R3-9, R4-9]. |
| C4 | Scan stale or none | Scan-based sources cannot be reviewed; go back and scan. |
| C5 | Connection test fails | Specific reason (unreachable, wrong login, no permission); source not added; Create still valid. |
| C6 | Rate limited on tests | A short pause is asked for, with the wait shown. |
| C7 | File source with no file | Create disabled with the reason. |
| C8 | Window missing / ends before it starts / too long | Create disabled with the reason. |
| C9 | Tag empty, too long, or looks like a password | That source's tags refused; case kept; source listed as needing another try. |
| C10 | Case or disk cap reached | The step says the cap; kept data stays. |
| C11 | Same file added twice | Warned in review; allowed. |
| C12 | Window closed during create | Create continues on the server; the case is in the list on return. |
| C13 | Demo mode | Create hidden, one line says why. |
| C14 | Browser back during wizard | Stays inside the wizard's own history; leaving asks. |
| C15 | Double press on Create | One case (same request id). |
| C16 | Notice not ticked | Create disabled: "Tick the box above to confirm the notice." |
| C17 | Server restarts during an import | The import stops; it is listed as "needs another try"; nothing partial is kept (verified at build) [R1-21]. |
| C18 | Retry of a failed tag save | Retry on the item; the same refusal rules as C9. |
| C19 | A source fails at "add to analysis" versus at "import" | Listed separately as "not added to the analysis" and "import failed"; each has its own retry. |

Message column rule [R2-20]: where a row says no sentence, it uses an existing sentence from the named copy file (wizardCreateCopy, enterCopy, caseHomeCopy, startCopy) or changes state only; new sentences have an M-id in Appendix B.

After success: "Your case is ready. Opening it now." and the case home opens. If any source failed, the case opens anyway with the list.

## 6. Open a case

### 6.1 Ways in
1. **From the list**: Open on a row.
2. **By code**: 26 characters; case-insensitive; spaces and dashes ignored; counter "n of 26"; characters outside a-z and 2-7 refused with a plain message. Not found, malformed, erased, archiving, archived or a server error: the same sentence plus the same fixed line (M6), shown on every failed code whatever the number of archived cases [R1-10, R2-11, R2-12]. Closed cases do open by code.
3. **Restore from a case file**: full local path ending `.unravis-case`, plus the key the file was made with. Always makes a **new** case.
4. **Launch link**: the existing one-time link that starts the app stays as it is. This spec adds no new link, token, temporary address or share URL; a case is reached only through the app [R6-2].
5. **Resume**: a reload in the same tab returns to the remembered investigation.

### 6.2 What opens, by state
| State | Result |
|---|---|
| Open | Case home. Everything works. |
| Closed | Case home, read-only, with "This case is closed. You can read it, but nothing can be added." and a Reopen button. |
| Archiving | The list row says "Archiving, n%". By code or link: not found plus M6. Never opened. |
| Archived | Never opened in place. The list row shows the archive record and Restore (section 7.4) [R1-1]. By code or link: not found plus M6. |
| Untrusted | Not opened. Its sentence and Erase. |
| Erased | Same as not found. |
| Locked key | Unlock first, then continue. |

### 6.3 Case home
One page per case: Story so far (oldest first), Theories, Health checks, Analysis ("what this case can answer"), Files. Buttons: Open the analysis, Add a file, Add a theory, All cases; Close, Archive, Erase in a quiet menu. Failures: "This case could not be found. It may have been erased." and "The case could not be opened. Try again."

### 6.4 Open scenarios
| # | Situation | Behaviour |
|---|---|---|
| O1 | Two tabs open the same case | Reads share; state-changing moves follow section 9.1. |
| O2 | Another process holds the case | Not supported: "This case is in use by another Unravis." |
| O3 | Typo in a code | The one sentence; no hint whether other cases exist. |
| O4 | Case erased in another tab | "This case could not be found. It may have been erased." |
| O5 | Restore: wrong key, path or damaged file | The one fixed restore sentence; nothing written. |
| O6 | Restore while another save or restore runs | "Another case file is being saved or restored." |
| O7 | Restore throttled | "Too many tries." plus the wait. |
| O8 | Restore with no master key on this PC | "Restoring needs a master key on this PC too." |
| O9 | Large case slow to load | Loading line; Try again on failure. |
| O10 | Deep link to a closed case | Opens read-only with the closed note. |
| O11 | Deep link to a missing, archiving or archived case | Start screen with the not-found sentence and M6. |
| O12 | Archiving case in the list | The row shows progress; no Open. |

## 7. Archive a case

### 7.1 Purpose
Keep a finished investigation outside the working list, optionally free its disk space, and keep a way back.

### 7.2 What Archive does
1. Starts from Open or Closed (either belief is accepted). It does not close the case first [R2-1]. Refused while an import runs (M1). The person confirms; the text says imports stop and the case becomes read-only.
2. The person types a **full local folder path**. Rules, checked on the real path after resolving [R3-13]: it must be an absolute drive path (`C:\...`); a `\\?\C:\` prefix is stripped first; UNC paths (`\\server\share`, `\\?\UNC\`), relative paths and any `.` or `..` segment are refused; 8.3 short names, symlinks and junctions are resolved to the final folder before the next two checks; a folder on a network or mapped drive (drive type "remote") is refused; a folder inside or equal to Unravis's own data folder is refused; the folder must exist and be writable. The resolved folder path plus the file name must be at most **240 characters** (checked after the prefix is stripped and short names resolved) [R4-8]. Any failure gives M8 and changes nothing. The file name is made by Unravis: `<slug>-<short-code>-<yyyymmdd-hhmmss>.unravis-case`; on a collision a suffix `-2`, `-3` is added. Unravis never overwrites [R1-5, R1-6, R2-8]. The slug is lowercase ASCII letters and digits, every other run of characters becomes one "-", trimmed, at most 40 characters; an empty result gives "case"; a Windows reserved name (CON, PRN, AUX, NUL, COM1 to COM9, LPT1 to LPT9) gets the prefix "case-" [R2-9]. The slug never comes from tags.
3. Free space of at least 1.2 times the case size is checked first, **on the target folder's volume** (where the `.part` is written) [R3-13]. "Case size" is the working copy's bytes as listed; the file is no larger [R2-10]. The limit is the existing case file limit: a database of at most 8 GiB, twice the 4 GiB case cap, so a normal case never reaches it; a case over it cannot start (A17, M16) [R6-3].
4. Unravis stores **Archiving** (the state to return to, whether the sampler was running, the folder and the file name) in the master-key-protected index before writing. It runs as a background job with percent and Cancel, one at a time, sharing the save/restore lock with Export, Verify and Restore, streaming to `<name>.part` [R1-13, R1-14, R2-17].
5. When written, the `.part` is read back (size and checksum compared), renamed to the final name, and only then, under the case lock, is the case marked **Archived** (the commit point). Cancel and commit are serialised by that lock: first wins [R2-2].
6. The working copy is **kept**. Freeing it is a separate action, 7.5.

### 7.3 Case-file history and the archive record
Held in the master-key-protected case index, in the case's own entry. The entry lives until Erase; no other move removes it [R3-4]. It holds:
- **Case files made**: for each, the file name, the folder as typed, size, checksum, key check value (the full value is kept only in the sealed record; its last 4 characters are shown as "Key ending xxxx"), date, and counts of imports, sources and events.
- **Restores as new cases**: date and new short code, for each.
- Id, title, created, closed (a time, or none if the case was Open when archived [R3-3]), archived date, and working copy kept or freed.

The **archive record** is the newest item of "case files made"; the list row shows it while the case is Archived, with the file name and folder so the person can find the file. Older items are the earlier files; the case home shows "Case files made earlier: n". Restore in place moves the case to Closed and keeps the history; Archive again appends. The entry holds no connection details, source names or payloads [R1-23].

### 7.4 Archived behaviour and restore
- Listing state only: no reading, no analysis, no export, no import [R1-1].
- **Restore** (in place; only while the working copy is kept): always back to **Closed**, same id, instant, even if the case was Open before it was archived [R3-3]. The story says "restored". The earlier Open or Closed state is not kept. This is the row's main action.
- **Restore from the case file as a new case** (More menu; kept or freed): always a new case, Closed, new id, title unchanged. Created is the restore time; the story keeps the original entries and adds "Restored from a case file made <date>" and "Restored from case <short code of the original>" [R2-15, R2-16]. The case's needs-another-try list is part of the case file and comes back with it; imports in it need their password typed again [R3-10]. It may be done again; each time makes another new case and adds a line to the original's restores list (if the original still exists). No duplicate check [R1-4]. The typed key is the key the file was made with and may differ from this PC's key; the restored case is encrypted with this PC's key [R1-8].
- **Archive again** after a restore: a new file with a new name; older files are left alone [R1-5].
- A Closed case restored from an archive can be Reopened.
- A **missing** case (an Open, Closed or kept Archived case with no mark whose folder is gone; never a Freed one) is found by a folder check when the list loads or a call touches it. The row says "Working copy missing" and stays in the list it belongs to (the main list, or the Archived list if it was Archived). Only Erase (record only) is offered, plus Restore from the case file as a new case if it was Archived. Open by code, Open, Restore in place, Free the space, Archive, Close and Reopen all fail like any case that cannot be opened (the not-found sentence, with M6 for an Archived one). The health panel lists it. It is never removed silently [R3-5, R4-2].

### 7.5 Free the space
Offered only on an Archived case whose working copy is kept, and blocked while the key is damaged, locked or in setup [R1-7].
1. **Verify** [R3-6]: a background job with percent and Cancel, sharing the save/restore lock (so it is refused with "Another case file is being saved or restored." if one is running). It opens the case file with the master key into a temporary store inside Unravis's data area (needs free space of 1 times the case size on that volume, else M9), recomputes the checksum and compares it, and compares the counts of imports, sources and events with the record, then discards the temporary store [R1-2, R2-7]. It takes no case lock and is not a reader of the working copy, so Restore in place or Erase may happen meanwhile; the last step then answers M2 [R4-3]. Cancel, or a key lock, stops it, discards the temporary store and shows M17; nothing is changed. Temporary stores live in one named subfolder of the data area; start-up recovery deletes every one a crash left, and a delete that fails is retried at next start and shown as "cleanup pending" in the health panel [R5-7]. If the key check value of the current master key differs from the one recorded for the file, Verify stops at once with M13 and nothing is freed (the case file can still be restored as a new case with the old key); if the file fails the check: nothing freed; M11 [R4-6].
2. **Second confirmation** names the file and its folder and says M13. The person types the short code. It is asked once, here [R2-6].
3. The server first runs the reader wait of section 8 with no lock held (new readers are refused with M19; on a timeout: M4, the refusal is lifted and nothing is marked) [R5-6]. Then, under the case lock, it re-checks the case is still Archived with the working copy kept (else 409 M2) and marks the entry **Freeing**, which is the commit point, then removes the working copy with the rest of the mechanics of section 8 (tombstone) but **keeps the entry and its history**, marked Freed. While the mark is set every other call answers M19. A crash or key lock after the mark is finished by recovery (section 2) [R3-6, R4-4, R5-1].

### 7.6 Key changes
**Keys, as ruled in v7 [R6-2, R6-4].**
- **Case key (new, optional).** A case may have its own key, chosen at Create (step 1, off by default) or later from the case's drawer. It is a passphrase of at least 12 characters, derived with the same Argon2id method as the master key, held in memory only while unlocked. A case with a case key is unlocked per case; the master key is not asked for it. Locking, losing or resetting a case key affects that case alone. The existing machine-wide master key and its "Not now" choice are unchanged.
- **Case file key.** A case file is sealed with the case key if the case has one, else the master key, else (neither exists) a passphrase typed for that file and never stored. The existing case file format and its single failure sentence are unchanged.
- **Master key reset** (existing "Start over with a new master key": the old key file is set aside and cases that used it stay unreadable) is not blocked, but first says M12 with N = the number of Archived cases whose working copy is **freed** and that were sealed under the master key (their case file is the only copy; the record shows "Key ending xxxx"). Cases with their own key are not affected by a master key reset and are not counted; kept working copies are not counted. After a reset, a kept archive made under the old key fails Verify with M13 (7.5) [R3-15, R4-6].
- **Key check value (security review, done in v7).** It is HMAC-SHA256 under the Argon2id-derived key with a fixed label, never a hash of the passphrase. The full value is stored only inside the sealed archive record; the screen shows its last 4 characters. Result: acceptable. It lets Verify detect a changed key without opening the file, gives an attacker no exact test (each guess still costs a full Argon2id run, and the case file header already provides the only exact test), and leaks 16 bits of an output that is itself unusable without the key. A change to the method needs a new review.

### 7.7 Archive scenarios
| # | Situation | Behaviour |
|---|---|---|
| A1 | Folder missing, not writable, network, relative, too long (240 characters with the file name), or inside the data folder | Nothing changes; M8. |
| A2 | Disk full while writing | `.part` removed; case returns to its prior state; M9. |
| A3 | Checksum mismatch after write | `.part` removed; not Archived; M10; retry makes a new name. |
| A4 | Key locks mid-archive | The job aborts at once and removes `.part`. Every case route answers 423 while locked. Recovery (section 2) returns the case to its prior state at next unlock or start-up, before any reply [R2-18, R3-1]. |
| A5 | Import running when Archive pressed | Refused, M1. |
| A6 | Press Archive twice | One job; the second press shows M3. |
| A7 | Freeing the space, then the key changes | M13 before freeing; M12 before a reset. |
| A8 | Case file moved or deleted by the person | Restore from file: fixed sentence. Restore in place with a kept copy still works. Free the space fails verify (M11). |
| A9 | Untrusted case | Not archivable. |
| A10 | Demo mode | Hidden. |
| A11 | Theories and story | Included in the file; restored with it. State only. |
| A12 | Zero imports | Allowed; the file restores as an empty Closed case. State only. |
| A13 | Server stops or crashes during Archiving | Recovery at start-up returns the case to its prior state and removes its `.part`. A complete renamed file left behind has its name and folder stored in the index's **unresolved files** list, an **orphan** is exactly an entry of that list: the health panel lists them by name, folder and size; Unravis never deletes it and has no adopt action; the person removes it in their file manager. The listing is checked each time the panel opens and disappears when the file is gone. Retry makes a new name [R1-13, R2-8, R3-2]. |
| A14 | Cancel pressed | `.part` removed (if Cancel wins after the rename, the finished file's name and folder are moved to the unresolved files list before the mark is cleared); prior state, sampler started again if it was running; M14. After the commit, or if recovery already ran, Cancel gets 409 M2. |
| A15 | Archive, restore as new, archive the new case | Allowed; no loop limit. State only. |
| A16 | Unravis finds an unfinished file with its own `.part` name | It removes only `.part` files named in an Archiving mark or the unresolved files list. It never deletes a finished `.unravis-case` [R1-3]. State only. |
| A17 | Case database over 8 GiB (the existing case file limit) | The Archive button is disabled and says M16. |
| A18 | Export, Verify or Restore already running | Archive refused with the existing sentence "Another case file is being saved or restored." |

## 8. Erase
- Offered from Open, Closed, Archived, Missing and Untrusted rows and the case home. Hidden on an Archiving row; the server answers M15 if called [R2-3].
- Confirmation: the person types the short code (first 8 characters). Disabled until it matches. Text: erase cannot be undone; a running import will be stopped; a case file made earlier is not touched. On a Freed case the text also says no working copy remains and shows the case file's name and folder, so the person can find it [R3-7].
- A **reader** is any request being served for that case or any open handle to its store (a read, an export). An idle tab holds none. Live streams of the case (progress, sampler) are closed by the server at once and answer M5 [R3-12].
- Order: new readers are refused (M5); live streams are closed; a running import is cancelled and the sampler stopped; Unravis waits up to **30 seconds** for readers (a verify reads the case file and a temporary store, so it is not one); after that M4, the refusal is lifted, the case is readable again and nothing is erased or marked. No forced erase [R1-11, R4-3]. Then, under the case lock, the entry is marked **Erasing** in the index (the commit point); the folder is renamed to a tombstone; the entry is removed and the case leaves the list; the tombstone is deleted [R1-12, R3-5].
- Recovery (start-up, unlock, or a touching call; section 2): an **Erasing** entry is finished (rename if the folder exists, then remove the entry). An entry with a missing folder and no Erasing mark is **kept** and shown as "Working copy missing" (a Freed entry has no folder by design and is shown as Archived, freed; a Freeing entry whose folder is already gone is finished as Freed [R5-4]). Leftover tombstones are deleted; a locked file is retried at next start and shown as "cleanup pending" in the health panel.
- Erase of an Archived case removes the entry, its history and any kept working copy; a case file is never touched.

## 9. Action by state

| Action | Open | Closed | Archiving | Archived (kept) | Archived (freed) | Untrusted | Missing | Freeing / Erasing |
|---|---|---|---|---|---|---|---|---|
| Open in place | yes | read-only | no | no | no | no | no | no (M5/M19) |
| Add import | yes | no | no | no | no | no | no | no (M5/M19) |
| Read analysis | yes | yes | no | no | no | no | no | no (M5/M19) |
| Export | yes | yes | no | no | no | no | no | no (M5/M19) |
| Close | yes | n/a | no | n/a | n/a | no | no | no (M5/M19) |
| Reopen | n/a | yes | no | n/a | n/a | no | no | no (M5/M19) |
| Archive | yes | yes | n/a | n/a | n/a | no | no | no (M5/M19) |
| Cancel archive | n/a | n/a | yes (live job only) | n/a | n/a | n/a | n/a | n/a |
| Restore (in place) | n/a | n/a | no | yes | no | no | no | no (M5/M19) |
| Restore from case file as new | n/a | n/a | no | yes | yes | no | yes if Archived | yes (not Erasing) |
| Free the space | n/a | n/a | n/a | yes | n/a | n/a | no | no (M5/M19) |
| Erase | yes | yes | no (M15) | yes | yes (entry and history only) | yes | yes (entry only) | no (M5/M19) |

M5 is for Erasing and M19 for Freeing; each is answered to the whole case, not only the called action. A Freed case is never Missing.

### 9.1 Two tabs acting at once [R1-15, R3-11]
The server decides, under the case lock. Rule: each state-changing call carries the state the page believes; if it differs from the server's state the answer is 409 M2. Exceptions:
- Archive accepts a belief of Open or Closed and runs from either; if the server finds Archiving the answer is M3.
- Erase accepts any belief; if the server finds Archiving the answer is M15; if it finds Erased, the not-found sentence.
- Restore from file as new is always allowed for any Archived case: kept, freed or missing (section 9).
- Export, Archive or Verify while a save or restore runs: "Another case file is being saved or restored."
- Adding an import to a case the server finds Closed or Archiving: `case-closed` (409).

Examples:
| Page believes | Server finds | Result |
|---|---|---|
| Open, presses Close | Closed, Archiving or Archived | M2 |
| Closed, presses Reopen | Open, Archiving or Archived | M2 |
| Open, presses Archive | Closed | runs |
| Open or Closed, presses Archive | Archiving | M3 |
| Open or Closed, presses Archive | Archived | M2 |
| Archiving, presses Cancel | Archived (commit won) or prior state (stopped or recovered) | M2 |
| Archived (kept), presses Restore | Closed (already restored) or freed | M2 |
| Archived (kept), presses Free the space | freed already, or restored in place | M2 |
| Archived (kept), presses Free the space | Erased | not-found sentence |
| Archived (freed or kept), presses Erase | freed or erased meanwhile | runs on what is left; Erased gives the not-found sentence |
| Closed, presses Erase | Archiving | M15 |

## 10. Message catalogue (all sentences, one place) [R1-18]
Every failure says what happened, what is safe, what to do; never a path, connection string, password or id. Appendix B lists the new sentences with ids. Existing sentences stay in the copy files (wizardCreateCopy, enterCopy, caseHomeCopy, startCopy).

## 11. Analysis that helps

Each analysis names its data. If the data is missing the screen says so and does not estimate.

### 11.1 Catalogue
| # | Analysis | Needs | Answers |
|---|---|---|---|
| N1 | Overview percentiles (P50, P95, P99, max), by route and day | audit documents with total ApiTime | How slow, how often. Shows request count. |
| N2 | Slowest list and Nearby activity | audit documents plus other sources in the same window | What else happened beside a slow call. |
| N3 | **Timing split D = A + C** (before-D, audit filter, inside-C filters; sum equals the array within 0.001 ms) | `PreActionFilterTimings` array | Which layer holds the time. |
| N4 | **Filter share** per filter (for example the permission-check lane, S7) | the array | Which filter owns the time and its share of slow calls. |
| N5 | **Start-minute clustering** and `shared_stall` | audit start times and durations | Slow calls together (shared cause) or alone. |
| N6 | `errors_up` | status codes | Whether failures rose with slowness. |
| N7 | **Little's Law** (L = λW) and `near_saturation` | request rate, durations, and a count of calls in flight **derived from the audit documents' own start time and duration**; it excludes the web server queue and says so [R1-22] | Stalled or overloaded. |
| N8 | **Amdahl bound** 1 ÷ (1 − p) | share p from N4 | The most a fix of one part can give (upper bound only). |
| N9 | **Slow versus normal comparison** (BubbleUp style) | slow and normal groups, each at least 20 requests (P95) or 100 (P99) [R1-22] | What differs: route, host, user group, minute. |
| N10 | IIS time (O = IIS time − A) | IIS logs; clock tolerance starts at 1 second [R1-22] | Time spent before the app. |
| N11 | SQL Server waits, Query Store, blocking | SQL health read | Whether the database held the time. |
| N12 | MongoDB health (profiler, serverStatus, currentOp, log) | Mongo health or files | Whether Mongo held the time. |
| N13 | Redis slow log | Redis export | Whether Redis held the time. |
| N14 | Hangfire recurring jobs | SQL read or CSV | Whether background work coincides. |
| N15 | App log errors | app logs | Errors beside slow calls. |
| N16 | Host counters | sampler, perf counters | CPU, memory, handles beside slow calls. |
| N17 | **Permission-check counters** (planned, section 12) | new collection | Whether the check, or its store, was slow or failing. |

All numbers above are starting values, shown with their group sizes.

### 11.2 Rules
- Measure first: no cause is claimed without evidence from the case.
- Every recommendation is a finding with its evidence and what it would change.
- A model appears only when its data exists; otherwise the screen names the missing data.
- Float comparisons use a tolerance; tier ordering needs a clock tolerance; small groups show their size and are not ranked.

### 11.3 By lifecycle point
- **After create**: "What this case can answer" (from sources present) and "What it cannot answer yet" with what to add; N17 is listed there as "Planned, not collected".
- **Open case**: Add more data suggests the next source that unlocks an analysis.
- **Closed case**: same analyses, read-only.
- **Archived case**: no analysis; the record shows sources, window, counts and dates. To read, Restore [R1-1].

## 12. Planned data: permission-check figures (planned, not part of acceptance) [R1-24]

Not collected today. Out of acceptance for this feature; shown in the app only as "Planned, not collected".

| Figure | Why |
|---|---|
| Permission-check latency per call, and a histogram per minute | Is the lane that holds the largest share of slow time slow by itself? |
| Error and retry counts | Rule out failures hidden behind slowness. |
| Latency and error counters of whatever store backs the check | Separate "the check is slow" from "its store is slow". |
| Counters at the stall minutes | Does a shared stall (N5) coincide with the store? |

Collector contract (follow-up): per-minute aggregates; no payloads; no user ids; same time base as the audit documents; a new source kind; N17 shows only when the source exists. Pre-build question: what backs the check is not verified.

## 13. Quality bars
- Plain words; no jargon without a link.
- Keyboard: every action reachable; focus moves to the heading on a new screen; errors announced (role alert); colour never the only signal; WCAG AA in both themes; 390 to 1600 px.
- Security: no credentials in the case, logs, exports, URLs; case codes are identifiers, not secrets; connections are read-only.
- Copy passes the copy lint.

## 14. Acceptance criteria
Test steps are in Appendix C. Each is pass or fail.
1. Every scenario in 5.3, 6.4, 7.7 has a defined message (an M-id, or an existing sentence in the copy file it names) or, where listed "state only" (Appendix C), a defined state.
2. Archive never leaves a half-archived case: after any failure or Cancel the case is in the state it was in (Open or Closed, with the sampler running again if it was before), and its `.part` is removed at once (the stale mark is cleared by recovery, section 2).
3. Unravis never deletes a finished `.unravis-case`; it removes only its own unfinished `.part`.
4. Erase needs the short code, is atomic from the list's view, and never touches a case file.
5. Restore from file always makes a new case; restore in place only with a kept working copy.
6. Free the space only after the verify passes and the second confirmation.
7. Archived cases are never readable in place.
8. A mistaken Close can be undone with Reopen.
9. The analysis catalogue shows only what its data supports; N17 is only ever "planned".
10. The wireframe shows each flow and main state in both themes, with an index of the states it shows.

## 15. Open questions (to settle before build, not blockers for the spec)
All six are settled in v7 (round 6); none is open.
1. What backs the permission check: nothing is built now; the figures stay planned, outside acceptance [R6-1].
2. A lock or restart leaves nothing partial in a case: an import is one transaction, checked in the code (5.2, C17) [R6-5].
3. Security review of the key check value: done, acceptable (7.6) [R6-4].
4. Reopen applies the saved filters and Mongo link and lets the person run again; it never starts the sampler. Only a return from Archiving restores a running sampler [R6-6].
5. The archive limit is the existing case file limit, 8 GiB of database (5.1, 7.2, A17) [R6-3].
6. Keys are per case and optional, with no new token or link (7.6) [R6-2].

## Appendix A. Spec map
Sections 2, 7, 8, 9 are the state model. Sections 5, 6 are the flows. Section 10 and Appendix B are the words. Appendix C is the proof.

## Appendix B. New message ids
| Id | Sentence |
|---|---|
| M1 | An import is still running. Wait for it or stop it first. |
| M2 | This case changed in another tab. Reload. |
| M3 | An archive is already running. |
| M4 | The case is in use. Close its other tabs, then try again. |
| M5 | This case is being erased. |
| M6 | Archived cases are in the Archived list on the start screen. |
| M7 | This case is closed. You can read it, but nothing can be added. |
| M8 | The folder cannot be used. Choose a folder on this computer that you can write to. |
| M9 | There is not enough free space for the archive. |
| M10 | The archive file could not be checked. Nothing was changed. Try again. |
| M11 | The case file did not pass the check. The working copy was kept. |
| M12 | N archived cases can only be opened with the old key. (N counts freed archives.) |
| M13 | This file can only be opened with the key it was made with. |
| M14 | Archiving was stopped. The case is back as it was. Try again. |
| M15 | An archive is running. Cancel it first. |
| M16 | This case is too large to archive in one file. |
| M17 | The check was stopped. Nothing was changed. Try again. |
| M18 | This case was already made. Find it on the start screen. |
| M19 | This case is being freed. Try again in a moment. |

## Appendix C. Test steps per criterion
| Criterion | Steps and expected result |
|---|---|
| 1 | For each scenario row, trigger the situation (inject where needed); the screen shows the sentence the row names (an M-id, or the existing sentence from the named copy file) and the stated state. Rows that say "state only" are checked by the state alone. |
| 2 | Kill the server mid-archive, fill the disk, flip one byte of the written file, lock the key mid-job, press Cancel: the case is back in its prior state (Open or Closed) after recovery (restart, next unlock or the next call to it); no `.part` remains. Press Cancel in the same instant as the commit: exactly one wins, the other shows M2. |
| 3 | Archive, then erase, restore, archive again: finished files remain; unfinished `.part` files are gone. |
| 4 | Wrong short code keeps Erase disabled; right code erases. Kill the server after the rename and before the index change: at start-up the case is gone and the tombstone swept. Hold a reader for over 30 seconds: M4 and nothing erased. Erase on an Archiving row: M15. Kill the server after the Erasing mark and before the rename: at start-up the erase is finished. Delete a Closed case's folder by hand: the row says "Working copy missing" and is not removed; a Freed row stays Archived (freed). |
| 5 | Restore twice: two new cases, record lists two restores; in place with a kept copy: same id; in place with a freed copy: the action is absent and the server refuses. |
| 6 | Corrupt the file, press Free the space: nothing freed, M11. Cancel or lock the key during verify: M17, temporary store gone. Verify with too little space: M9. Kill the server during Verify: at start-up its temporary store is deleted. Good file: needs the second confirmation (asked once). Restore in place during the confirmation: the final step answers M2 and frees nothing. |
| 7 | Open an Archived case by code, link and list: never opens. |
| 8 | Close, Reopen: import accepted again; story shows closed and reopened. |
| 9 | Case with audit only: N1 to N6 offered; N17 appears only as "planned". |
| 10 | Open the wireframe: every state listed in its index is present in light and dark. |
| Slug vectors (7.2.2) | "Q3 Slow API!" gives `q3-slow-api`; "  " gives `case`; "CON" gives `case-con`; "Ünïcode éé" gives `n-code`; a 100-letter title gives the first 40 letters. |
| Key reset (7.6) | Archive two cases and free the space of both, start a key reset: M12 shows "2 archived cases"; with only kept copies it shows 0. Reset, then Verify a kept archive made under the old key: M13. Give a case its own key: opening it asks for that key, other cases open with the master key, a master key reset leaves it readable and uncounted by M12, locking it closes that case only. |
| Request id (5.2) | Press Create, reload the tab, press again: one case. Open a second tab and run the wizard: a second case. Kill the server mid-create after a connection source: on return that source is listed as needs another try, never imported twice. Erase the case, repeat the id: a new case. |
| Needs-another-try (5.2) | Fail a tag save, reload, close the case: the list is still there with Reopen to try again. |
| Injection points (for QA) | INJ-1 disk full during write; INJ-2 flip a byte after write; INJ-3 kill after `.part` is complete, before rename; INJ-4 kill after rename, before the Archived mark; INJ-5 kill after the Erasing mark, before rename; INJ-6 hold a store handle for a set time (a fake clock may be used for the 30 seconds); INJ-7 lock the key; INJ-8 start an import; INJ-9 pause the archive job at the commit lock, then fire Cancel (exactly one wins: Cancel wins and the case returns to its prior state with the file on the unresolved list, or commit wins and Cancel answers M2); INJ-10 lock the key between the rename and the Archived mark (recovery returns the prior state; the file goes to the unresolved list). |
| State-only rows (criterion 1) | C1, C4, C11, C12, C13, C14, C15, O1, O9, O12, A9, A10, A11, A12, A15, A16: checked by the state they name, no new sentence. Rows A8 and C19 name an existing sentence or an M-id and are checked by it. |
| Path rules (7.2.2) | Refused with M8: `\\srv\share\x`, `\\?\UNC\srv\s`, `x\y`, `C:\a\..\b`, a mapped network drive, a junction that points into the data folder, an 8.3 short name of the data folder; accepted: `\\?\C:\Cases` (prefix stripped). A resolved folder plus file name of 241 characters is refused; 240 is accepted. |
| Stale mark (section 2) | Crash during Archiving, restart: the list shows the case in its prior state, never "Archiving"; an Open case's sampler runs again. Cancel from an old tab: M2. Lock the key after the Erasing mark, unlock with no restart: the next call finishes the erase. Same for a Freeing mark: the case ends Archived (freed). |
| Missing case (7.4, 8) | Remove a kept working copy's folder by hand and reload the list (no restart needed): row "Working copy missing" in the list it belongs to; Erase removes only the entry; Restore from the case file as new works. A Freed case has no folder and is never shown as Missing. |
