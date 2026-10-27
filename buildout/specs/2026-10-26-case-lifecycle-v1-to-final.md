# Case lifecycle: what changed from v1 to the final spec (v7)

Plain summary. v1 is `2026-10-26-case-lifecycle-spec-v1.md`; final is `2026-10-26-case-lifecycle-spec.md`. Every ruling, with its reason, is in `2026-10-26-case-lifecycle-decisions.md` (five review rounds, 78 questions, then six owner rulings, each ruled). The wireframe (`buildout/wireframes/case-lifecycle.html`) shows them in the real app's own look: the optional case key in Create (S4) and Keys (S17), Reopen in Case home (S8). The whole workflow, step by step (create, sources, import, analysis Steps 0 to 10, close, archive, free, erase), is in `buildout/wireframes/workflow-walkthrough.html` (139 screens): it also covers saved SQL and Mongo connections, the separate Blackbox recorder (place, scan, use as a source), guided analysis versus manual tools, the suspect colours and the checklist.

## The five big changes

| | v1 | Final |
|---|---|---|
| **Close is reversible** | Closed is one-way. | **Reopen** exists. A mistaken Close is undone; imports work again. |
| **Archive is safe to fail** | Open case is closed first, then archived. A failed archive leaves it Closed. | Archive does **not** close first. A new transient **Archiving** state; failure, Cancel, a key lock or a crash returns the case to exactly the state it was in. |
| **Archive can be proven** | Write file, compare checksum, mark Archived. | Write to `.part`, read back, rename, then mark Archived under one lock (the single commit point). Cancel and commit cannot both win. Recovery after a crash is automatic and invisible; a left-over complete file is listed, never deleted. |
| **Freeing space is guarded** | "Free the space" after the checksum passed. | A real **verify** first (test restore into a temporary store, checksum and counts), then one second confirmation that names the file and folder and warns about the master key. |
| **Archived is a listing state** | Archived rows can still be exported; analysis summary shown. | Archived cases are never read in place. **Restore** (in place, only while the working copy is kept, always gives Closed) or **Restore from the case file as a new case** (kept or freed, repeatable). |

## Other changes, by area

**Create**
- A request id (kept for one wizard run) replaces "find by title and time", so double press, reload and server restart never make a second case.
- A server stop mid-create re-runs the remaining steps (nothing made twice) instead of only offering Open.
- The "needs another try" list is stored in the case, has a retry per item, and travels in a case file.
- New scenarios: server restart during an import (C17), retry of a tag save (C18), "not added to analysis" versus "import failed" (C19).

**Open**
- Every failed code (not found, malformed, erased, archiving, archived) gives the same sentence plus one fixed line: "Archived cases are in the Archived list." Nothing leaks.
- Archiving and Archived cases are never opened by code or link.
- The list is paged 50 at a time, sorted by last activity, with its own Archived toggle and total.
- A **Missing** case (folder gone) is shown in the list it belongs to and never removed silently; only Erase (and Restore as new) work on it.

**Archive**
- Typed full local folder path with exact rules: no network, relative or `..` paths; symlinks and short names resolved; not in the data folder; at most 240 characters with the file name.
- File name `<slug>-<short-code>-<yyyymmdd-hhmmss>.unravis-case`; never overwrites; slug rules with test vectors.
- Free space checked (1.2 times the case size) on the target volume. Over the size cap: disabled with a message.
- Runs as a background job with percent and Cancel, sharing the Export and Restore lock.
- The case keeps a **history** (every file made, every restore as new) until it is erased. A key check value ("Key ending xxxx") is stored per file.
- Unravis never deletes a finished `.unravis-case`. An orphan file is listed in the health panel and left alone.
- Master key reset warns first, counting archives that only exist as files.

**Erase**
- Short code typed. A running import is stopped first, live streams are closed, then up to 30 seconds for readers before "in use". Nothing is marked until the wait is over.
- Crash-safe: an Erasing mark is the commit; recovery (start-up, unlock or the next call) finishes it. Freeing has the same rule and its own message (M19). A Freed case's erase text says where the case file is.
- Erase is hidden on an Archiving row; the server refuses it.

**Rules between tabs**
- One lock per case. The page sends the state it believes; any mismatch is "This case changed in another tab", with a short list of exceptions and a worked table.

**Demo and key**
- Demo never reads or asks for a key.

**Analysis**
- Catalogue unchanged in spirit (N1 to N17). Inputs made concrete: concurrency for Little's Law comes from the audit documents themselves; group sizes 20 and 100; IIS clock tolerance 1 second.
- Permission-check figures stay **planned, not collected, outside acceptance**; the app shows them only as "Planned".
- Open questions now listed: what backs the permission check, a security review of the key check value, what a key reset does to working copies, the size cap number.

**Testing**
- Restore in place sets a real closed time; Verify says M13 when the key changed; a state table with Missing and Freeing / Erasing columns.
- Message ids M1 to M19 (new sentences), an Appendix C with test steps per criterion, named failure-injection points (INJ-1 to INJ-10), and a "state only" list. Acceptance criteria grew from 6 to 10.

## Changes in v7 (owner rulings)

- **Keys are per case and optional.** A case can have its own key (off by default). The PC-wide master key and "Not now" stay. A master key reset does not touch a case with its own key.
- **No new link or token.** The existing one-time launch link stays; the spec adds nothing.
- **Reopen** applies the saved Mongo link and filters and lets the person run the sampler again. It never starts it.
- **Archive limit** is the existing case file limit (8 GiB of database); no new number.
- **Key check value** reviewed and accepted (HMAC under the derived key; full value only inside the sealed record).
- **Restart mid-import** leaves nothing partial: an import is one transaction.
- **Permission check** figures: nothing built now; still planned.
- **Wireframe** rebuilt from the real app's styles and screens.

## Size
v1: 15 sections, 12 archive scenarios, 6 criteria. Final: 15 sections, 18 archive scenarios, 19 create scenarios, 10 criteria, 19 messages, a state table with eight columns, and a decisions log.
