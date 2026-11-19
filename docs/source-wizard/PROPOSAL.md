# One source wizard for Unravis

Proposal, 2026-11-19. Docs only: no app code changed, no backlog item ticked.
Prototype: [`source-wizard-prototype.html`](source-wizard-prototype.html) (v3b, one file, double-click). Earlier versions: [`v1`](source-wizard-prototype-v1.html), [`v2`](source-wizard-prototype-v2.html).

**v3b (2026-11-19): status fix, Open folder, add to an existing case: see section 9.** **v3 (2026-11-19): Find on this PC first, Blackbox auto-detect, computer detection: see section 8.** **v2 (2026-11-19) redesigns only step 2, "Add sources": see section 7.** Sections 1 to 6 are the v1 proposal and still hold for the rest of the flow.

## In a nutshell

Make "create a case" = **Name step + the same Add-sources wizard**. Adding to an existing case runs that same wizard without the Name step. One screen to pick a source type, one shape for every source (Where, Check it works, Choose what to read), one tray that shows what you are about to add, one final button.

## 1. What is inconsistent today

Verified in the running app (demo instance, screenshots in `shots/`) for the create flow, and from code only for the add-to-case flow (demo mode has no case pages). Not verified on live data.

| Area | Create case | Add to existing case |
|---|---|---|
| Container | Full-page 3-step wizard (Name, Sources, Review) | A 7-tab hub, plus a modal Import popup, plus a right-hand Connections drawer, plus a Blackbox side panel |
| Mongo | `MongoClassify` form: "Use a saved connection", "Test and list databases" | Connections drawer form: "Test connection & load databases", "Save connection", "Add selected databases" (other words, other order, TLS select with 5 options) |
| Files | Pick a card type first (IIS, App logs, Query Store, Audit export), then add files | Drop zone that auto-detects the type |
| Blackbox | Not offered | A card on "Add data" that opens a side panel, which is independent of any case |
| SQL / Redis | SQL and Redis by address | SQL is its own tab (Step 7); Redis is not in the hub |
| Labels (Environment, Portal, API) | Tag row on every source | No equivalent at add time |
| Timing | "Nothing is read until the case is made" | Popup imports immediately |
| Words | "On this computer / Another computer" | "Step 7 / Step 8", "lane", "profile slots" |

Extra clicks, example (Mongo into an existing case): Add data tab, Mongo card, Connections drawer, New connection tab, fill form, Test, tick databases, Add. About 8 steps across 3 surfaces. In the create flow the same job is 3 steps in 1 surface.

Confusing spots: the split by where the data lives ("this computer / another computer") rather than what it is; Blackbox is a different world from every other source; two vocabularies for one idea.

## 2. Research (what power tools do)

| Tool | Pattern worth borrowing | Source |
|---|---|---|
| Grafana | Search a type, "Add new data source", fill one Settings page, **Save & test** gives a clear pass/fail | [Grafana docs](https://grafana.com/docs/grafana/latest/administration/data-source-management.md), [save and test](https://grafana.com/docs/learning-paths/loki-data-source/save-and-test.md) |
| DBeaver | Choose a driver from a searchable list, a Main tab, optional advanced tabs, **Test Connection** before Finish | [DBeaver docs](https://dbeaver.com/docs/dbeaver/Create-Connection/) |
| Splunk | Fixed sequence: Select source, Input settings, **Review**, Submit; sensible defaults, skip the type step when it can be detected | [Splunk tutorial](https://help.splunk.com/en/splunk-enterprise/get-started/search-tutorial/9.1/part-2-uploading-the-tutorial-data/upload-the-tutorial-data) |
| Kibana / Elastic | Integrations are the single entry point for "add data"; guided checklist on top | [Kibana docs](https://www.elastic.co/guide/en/kibana/8.8/connect-to-elasticsearch.html) |
| Datadog | Install, then a Configuration tab per integration (setup is a separate, optional phase) | [Datadog integrations](https://docs.datadoghq.com/getting_started/integrations/) |
| Azure Data Studio | Saved and Recent connections side by side in one dialog | [Microsoft docs](https://learn.microsoft.com/en-us/azure-data-studio/connect) |
| Perfmon | Same Add Counters dialog is reached from the graph and from the collector-set wizard | [Microsoft docs](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-vista/cc749266(v=ws.10)) |
| Wizard design | Steps lower perceived effort; only show a progress indicator where it clarifies the journey; show the true step count; keep entered data on Back; make optional steps skippable | [Dolfy](https://www.dolfy.ai/blog/multi-step-forms-wizards-users-actually-finish), [Baymard](https://baymard.com/blog/progress-indicator-ui-ux), [Orbit Forms](https://orbitforms.ai/blog/multi-page-form-best-practices) |

Caveats: I did not find a Nielsen Norman Group page in the search; the wizard-design links are secondary sources and some figures there come from vendors. Datadog, Postman and Splunk details come from search snippets, not from opening every page. I did not research Postman (no useful result for it).

Takeaways:
1. **One entry point** per job (Kibana, Perfmon): the same dialog whether you start fresh or add later.
2. **Pick the type, then one common form shape** (Grafana, DBeaver).
3. **Test before commit** with a plain pass/fail (Grafana, DBeaver).
4. **Review before submit** (Splunk).
5. **Detect before asking** (Splunk compressed files, Unravis scan).
6. **Saved and new live together** (Azure Data Studio).

## 3. Principles for Unravis

1. One wizard, two doors. The Name step is the only difference.
2. Say what it is, not where it lives. Type tiles, not "this/another computer".
3. Same three beats for every source: **Where** (address or path), **Check** (Works / Failed, using the fixed status words), **Choose** (databases, files).
4. Detect first. Scan and drop zone offer suggestions before any form.
5. Nothing is read until the last button, in both modes. Say it once, at the top.
6. A tray, always visible, shows what will be added. Redundant with the form, but it saves a trip to Review.
7. Labels (Environment, Portal, API) are optional and live in the tray row, never a separate screen.
8. Plain words for a first-time investigator. No "Step 7", "lane", "slot".
9. Calm: one primary button per screen, outline buttons, stickers for information, no hatched or green for "waiting" (brand rules).

## 4. Proposed flow

```
New case:     [1 Name] -> [2 Add sources] -> [3 Review & create]
Existing case:            [1 Add sources] -> [2 Review & add]
```

**Add sources** (one screen, two zones):
- Left: type tiles (MongoDB, SQL Server, Redis, Files, Blackbox, Find on this PC). A drop zone sits above them: drop files or a folder anywhere and Unravis picks the type.
- Right: the form for the chosen tile. Always the same order: Where, Check it works, Choose what to read, then "Add to tray".
- Bottom: the tray (source name, type, optional labels, remove).

**Review**: the tray as a read-only list, a nutshell line ("3 sources, read-only, nothing changes on those servers"), and one primary button: "Create case" or "Add 3 sources to Case X".

Blackbox: "Use a recorder I already have" lives in the wizard (folder, share or address). "Place a new recorder" needs Windows admin approval, so the tile hands off to the existing Place wizard and returns. **Unverified**: how that hand-off should resume the wizard.

Saved connections: Mongo/SQL tiles open with a "Saved" dropdown first, as in Azure Data Studio.

## 5. Choices for the owner

1. **Staging in add mode.** Today the popup imports immediately. Proposal: stage in the tray and commit at the end in both modes (consistent, one extra click for the single-file case). Alternative: keep "Add now" as a second button for one source.
2. **Name step.** Keep Name as its own step (proposal) or fold name into Review for a 2-step create.
3. **Blackbox in the wizard.** Existing recorder only inside the wizard, Place stays a separate flow (proposal), or fold Place in too (needs backend work, so deferred).
4. **Hub tabs.** Keep the Sources hub for management (list, combinations, step panels) and make "Add data" open the wizard (proposal), or retire the tabs.
5. **Typed file cards.** Replace by auto-detect with a "this is wrong, change type" link (proposal).

## 6. What is not verified

- The add-to-case screens were read in code, not seen running.
- Prototype data is illustrative sample data: the scan only runs on a real Windows instance and demo mode returns nothing, so I did not capture live findings. Nothing in the prototype touches a real source.
- No backend contract exists for staging a mixed tray in add mode; the Go side would need a single "add sources" call (likely new work, owner's go needed).

## 7. v2: step 2 redesigned (many sources per type, every field, saved connections)

Owner asks: several sources of one type (many Mongo, SQL, Redis), every field the app has today, saved connections visible, easy to scan for an ADHD reader. Only step 2 changes. Name and Review are as in v1.

### 7.1 Shape

- **Left rail "Your sources"**: grouped by type, each group has a count and its own "+ Add". Add a second or third Mongo from the Mongo group. Click an entry to open or edit it.
- **Main area, one thing at a time.** Either the pick screen (type tiles with an "N added" sticker, plus the drop zone) or one card for one source. A card has a mini stepper, 2 steps at most: Mongo (Connect, Choose databases), SQL (Connect, Choose database), Redis (Address), Files (Pick files, Check types), Blackbox (Find recorder, Choose machine).
- **Saved connections are chips above the address**, with "+ New connection". Picking one fills the form. Editing a filled field detaches it from the saved one.
- **Folds for rare fields.** Sign-in, Encryption, Audit log collection and Labels sit in closed `<details>` with a one-line summary on the closed row ("Mandatory", "optional"). Nothing is hidden for good.
- **One primary button per card** (Test and list databases, then Add to my list). Footer: "Review", or "Skip, add later" when the list is empty.

### 7.2 Field audit (read from code, nothing changed)

| Type | Fields today | Where in v2 |
|---|---|---|
| MongoDB | Saved connection (password saved or not; picking fills address, user, login database, TLS, collection). Address (max 2000). Encryption and server trust, 5 options: verify with system roots, custom CA file (.pem/.crt, 256 KiB max, no private key), skip verification (warning), as set in the address, off (warning). Audit collection (default ApiAuditLog). User, password (memory only). Login database (authSource). Test and list databases. Per database: API audit logs, Application data (audit-looking names pre-ticked). Test again, remove. Note that health checks run in the case. | Chips + address on step 1; Sign-in, Encryption, Audit log collection folds; step 2 per-database segmented control (API audit logs / App data / Both / Skip) |
| SQL Server | "Kept before" list (Use, Remove). Server name (max 260, e.g. `192.168.4.2,1655`). Authentication SQL or Windows. Encrypt Mandatory, Optional or Strict. Trust server certificate. User and password (SQL only). Remember password (sealed to the Windows account). Test and list databases. Database select with "type the name" (empty means the login default). Connection name (optional). Stale warning when details change after a test. | Chips, server name, Sign-in fold, Encryption fold, step 2 database pick, name under Labels |
| Redis | Address (host or host:port, max 260). Check and add: probes the port. Unreachable is still added and marked. No credentials. | One field, one button; unreachable kept and marked "No reply from that port" |
| Files | Drop zone, files, folder. 11 kinds: IIS W3C, HTTPERR, IIS failed request trace, SQL extended events, SQL error log, Query Store export, Windows event log, App log, Hangfire CSV, Redis slow log CSV, Another file. Per-file "Change type". Which computer wrote it (time zone or host) for some kinds. Refusal for too big, unreadable, or private-key files. | Step 1 pick, step 2 "Is each file the right type?" with a guess per file |
| Blackbox | Folder, share or address of a recorder; choose machine. | Two-step card |
| Every source | Labels Environment, Portal, API. Cap 200 sources. Duplicate refused ("already chosen"). "On this computer" scan. | Labels fold; duplicate message per type; scan stays a card on the pick screen |

### 7.3 Multiple sources of one type

Matches today's data model: the list already holds many Mongo, SQL and Redis entries and refuses exact duplicates. v2 makes that visible: group counts in the rail, "N added" on tiles, "+ Add" per group, and the same card reopened each time. The prototype checks that 2 Mongo, 1 SQL, 3 Redis (one unreachable) and a files source can sit on one list, and that a duplicate address is refused.

### 7.4 ADHD-friendly rules applied

1. One card at a time, 2 steps at most, a visible mini stepper.
2. The first screen of a card holds only what is needed to connect. Rarely used fields are folded, with their current value shown on the closed row.
3. Short labels in plain words ("Which SQL Server?", "What is in each database?"). Each has one hint line, not a paragraph.
4. One obvious next action: a single filled button. Cancel and Change connection are outlined and quieter.
5. Choices with 3 or 4 options are segmented controls, not dropdowns, so they can be seen at once.
6. The rail is the memory: what you have already added is always on screen, so nothing has to be held in the head.
7. Status uses the fixed words (Works, Failed, Reached the port) and one colour mark, not paragraphs.
8. Reassurance in the footer: "Nothing is read until the last button."

### 7.5 Compared with today (screens in `shots/`)

| | Today (`03-create-sources`, `04-create-mongo`) | v2 (`v2-*`) |
|---|---|---|
| Layout | Long page; four buttons, then a long form under them | Rail + one card |
| Saved connections | A select inside the form | Chips on top |
| Many of one type | Possible, but a flat list under a form | Grouped, counted, "+ Add" per group |
| Fields visible at once | All of them | Only the needed ones; the rest folded with a summary |
| Databases | Two checkboxes per database | One segmented control per database |
| Words | "On this computer / Another computer" | Type names |

### 7.6 Not verified

- Sample data only. Nothing here touches a real source, and demo mode has no add-to-case screens, so those were read in code only.
- The field list comes from reading the Svelte components and `sqlConnection.ts`. It was not diffed against the live Go validation.
- The Blackbox card shows the shape only. The Place hand-off is still open (section 4).
- Mobile and light theme were not reviewed.
- Moving the SQL Windows-login, Redis and Blackbox details into this card needs no new backend. A single "add many sources" call for add-to-case is still new work, owner's go needed.

## 8. v3: Find on this PC first, Blackbox auto-detect, computer detection (owner round 3)

Prototype: `source-wizard-prototype.html` (v3). v2 kept as `source-wizard-prototype-v2.html`. Screens: `shots/v3-01..04`.

**1. Find on this PC is the first, highlighted tile** ("Start here"). It runs, shows what it found with a status word, and the investigator ticks what to add. Ready items have a tick box. MongoDB and SQL Server show "Connect..." which opens the normal card with the address filled in, then returns to the list. Not found (Redis) offers "Add by address". Added rows show "Added". One main button: "Add N ticked". Also "Look again".

**2. Blackbox auto-detect.** Opening the card runs a detect step. Recorder ON: its folder is read from the recorder service and offered. Recorder OFF: default folders on all readable drives are searched; the folders found are ranked by newest log, the newest is marked Recommended and preselected, and the person can pick any or type their own ("Another folder"). "Check it works" shows Works or Failed inline. "Place a new recorder instead" stays secondary. A dashed "Prototype only" row flips ON/OFF so both paths can be seen.

**3. Files: add or edit the computer.** Each file gets the computer found in its header as a suggested chip, next to the saved computers and "+ Add a computer" (name and time zone). "Edit suggestion" renames it. If nothing is found the hint says so.

### What the real app needs (new backend work, needs the owner's go)
- Find on this PC today reads file names only. New: list Windows services and listening ports (Mongo 27017, SQL Server and SQL Browser, Redis 6379); read the recorder service's folder (service config or registry); scan default folders on every readable drive; read newest log file time; read IIS/SQL headers for the computer name; check the SQL Windows login without a password prompt.
- New collector/agent items are deferred in BACKLOG: nothing here was built or ticked.
- Add to case uses the same screens (read in code only).

### Not verified
Sample data only. Nothing detects anything. No backend contract. Tested in the browser pane: flows, no console errors.

### Design check
Impeccable skill (already installed) read; its detector launcher is not in the install and `npx` download was blocked, so only a manual pass was done: removed a unicode dot used as a state marker, no eyebrow labels, no gradient text, no colour side stripes.

## 9. v3b: status fix, Open folder, add to an existing case (owner round 4)

Prototype: `source-wizard-prototype.html` (v3b). Screens: `shots/v3b-01..05`. Real app, read live on a throwaway demo case: `shots/real-*.jpg`.

### 9.1 Status bug
**Cause:** the "Find on this PC" row looked up an added source by a key that a signed-in Mongo or SQL source never matched (it had the typed address, for example `.\SQLEXPRESS` or `127.0.0.1`, not the scanned one). **Fix:** compare by host after normalising `.`, `(local)`, `127.0.0.1`, `::1` to `localhost` and dropping the default port. Now a row turns **Added** (or **Added, check it**) the moment the source is on the list, with "On your list as <title>", and always matches "Your sources". Checked in the browser: connect, test and add for Mongo and SQL; the Redis row after "Add by address"; tick and add all; the left list matches every time. Not yet checked beyond that: remove from the list, then look again (the row should go back to Found).

**Correction (same day, owner retest): the host match was not enough.** If the investigator picks a saved connection (for example audit-prod on db01) after pressing Connect on the scanned `localhost` row, the saved source has a different address, so the row stayed "Needs sign-in". Fix: the row now links to the source by id (the source remembers which scan row it came from). Removing the source from the list returns the row to its found state on the next look. Also fixed: switching between Create and Add left old sources and scan results behind. Click-through in both modes (cancel, add Mongo and SQL with other addresses, remove, look again, re-add with the scanned address): every row state was correct. Screenshot: `shots/v3c-01-status-fixed.jpg`.

### 9.2 Open folder
Every folder path now has an **Open folder** button: scan rows (IIS logs, app logs, Blackbox) and every Blackbox candidate. In the prototype it only shows a message. **The real app needs:** one local-only backend call that opens Explorer on the path (loopback request only, same one-time token as other calls), checks the folder exists and is a directory, only accepts paths the scan returned or the user typed in this session, and starts Explorer with an argument list (never a shell string). Needs the owner's go (new backend).

### 9.3 Add to an existing case: what exists today
Case Home embeds a **Sources hub** with seven tabs (Connections, Sources, Step 7 SQL Server, Step 8 MongoDB, Add data, Add another day, Combinations). **Add data** has five cards, and each opens a different surface:

| Card | Opens | 
|---|---|
| SQL Server | the Step 7 tab |
| MongoDB | a full page ("Add an API audit database") |
| Blackbox | a right-side panel (place, scan this PC, add by folder or address) |
| Files | the "Import files" pop-up (Choose, Check, Result) |
| Another day | a tab |

No Find on this PC and no Redis there. Read-only or closed cases say "This case cannot take new data right now: it is closed, or it changed somewhere else." Footer notes: a step that ran before the new data is "Out of date, rerun"; "Reading does not change any server." Shots: `shots/real-add-data-cards.jpg`, `real-hub-connections.jpg`, `real-add-mongo-audit.jpg`, `real-blackbox-panel.jpg`, `real-import-files-popup.jpg`.

### 9.4 Proposed: the same step 2, in the case
- Same tiles, Find on this PC first, scan results, Open folder, connect forms, tray. Only the Name step is left out. Two steps: Add sources, Review.
- The left tray shows **Already in this case** (read only, "In case") above **To add now**, so nothing is added twice and the investigator sees the whole case.
- **Another day** stays a tile: pick which source, then the files form opens with that source named.
- Closed or read-only case: the same plain message, no tiles. (Prototype button "closed" shows it.)
- Review says what stays as is and that earlier steps are marked Out of date, rerun. Last button: "Add N sources to case".
- Redis and Find on this PC become available in a case, which today they are not.

### 9.5 Real-app needs and not verified
- Find on this PC and Redis in a case need the backend work in section 8 (deferred, owner's go). One "add many sources" call is also new.
- The old tabs and pop-ups can stay while this ships; the hub's Add data cards would open this wizard.
- Not verified: sample data only; the add-to-case flow was read in code and viewed live on demo data, not built; the "already in this case" list is sample data; light theme and mobile not reviewed; Open folder is a message only.
- Keyboard: Blackbox candidate cards now respond to Enter and Space.
