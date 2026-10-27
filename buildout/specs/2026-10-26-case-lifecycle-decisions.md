# Decisions log: case lifecycle spec

One entry per reviewer question: id, short question, ruling, why. The reviewer is a fresh agent each round with no knowledge of the product. v1 is `2026-10-26-case-lifecycle-spec-v1.md` (kept unchanged).

## Round 1 (25 questions on v1)

| Id | Question | Ruling | Why |
|---|---|---|---|
| R1-1 | Is analysis readable in an Archived case? | No. Archived is a listing state only (record, no reading). To read: Restore, which gives Closed. Section 9 and 11.3 made to agree. | One rule, no half-readable state. |
| R1-2 | What proves the archive file is usable before the space is freed? | "Free the space" runs a full test-open: decrypt the file with the master key and compare its counts (imports, sources, events) to the record. A second confirmation names the file and says the master key is needed. | Checksum proves write only; freeing is the only irreversible step. |
| R1-3 | "Never deletes a case file" versus removing a partial file | Written as `<name>.part`, renamed only when complete and verified. Unravis never deletes a complete file; it removes only its own unfinished `.part`. Criterion reworded. | Makes the rule testable. |
| R1-4 | Restoring an archive as a new case twice? | Allowed. The archived record stays Archived and lists each restore (new short code, date). No duplicate check. | Restore never changes existing cases; simplest honest rule. |
| R1-5 | Archive, restore, archive again: file naming | New file every time (`<title-slug>-<short-code>-<yyyymmdd-hhmm>.unravis-case`); never overwrites; the record keeps the latest file and a count of earlier files; no loop limit. | Never lose a file; no hidden overwrite. |
| R1-6 | Where does the folder come from? | A typed full local path, same rules as the restore path: no network path, at most 520 bytes, not inside Unravis's own data folder, folder must exist and be writable. File name is generated; an existing name is refused. Free space of at least 1.2 times the case size is checked first. | Matches the restore field the app already has. |
| R1-7 | What if the master key is reset or damaged after archive? | The record stores a short key check value (4 characters, cannot be turned back into the key) shown as "Key ending xxxx". Free the space is blocked while the key is damaged. A key reset is not blocked but first says "N archived cases can only be opened with the old key". Security review needed at build for the check value. | Key loss must never be silent. |
| R1-8 | Which key does restore use? | The key typed decrypts the file and can differ from this PC's key; the restored case is encrypted with this PC's key. This PC must have a key. | Matches existing copy and behaviour. |
| R1-9 | Demo mode and links | Demo and real are separate server runs; there is no switch in flight. In demo, a case link or remembered tab is ignored and the sample analysis opens. | Removes the mixed state. |
| R1-10 | Does open-by-code leak an Archived case? | Every failed code shows the same sentence plus the same fixed line "Archived cases are in the Archived list on the start screen." Closed cases still open by code (as today). Codes are unguessable (26 characters) in a single-user local tool. | No leak from the failure path; accepted residual. |
| R1-11 | How long does erase wait for readers? | 30 seconds. During the wait new readers are refused ("being erased"). After that: "The case is in use. Close its other tabs, then try again." No forced erase in v1. A running import is cancelled and the sampler stopped first. | Bounded, no data loss by force. |
| R1-12 | Erase crashes midway | The folder is renamed to a tombstone first (atomic) and the case is gone from the list at once; deletion follows; a start-up sweep deletes leftover tombstones; a locked file is retried at next start and shown as "cleanup pending" in the health panel. | Atomic from the person's side. |
| R1-13 | Crash during archive | An `Archiving` state is stored before writing; the commit point is marking Archived after the rename. At start-up an `Archiving` case returns to Closed and its `.part` is removed; a complete file with no mark is reported and left; retry makes a new name. | Idempotent and no stuck state. |
| R1-14 | Large case archive | A background job with percent, Cancel, one at a time (shares the existing save/restore lock). Streams the file (no whole-case memory). Over the existing export cap: the button says so. Free space checked up front. | Same pattern as imports. |
| R1-15 | Two tabs acting at once | The server decides. State-changing calls carry the state the page believes; a mismatch answers 409 "This case changed in another tab. Reload." with a table of pairs in the spec. | Testable. |
| R1-16 | Close with a running import | Close is refused while an import runs ("An import is still running. Wait for it or stop it first."). Close text says nothing can be added after. | Avoids half-imports in "final" evidence. |
| R1-17 | Create dedupe | The page sends a request id (random) with Create; the server returns the same case for a repeated id. Used for double press and for recovery after a server stop. | Removes title-matching guesswork. |
| R1-18 | Untestable criteria | Appendix B gives a message table; Appendix C gives test steps per criterion, including failure injection. | Measurable. |
| R1-19 | Last opened, sort, clock | List shows "Archived on <date>" or "Not opened" where there is no open time. Sort key: last activity (latest of created, opened, closed, archived) by server wall clock. Scan age uses elapsed server time, not wall clock. | Clock moves cannot break the one-hour rule. |
| R1-20 | Paging and caps | List is paged 50 at a time by the server. Untrusted cases sit last and do not count. Archived has its own count. No cap on number of cases. Size caps are the existing importer and store caps, shown with their number. | No invented numbers. |
| R1-21 | Passwords on retry | Passwords are never stored. A retry of a connection source needs the password typed again. A lock or restart stops a running import; it is listed as needing another try; nothing partial is kept (verified at build). | Consistent with the memory-only rule. |
| R1-22 | Unclear data needs | N7 concurrency is derived from the audit documents' own start and duration (calls in flight); it does not include the web server queue and says so. N8, N9 share the minimum group of 20. N10 clock tolerance starts at 1 second. All are starting values. | Every input has a source. |
| R1-23 | Archive record fields | Held in the master-key-protected case index: id, title, created, closed, archived date, file name, size, checksum, key check value, counts, kept or freed, restore history. Shown in the row only. | Titles stay protected. |
| R1-24 | Permission-check collection | Kept in the spec as planned. Out of acceptance for this feature; the catalogue and the "cannot answer yet" list show it as "Planned, not collected". A collector contract is given as a follow-up. What backs the check stays a pre-build question. | User wants it included; it must not block this build. |
| R1-25 | Lifecycle events; accidental Close | The server writes story entries: created, closed, reopened, archiving started, archived, restored, imports stopped. A Closed case can be Reopened (the earlier cold-start criteria already planned it). Closing no longer means permanent. Archived cases become Closed on restore and can then be Reopened. | Escape hatch for a mistaken Close. |

## Round 2 (20 questions on v2)

| Id | Question | Ruling | Why |
|---|---|---|---|
| R2-1 | Archive from Open fails or is cancelled: Open or Closed? | Archive does not close first. Open goes to Archiving (imports refused, sampler stopped, as for Close). Failure or Cancel returns the case to the state it was in. "closed" and "archived" story entries are written only at commit. | Nobody is left Closed by a failed archive. |
| R2-2 | Cancel racing the commit | One server lock per case serialises Cancel, commit and every move. First to take it wins; a Cancel after commit gets 409 "changed". | Single decision point. |
| R2-3 | Erase on an Archiving row | Hidden in the page; the server answers 409 "An archive is running. Cancel it first." (M15). | Matches the matrix. |
| R2-4 | Kept versus Freed in the matrix | Two columns: Archived (kept), Archived (freed). | Actions differ. |
| R2-5 | Two Restore actions | Row shows "Restore" (in place, kept only) and, in a More menu, "Restore from the case file as a new case" (kept or freed). Concurrency rows added. | Distinct names, distinct results. |
| R2-6 | Free the space races and erase rules | The server re-checks "Archived, kept" at the moment of erasing, else 409. Mechanics of section 8 (tombstone, 30-second reader wait) apply, but the record stays. The short code is asked once, in the second confirmation. | One confirmation, one commit. |
| R2-7 | Verify content and sentence | Counts of imports, sources, events plus the recomputed checksum. Key is the master key only (no typed key). Failure sentence is M11. | Removes the second sentence. |
| R2-8 | Orphan file and same-minute name | Name has seconds, and a suffix -2, -3 on collision. An orphan complete file is listed in the health panel by name; Unravis does not delete it; the person removes it in their file manager. | Never delete a finished file. |
| R2-9 | Slug | Lowercase ASCII letters and digits; every other run becomes one "-"; trimmed; at most 40 characters; empty gives "case"; a Windows reserved name gets prefix "case-". Test vectors in Appendix C. | One function. |
| R2-10 | Size and cap | Size means the working copy's bytes as listed; export is no larger (encrypted, not expanded). Cap is the existing export cap; scenario A17 and M16 added. Number confirmed at build (open question 5). | Testable once the number is read. |
| R2-11 | Archived line when n = 0 | Shown on every failed code, whatever n is. M6 is the one source of the words. | No leak, one source. |
| R2-12 | Archiving by code or link | Same as Archived: not-found sentence plus M6. Only the list row shows progress. | Same rule for every way in. |
| R2-13 | Request id lifetime | Kept in the tab's session storage for one wizard run, so a reload keeps it; a new tab or a new run makes a new one. A retry after failure at step 1 reuses it. The server remembers it for the life of the case. | Simple and bounded. |
| R2-14 | "Needs another try" storage | Stored in the case on the server; survives reload. On a Closed case it shows "Reopen to try again". Retry buttons per item (tags, add to analysis, import). Scenarios C18, C19. | Not page state. |
| R2-15 | Dates of a restored-new case | Created = restore time; the story keeps the original entries and adds "Restored from a case file made <date>". Sort is by last activity, not created. | Honest dates. |
| R2-16 | Link between restored and archived | The new case's story says "Restored from case <short code of the original>". No live link. | Enough, no new object. |
| R2-17 | Export versus Archive lock | Same writer, same lock; the second press says "Another case file is being saved or restored." (existing sentence). | One path. |
| R2-18 | Where the Archiving marker lives; lock mid-job | In the master-key-protected index. A lock aborts the in-memory job at once and removes `.part`. The marker is cleared by recovery at next start-up or next unlock, whichever is first. | File deletion needs no key; state needs the index. |
| R2-19 | Erase crash between rename and index | The rename is the commit. Start-up removes an index entry whose folder is missing, unless the record is Archived-freed (which has no folder by design), and sweeps tombstones. In-use test added. | Defined recovery. |
| R2-20 | Messages for every row | Each scenario row names its message as an M-id or "existing" (a sentence already in the named copy file) or "state only". Criterion 1 tests those. Missing test steps added. | Honest scope. |

## Round 3 (15 questions on v3)

| Id | Question | Ruling | Why |
|---|---|---|---|
| R3-1 | Stale Archiving mark after a crash or lock: Cancel, Erase, row text? | Recovery runs at start-up, at unlock before the first reply, and at the start of every call that touches the case (and for every row when the list loads). The mark is never visible; "Archiving, n%" means a live job. Cancel after recovery is M2. | Closes the window instead of defining behaviour inside it. |
| R3-2 | Orphan complete file: accepted? Any adopt or remove? | Accepted. Health panel lists name, folder and size, re-checked on each open. No adopt, no delete by Unravis. | Never delete a finished file; no new object. |
| R3-3 | Is the pre-archive Open or Closed state kept? | No. Restore in place always gives Closed. Commit writes only "archived"; the record's closed time is none if the case was Open. | One rule. Reopen is the way back. |
| R3-4 | Where do restore history and earlier-file count live across in-place restore? | In the case's index entry, which lives until Erase: a list of case files made and a list of restores. The archive record is the newest file. | Nothing is lost by restore. |
| R3-5 | Start-up sweep: Freed versus a lost folder | Erase writes an Erasing mark first (commit). Only Erasing entries are removed at start-up. Missing folder without the mark is kept as "Working copy missing" with Erase and, if Archived, Restore from file. Freed uses a Freeing mark. Supersedes R2-19. | A lost folder must never silently delete the only record. |
| R3-6 | Verify: lock, space, progress, cancel, key lock | Save/restore lock; temporary store in the data area, 1 times the case size else M9; percent and Cancel; no case lock, final step re-checks (M2); Cancel or key lock gives M17. | Defined and testable. |
| R3-7 | Erase of a Freed case: pointer to the file | The record keeps the folder as typed; the erase text on a Freed case says no working copy remains and names the file and folder. | The person can find the file. |
| R3-8 | Demo and key state | Demo never reads the key; never shows unlock or setup. | Demo has no store. |
| R3-9 | Request id after erase or archive | Open or Closed: return the case. Archiving or Archived: M18, nothing made. Erased: id forgotten. Wizard drops its id when the case opens. | No silent duplicates while the case exists. |
| R3-10 | Needs-another-try list in a case file | Part of the case; travels with the file and returns on any restore; imports in it need the password again. | It is case data. |
| R3-11 | Complete the two-tab table | General rule (belief must equal server state, else M2) plus listed exceptions and more example rows. | Complete without a huge table. |
| R3-12 | Reader definition; erase of a running import | Reader: a request or an open store handle. Live streams are closed at once. A running import is stopped by Erase, and the confirmation says so. | No M4 loop from an idle tab. |
| R3-13 | Path normalisation, byte encoding, volume | Real path after resolving; UNC, relative, dot segments, remote drives refused; `\?\C:\` stripped; 520 UTF-8 bytes; space checked on the target volume (and the data volume for verify). | Testable rules. |
| R3-14 | Untestable criteria rows | "State only" rows listed; injection points INJ-1 to INJ-8 named; criterion 2 reworded; criterion 10 mentions an index. | QA does not guess. |
| R3-15 | Archived paging, counting, M12 scope | Archived list paged 50; n is the server total; M12 counts freed archives only; what a reset does to working copies is an open question (6). | Counts follow what can actually be lost. |

## Round 4 (cold review of v4, 10 questions; v5)

| Id | Question | Ruling | Why |
|---|---|---|---|
| R4-1 | Closed time after Restore in place | Closed time = the restore time; the archive record keeps its own copy; the list sorts by the restored time. | A Closed case always has a time. |
| R4-2 | Missing is not a modelled state | Missing is a display state found by a folder check when the list loads or a call touches the case. It stays in the list it belongs to. Only Erase (and Restore as new if Archived) work; everything else fails like a case that cannot be opened. A Missing column joins the action table. | One defined answer per action. |
| R4-3 | Erase reader versus cancelling the import | Erase cancels a running import first, then waits for readers. A reader is a request or export. Verify reads the case file and a temporary store, so it is not a reader. | A long import must not always give M4. |
| R4-4 | Freeing and Erasing marks | Reader wait comes before the mark; a timeout gives M4 with nothing marked. While a mark is set, every other call answers M5. Matrix gets a Freeing / Erasing column. | Mark means committed. |
| R4-5 | Orphan data after recovery | Recovery moves a left-over complete file's name and folder to an unresolved-files list in the index; the health panel checks it. `.part` cleanup is limited to names in a mark or that list. | Recovery must not lose where to look. |
| R4-6 | Key reset test and Verify after a reset | Test frees the cases first. Verify stops with M13 when the current key check value differs from the file's. | The test now follows the M12 rule; Verify gives the true reason. |
| R4-7 | Recovery on every call, and on the list | Recovery runs only for cases that carry a mark, only on the returned page rows, never for an unmarked read. A job still unwinding is live until it exits. | No writes on plain reads. |
| R4-8 | Path length | Resolved folder plus file name at most 240 characters; the 520-byte rule is dropped. | Matches Windows limits. |
| R4-9 | Create retry after a server stop | Wizard keeps its plan with the request id and re-runs steps 2 to 5 (idempotent); with the plan lost, the person is offered Open it and adds sources later. | Chosen sources are not silently lost. |
| R4-10 | Untestable Cancel and commit race | INJ-9 (pause at the commit lock, fire Cancel) and INJ-10 (lock the key between the rename and the mark) added with expected results. | Testable. |

## Round 5

Cold review of v5 (Sonnet, no memory of the app): 8 defects, all accepted. Result: spec v6, final. A sixth cold round was not run; the open list is now about missing data and build-time facts, not the spec's logic.

| Id | Question | Ruling | Why |
|---|---|---|---|
| R5-1 | Freeing and Erasing marks had no recovery trigger except start-up | Recovery covers Archiving, Freeing and Erasing marks, at the same three triggers; a mark with no live job is stale. | A key lock with no restart must not strand a case. |
| R5-2 | A Cancel that wins after the rename leaves an untracked file | That Cancel moves the file's name and folder to the unresolved list before clearing the mark. "Orphan" now means exactly an entry of that list. | The panel can only list what the index knows. |
| R5-3 | "Back as it was" versus a stopped sampler | Returning to the prior state starts the sampler again if it was running; the Archiving mark records it. Reopen still does not restart it. | M14 and criterion 2 stay true. |
| R5-4 | Missing versus Freed and Freeing | Missing applies only to Open, Closed and kept Archived entries with no mark. A Freed case is never Missing; a Freeing entry with its folder gone is finished as Freed. | A Freed case has no folder by design. |
| R5-5 | Create re-run versus memory-only passwords, a Closed case and an erased case | Re-run skips connection-source imports (needs another try); a Closed case lists remaining steps as "Reopen to try again"; a repeated id after an erase makes a new case. | Passwords are never stored. |
| R5-6 | Reader wait under or before the lock | Free uses the Erase order: wait with no lock, then lock, re-check, mark. New message M19 ("being freed") for Freeing and for the wait; M5 stays for Erasing. | A 30-second wait must not hold the lock; M5 said "erased". |
| R5-7 | Verify temporary store left by a crash | Stores live in one named subfolder; start-up deletes them all; a failed delete shows "cleanup pending". | They count against free space otherwise. |
| R5-8 | Criterion 1 not fully testable; 9.1 versus section 9 | Every row without a sentence joins the state-only list (C1, C4, C11, C13, A9, A10 added). 9.1: Restore as new is always allowed for any Archived case, kept, freed or missing. | Criterion 1 is now checkable. |


## Round 6 (owner rulings, 2026-10-27; spec v7)

The owner answered the six open items and asked for the wireframe to be rebuilt in the real app's look.

| Id | Question | Ruling | Why |
|---|---|---|---|
| R6-1 | What backs the permission check | Nothing is built now. The figures stay planned, outside acceptance. | Owner: "No need for now". |
| R6-2 | What a key reset does to working copies; link or token | The key is per case and optional (a case key, off by default). The machine-wide master key and its "Not now" stay. A master key reset does not touch cases with their own key. No new token, temporary URL or share link; the existing one-time launch link stays. | Owner: "case by case master key, optional, no temp token URL". Checked: today's key is machine-wide, a reset only sets the old key file aside, and the "temp token URL" is the existing single-use `/launch` link. |
| R6-3 | Archive size cap number | No new cap. The limit is the existing case file limit: 8 GiB of database (`MaxCaseFileDB`), twice the 4 GiB case cap. A normal case cannot reach it. | Checked in `casefile.go` and `casestore/store.go`. A second number would only add a way to disagree. |
| R6-4 | Security review of the key check value | Accepted. HMAC-SHA256 under the Argon2id-derived key with a fixed label; full value kept only inside the sealed record; 4 characters shown. | It gives no exact guess test beyond the case file header's own AEAD check, and each guess still costs a full Argon2id run. |
| R6-5 | Partial create or import after a restart | An import is one SQLite transaction, so a stop commits nothing. A half-imported file never exists; the case may hold fewer files, which the wizard's re-run lists. | Checked in `casestore/sqlite/case.go` (`Import` runs inside one transaction). The request id of create is new work. |
| R6-6 | Reopen and the sampler | Reopen applies the case's saved Mongo link and filters and lets the person run the sampler again. It never starts it. | Owner: "apply the filters and let user run it again". |
| R6-7 | Wireframe | Rebuilt from the real app's own CSS, markup and screens (dark and light), not a hand-made look. | Owner: the first wireframe "looks nothing like Unravis". |
