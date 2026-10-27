# Case lifecycle and analysis: create, open, archive (spec v1)

Status: v1 draft, written 2026-10-26, before the outside review. Later versions are listed in `2026-10-26-case-lifecycle-decisions.md`; the plain difference is in `2026-10-26-case-lifecycle-v1-to-final.md`.
Wireframe: `buildout/wireframes/case-lifecycle.html`.

## 0. Purpose, scope, words

Unravis is a local tool. An engineer investigates a slow or failing system by gathering evidence (audit logs from MongoDB, IIS logs, app logs, SQL Server and Redis facts, counters) into one **case** and reading the analysis of it.

This spec covers three flows and the analysis that follows them:
1. **Create** a case.
2. **Open** a case (from the list, by code, by restoring a case file, by link, by resume).
3. **Archive** a case (new; nothing like it exists today), plus restore from archive and erase.
4. Which analyses help, what each needs, and which data is planned but not yet collected.

Words used (one meaning each):
- **Case**: one encrypted per-case store on this computer. Id: 26 characters (a-z, 2-7). **Case code**: the id typed by a person (spaces and dashes ignored). **Short code**: the first 8 characters, used to confirm an erase.
- **Master key**: the key that protects every case on this computer. States: `setup` (none yet), `locked`, `unlocked`, `damaged`.
- **Source**: one thing a case reads from (an audit database, an IIS log, a file, and so on), tagged Environment > Portal > API.
- **Import**: reading one source into the case. **Case file**: one encrypted `.unravis-case` export of a case.
- **Demo mode**: the server runs with sample data and no case store.
- **Evidence**: rows a case holds. **Ledger / theories**: the case's saved theories. **Story**: the saved event list of a case.

Out of scope: the Blackbox recorder, remote scanning, multi-user sharing, Linux, changing how imports parse.

## 1. As built today (facts the rest builds on)

- Screens: `loading, unlock, setup, start, create, running, case`. Moves are limited by `screenFlow.ts`; a refused move changes nothing.
- First screen: demo gives `running`; locked or damaged key gives `unlock`; a link with a view, or a remembered tab, gives `running`; otherwise `start`.
- Start screen always shows three cards: use an existing case, enter an existing case, create a new one.
- A case has: id, title, created, closed (a time or none), imports, bytes, untrusted flag.
- API: list and create (`/api/cases`), read and erase (`/api/cases/{id}`; erase body carries the id as confirmation), close (`/close`), imports and import jobs, events, series, resource, collect, timeline, call-trace, sources and tags, environments, mongo-sources, ledger, workspace, health, sampler, export, and `/api/cases/restore`.
- **Closed** is one-way today: a closed case can be read, nothing can be imported (`case-closed`, 409). There is no reopen and no archive. A closed case can be erased.
- **Untrusted** (legacy folder not known when the key was set up): listed, never opened (`untrusted-case`, 409); it can only be erased.
- Restore from a case file always makes a **new** case and never changes an existing one. A wrong key, path or damaged file gives one fixed sentence.
- A locked key answers 423 on every case route.
- Wrong or malformed case code gives one sentence for every reason; the screen never says which.

## 2. Case states and moves

States: **Open**, **Closed**, **Archived** (new), **Untrusted** (a flag, not a stage), **Erased** (gone).

| From | Move | To | Rule |
|---|---|---|---|
| (none) | Create | Open | Section 5. A case is never half-made and unnamed. |
| Open | Close | Closed | Asks first. Stops any running import for that case. Readable, no new imports. |
| Closed | Archive | Archived | Section 7. |
| Open | Archive | Archived | Does Close first, in one confirmed step. |
| Archived | Restore | Closed | While the working copy is still kept. Otherwise "Restore as a new case" (a new id). |
| Open / Closed / Archived | Erase | Erased | Section 8. Needs the short code typed. |
| Untrusted | Erase | Erased | The only move. |

No move from Closed back to Open in v1 (a closed case stays closed, as today). The reason: closing is how a person says "the evidence is final".

## 3. Preconditions that every flow checks

Checked in this order, and the first failure decides the screen:
1. **Server reachable.** If not: "Unravis could not be reached", with Try again; nothing else is shown as if it worked.
2. **Demo mode.** No case store: create, open, archive and erase are hidden. The analysis opens on sample data. The start screen is not shown.
3. **Master key.** `setup`: only "Set up the master key" is offered. `locked` or `damaged`: the unlock screen; after a right key the person returns to the work they left (`afterUnlock`). A key that locks while a flow is running keeps the flow's state and says "locked"; nothing is made twice on return.
4. **Case store health.** If the list cannot load: say so, keep Create and Enter usable.

## 4. Start screen

- Three cards always shown: Use an existing case, Enter an existing case, Create a new one. A locked screen names no case and shows no count.
- Use an existing case lists up to 50 cases, newest first, with title (or "Untitled case"), state chip (Open, Closed, Archived), imports, sources, last opened. "Show more cases" adds the rest. Archived cases are hidden by default behind an "Archived (n)" toggle.
- Zero cases: "No cases yet. Create a new case to begin."
- An untrusted case is listed with its sentence and an Erase action only.
- Focus lands on the heading on arrival; Back returns with focus on the card used.

## 5. Create a case

### 5.1 Steps
1. **Name it.** 2 to 80 characters; Next disabled until valid. Hint: no passwords or customer names. Duplicate names are allowed; the two get different codes and the screen says so.
2. **Look around.** Scan of this computer (names and sizes only). Findings with chips: ready, needs a connection, needs a file. States: scanning, finds things, finds nothing (calm, can continue). A scan older than an hour is stale and must be repeated.
3. **Connect.** Add connections: MongoDB, SQL Server, Redis, IIS logs, app logs, a file. Each is tested before it counts. Passwords live in memory only. A Mongo connection asks "does it hold API audit logs?" and the person sets each database to API audit logs, other data, or not used.
4. **Review.** Case name, environments, every source with its tags, what will happen to each (read from this computer, read from a file, named only), the how-far-back window, and the notice the person must tick. The button reads "Create case", or "Create empty case" with no sources.

Back is always free. Forward only when the step is valid. Nothing is saved until the button at the end. Leaving says "nothing is saved until the last step".

### 5.2 Submission order and partial failure
1. Make the case (if this fails: nothing saved, button stays, "try again").
2. Save environment names.
3. Save each source's tags.
4. Add audit sources to the analysis.
5. Start each import (async jobs, with percent).

After step 1 the case exists and is **kept** whatever happens next. Each later failure names the source and the reason in plain words, and the case opens with a "needs another try" list reachable from Add more data. Nothing is made twice on a retry.

### 5.3 Create scenarios (each must be handled)
| # | Situation | Behaviour |
|---|---|---|
| C1 | No sources chosen | Allowed; empty case; add data later. |
| C2 | Key locks mid-create | Progress screen says locked; Continue returns after unlock; resumes without repeating finished steps. |
| C3 | Server stops mid-create | "Unravis could not be reached"; on return, the case made in step 1 is found by title and creation time, and the person is offered Open it. |
| C4 | Scan stale or none | Cannot review scan-based sources; go back and scan again. |
| C5 | Connection test fails | Specific reason (unreachable, wrong login, no permission); source not added; Create still valid without it. |
| C6 | Rate limited on tests | A short pause is asked for, with the wait shown. |
| C7 | File source with no file chosen | Create disabled with the reason. |
| C8 | Window not chosen / ends before it starts / too long | Create disabled with the reason. |
| C9 | Tag empty, too long, or looks like a password | That source's tags are refused; the case is kept; the source is listed as needing another try. |
| C10 | Case or disk cap reached | The step says the cap; kept data stays. |
| C11 | Same file added twice | Warned in review ("this looks like a file already chosen"); allowed. |
| C12 | Window closed during create | Create continues on the server; reopening the app finds the case in the list. |
| C13 | Demo mode | Create hidden, explained in one line. |
| C14 | Browser back during wizard | Stays inside the wizard's own step history; leaving asks. |
| C15 | Double press on Create | One case only. |
| C16 | Notice not ticked | Create disabled: "Tick the box above to confirm the notice." |

After success: "Your case is ready. Opening it now." and the case home opens. If any source failed: the case is kept and opened anyway, with the list of failures.

## 6. Open a case

### 6.1 Ways in
1. **From the list**: Open on a row.
2. **By code**: type the 26-character code. Case-insensitive; spaces and dashes ignored; live counter "n of 26"; characters outside a-z and 2-7 are refused with a plain message. Not found, malformed, erased, or a server error: the same one sentence ("That code does not match a case on this computer.").
3. **Restore from a case file**: full path ending `.unravis-case`, plus the master key the file was made with. Always makes a **new** case.
4. **Link**: the address carries a view; goes to the analysis directly (after unlock if needed).
5. **Resume**: a reload in the same tab returns to the remembered investigation.

### 6.2 What opens, by state
| State | Result |
|---|---|
| Open | Case home. Everything works. |
| Closed | Case home with "This case is closed. You can read it, but nothing can be added." Read-only. |
| Archived | Not opened in place. The row offers Restore (section 7.4). By code: "This case is archived" with Restore. |
| Untrusted | Not opened. Its sentence and Erase. |
| Erased | Behaves as not found. |
| Locked key | Unlock first, then continue to the case. |

### 6.3 Case home
One page per case: Story so far (oldest first, only what the case saved), Theories, Health checks, Analysis, Files. Buttons: Open the analysis, Add a file, Add a theory, All cases. Failures: "This case could not be found. It may have been erased." and "The case could not be opened. Try again."

### 6.4 Open scenarios
| # | Situation | Behaviour |
|---|---|---|
| O1 | Two tabs open the same case | Reads share; erase waits for readers to leave. |
| O2 | Another process holds the case | Not supported in v1: the lock is single-process; the person is told it is in use. |
| O3 | Code typed with a typo | The one sentence; no hint whether other cases exist. |
| O4 | Case erased in another tab | "could not be found. It may have been erased." |
| O5 | Restore wrong key / path / damaged | The one fixed restore sentence; nothing written. |
| O6 | Restore while another save or restore runs | "Another case file is being saved or restored." |
| O7 | Restore throttled | "Too many tries." plus the wait. |
| O8 | Restore without a master key on this PC | "Restoring needs a master key on this PC too." |
| O9 | Large case slow to load | Loading line shown; Try again on failure. |
| O10 | Deep link to a closed case | Opens read-only with the closed note. |
| O11 | Deep link to a missing case | Start screen with the not-found sentence. |

## 7. Archive a case (new)

### 7.1 What it is for
Keep a finished investigation without keeping it in the working list, and optionally free the disk space, with a way back.

### 7.2 What archive does (v1 design)
1. Starts only from Closed (an Open case is closed first, in the same confirmed step).
2. Writes one **case file** (the existing export) to a folder the person names, encrypted with the master key.
3. Checks the file: size and a stored checksum are read back and compared.
4. Marks the case **Archived**. It leaves the default list (shown under "Archived (n)").
5. The working copy is **kept** by default. A separate, second, confirmed action "Free the space" erases the working copy, and is offered only after step 3 passed.

### 7.3 Archived behaviour
- Read-only; not opened in place; its row shows file name, size, date, checksum result, and whether the working copy is kept.
- Export again is allowed while the working copy is kept.
- Erase is allowed (section 8) and, when the working copy is freed, only the record of the archive is removed; the case file is never deleted by Unravis.

### 7.4 Restore from archive
- Working copy kept: "Restore" returns it to **Closed**, same id.
- Working copy freed: "Restore from the case file" always makes a **new** case with a new id; the archived record stays and says "restored as a new case on <date>".
- Wrong key or damaged file: the fixed restore sentence; nothing changes.

### 7.5 Archive scenarios
| # | Situation | Behaviour |
|---|---|---|
| A1 | Folder missing or not writable | Nothing changes; says the folder cannot be written. |
| A2 | Disk full while writing | Partial file removed; case stays Closed; says why. |
| A3 | Checksum mismatch after write | Case not marked Archived; partial file removed; try again. |
| A4 | Key locks mid-archive | Stops safely; nothing is half-archived; resume after unlock. |
| A5 | Import running when Archive pressed | Close stops it first; the import is marked stopped in the story. |
| A6 | Press Archive twice | One archive. |
| A7 | Archive then erase working copy then master key changes | The case file needs the key it was made with; the screen says so before freeing the space. |
| A8 | Archive file moved or deleted by the person | Restore from file fails with the fixed sentence; if the working copy is kept the row still offers Restore. |
| A9 | Untrusted case | Not archivable. |
| A10 | Demo mode | Hidden. |
| A11 | Theories and story | Included in the file; restored with it. |
| A12 | Case with zero imports | Allowed. |

## 8. Erase
- Offered from Open, Closed, Archived and Untrusted rows and the case home.
- Confirmation: the person types the short code (first 8 characters). The button stays disabled until it matches. The text says erase cannot be undone and that a case file made earlier is not touched.
- Waits for readers to leave; says so. Stops samplers for that case.
- Result: the row is removed; the case is not found after.
- Erase of an Archived case with a kept working copy asks for the same short code.

## 9. Scenario matrix: action by state

| Action | Open | Closed | Archived | Untrusted |
|---|---|---|---|---|
| Open in place | yes | read-only | no (Restore) | no |
| Add import | yes | no | no | no |
| Read analysis | yes | yes | no | no |
| Export | yes | yes | yes (working copy kept) | no |
| Close | yes | n/a | n/a | no |
| Archive | yes (closes first) | yes | n/a | no |
| Restore | n/a | n/a | yes | no |
| Erase | yes | yes | yes | yes |

## 10. Error sentences (one place)
Every failure has a plain sentence, never a path, connection string, password or id. Reasons map to sentences in the copy files (wizardCreateCopy, enterCopy, caseHomeCopy, startCopy); new archive sentences follow the same rules: say what happened, what is safe, what to do.

## 11. Analysis that helps

The case's value is what it lets a person explain. Each analysis names the data it needs; if the data is missing the screen says so and does not estimate.

### 11.1 Catalogue
| # | Analysis | Needs | What it answers |
|---|---|---|---|
| N1 | Overview percentiles (P50, P95, P99, max), by route and day | audit documents with total ApiTime | How slow, how often. |
| N2 | Slowest list and Nearby activity | audit documents, other sources in the same window | What else happened beside one slow call. |
| N3 | **Timing split D = A + C** (before-D, audit filter, inside-C filters; sum equals the array within 0.001 ms) | `PreActionFilterTimings` array | Which layer holds the time. |
| N4 | **Filter share** per filter (for example the permission-check lane, S7) | the array | Which filter owns the time, and its share of slow calls. |
| N5 | **Clustering by start minute** and `shared_stall` | audit start times and durations | Whether slow calls hit together (shared cause) or alone. |
| N6 | `errors_up` | status codes | Whether failures rose with slowness. |
| N7 | **Little's Law** (L = λW) and `near_saturation` | request rate, durations, a concurrency measure | Stalled or overloaded. |
| N8 | **Amdahl bound** 1 ÷ (1 − p) | the share p from N4 | The most a fix of one part can give (an upper bound only). |
| N9 | **Slow versus normal comparison** (BubbleUp style) | a slow group and a normal group with at least 20 requests (P95) or 100 (P99) | What differs: route, host, user group, minute. |
| N10 | IIS time (O = IIS time − A) | IIS logs with a clock tolerance | Time spent before the app. |
| N11 | SQL Server waits, Query Store, blocking | SQL health read | Whether the database held the time. |
| N12 | MongoDB health (profiler, serverStatus, currentOp, log) | Mongo health or files | Whether Mongo held the time. |
| N13 | Redis slow log | Redis export | Whether Redis held the time. |
| N14 | Hangfire recurring jobs | SQL read or CSV | Whether background work coincides. |
| N15 | App log errors | app logs | Errors beside slow calls. |
| N16 | Host counters | sampler, perf counters | CPU, memory, handles beside slow calls. |
| N17 | **Permission-check counters** (planned, section 12) | new collection | Whether the permission check itself, or what backs it, was slow or failing. |

### 11.2 Rules (from the performance-principles work)
- Measure first: no cause is claimed without evidence from the case.
- Every recommendation is a finding with its evidence and what it would change; never generic advice.
- A model is shown only when its needed data exists; otherwise the screen names the missing data (for example "needs a count of requests in flight").
- Float comparisons use a tolerance; tier ordering needs a clock tolerance; small groups show their size and are not ranked.
- Percentiles show their request count.

### 11.3 What helps at each lifecycle point
- **After create**: a "what this case can answer" list, built from the sources actually present (N1 to N6 with audit only; N10 with IIS; and so on) and "what it cannot answer yet" with what to add.
- **In an open case**: Add more data suggests the next source that unlocks an analysis.
- **In a closed or archived case**: the same analyses, read-only; an archived case shows its summary line only (sources, window, date).

## 12. Planned data: permission-check figures

Planned, to be developed; not collected today.

| Figure | Why |
|---|---|
| Permission-check latency per call (and a histogram per minute) | Show whether the permission lane, which holds the largest share of slow time, is slow by itself. |
| Permission-check error and retry counts | Rule out failures hidden behind slowness. |
| Latency and error counters of whatever store backs the check | Separate "the check is slow" from "its store is slow". What backs it is not yet verified. |
| Counters at the stall minutes | Show whether a shared stall (N5) coincides with the store. |

Collection rules: aggregated per minute, no payloads, no user ids; same time base as the audit documents; stored as a new source kind in the case; the analysis N17 appears only when the source exists.

## 13. Quality bars
- Plain words; no jargon without a link.
- Keyboard: every action reachable; focus moves to the heading on a new screen; errors announced (role alert); colour never the only signal; WCAG AA in both themes; 390 to 1600 px.
- Security: no credentials in the case, logs, exports, URLs; case codes are identifiers, not secrets; connections are read-only.
- Copy goes through the copy lint.

## 14. Acceptance criteria (v1)
1. Every scenario in sections 5.3, 6.4, 7.5 has a defined message and result.
2. Archive never leaves a half-archived case and never deletes a case file.
3. Erase needs the short code and never touches a case file.
4. Restore always makes a new case unless the kept working copy is restored in place.
5. The analysis catalogue shows only what its data supports.
6. The wireframe shows each flow and the main states in both themes.

## 15. Open questions
None recorded yet. The review loop fills this section.
