# Unravis prototype: step-by-step guide to every flow

This guide walks a first-time user through every flow in the Unravis prototype: the flows the real app already has, and the new **Investigation Timeline**. It uses the exact button and field names the prototype shows.

**Read this first**

- This is a throwaway **prototype**. All data is **synthetic**. Nothing leaves the browser. It shows how the screens are meant to behave; it does not prove the real software works.
- The prototype clock stands still at **20 October 2026, 12:00 UTC**. The demo case is "Payroll.Run slowdown, 20 Oct". Its API slows down at 09:14 UTC.
- Where a flow cannot be reached in the prototype, or a screen does not exist, this guide says so plainly. Nothing here is invented.
- Every image is in `prototype/video/images/flows/`, named `NNN-short-name.png` (three digits, because there are more than 99). The last section is an index: file, what it shows, how to reach it. The images can be re-made with `NODE_PATH=/opt/node22/lib/node_modules node prototype/video/capture-flows.cjs`.
- In the images the prototype's own bottom bar is hidden, unless the image is about that bar.

## 0. Words you will meet

| Plain word | What it means |
|---|---|
| Case | One investigation: its files, sources, theories and notes, kept on this computer. |
| Source | Somewhere evidence comes from: a log file, a SQL Server database, a MongoDB database. |
| Master key | One key for the whole PC. It locks every case on this computer. |
| Case key | An optional extra key for one single case. |
| Case code | A short name for a case, such as PAYROLLR. The lists show 8 characters; the "Enter an existing case" card asks for the full code (26 characters). |
| Lane | One kind of evidence drawn as a row on the timeline: **API audit**, **SQL**, **IIS**. |
| Investigation window | The period you are looking at. (The older analysis screens call it the incident window.) |
| Comparison period | An earlier period to compare against. It is only a comparison. It is not a promise that things were healthy then. |
| Bucket | One bar on the timeline. A bar says how long it covers, for example "Each bar = 5 minutes". |
| P95 | The time within which about 95 of 100 requests finished. |
| Suspect | A possible explanation the timeline checks. A suspect is not a cause. |

## 0.1 The prototype-only controls (bottom bar)

At the bottom of every prototype page there is a bar that is **not part of the product**. It exists so a viewer can jump straight to a prepared situation without clicking through everything. A tester can finish the normal journey without it.

1. **Prepared situations picker.** A drop-down labelled "Prepared situations for this screen…" (or the name of the current situation). It lists only the situations for the screen you are on, grouped: for example "Start screen", "Launch conditions", "Create a new case", "Case home", "Analysis · windows", "Export drawer", "Investigation Timeline · lanes", "Investigation Timeline · where does the evidence point". The arrows ‹ and › beside it step through the list; so do the left and right arrow keys.
2. **Layout pill** (only on the Investigation Timeline): "Layout A · Report", "B · Workbench", "C · Lane ledger", with ‹ and › to switch.
3. **What if:** buttons (on the timeline and the analysis): switch on a fault or condition, such as "Audit read exceeds its budget" or "SQL commands time out (D16)".
4. A line showing the last action and what changed, and two buttons: **Prototype** and **State**.
5. **Prototype** opens the Prototype panel: **Launch the app as** (Normal, Key locked, Key damaged, Fresh PC), **Release stage** (MVP-1 only, MVP-1 + MVP-2), test aids such as a button that shows the full case code, **Reset prototype data**, and the list of assumptions and questions. **State** shows the full state after every action.
6. When a situation is loaded, a banner "PROTOTYPE SCENARIO" explains it and suggests what to try. It has a **Hide** button.
7. The bar can be folded to "Prototype controls ▴".

Images: `253-prototype-dock-bottom-bar-timeline.png`, `254-prototype-panel-launch-and-stage.png`, `255-prototype-state-panel.png`.

---

## 1. Launching the app and the master key

### 1.1 Normal launch (key already unlocked)

**Purpose:** reach the start screen.

1. Open `tracing-paper-PROTOTYPE.html` in a browser.
2. You see the start screen titled "Start an investigation" with three cards (see section 2).

Image: `001-launch-start-screen.png`.

### 1.2 Master key locked

**Purpose:** protect cases. When the PC key is locked, nothing about any case is shown until you unlock.

1. Launch with the key locked (prototype panel, "Launch the app as", "Key locked", or the situation "Master key locked at launch").
2. You see "Unlock your cases". Text: "Type the master key for this PC to see your cases. The key is not sent anywhere." One field, **Master key**, and a button **Unlock**. No case title or count is shown.
3. Type the key and press **Unlock**. (Test aid in the prototype: the key is `orange-sunrise-2026`.)
4. You land on the start screen.

What can go wrong:
- Wrong key: "That master key is not right. Nothing was opened or changed."
- Three wrong tries in a row: "Too many tries. Try again in 5 seconds." Wait, then try again. (The prototype uses 3 tries and 5 seconds; the real server decides the real numbers.)

Images: `002-key-locked-unlock-screen.png`, `003-key-wrong-first-try.png`, `004-key-wrong-throttled.png`, `005-key-right-after-wait.png`.

### 1.3 Master key file damaged

**Purpose:** recover when the file that holds the master key cannot be read.

1. Launch with the key file damaged.
2. You see "The master key file is damaged. The file that holds the master key for this PC cannot be read. It may be damaged, empty or replaced. No master key can open it." and reassurance: "Nothing was changed. Your cases and the file are still where they were."
3. The screen explains the choice: "If you cannot put the file back from a backup, you can start over with a new master key. The damaged file is kept aside, not deleted. If it turns out to be fine, it can be put back under its old name." A box says what starting over does: "Cases protected by the old master key will stay unreadable after a new one is set."
4. The button **Start over with a new master key** is off until you tick the acknowledgement box.
5. Tick the box, press the button. The screen says "The old file was set aside. Next, set a new master key for this PC." with the button **Set a new master key**.
6. Press **Set a new master key**. **Prototype limit:** in the prototype nothing visible happens at this point. Its own move rules refuse the step from this screen to the set-up form (it records "The move from unlock to setup is not allowed." but does not show it). The set-up form itself can be reached by launching as "Fresh PC" (section 1.4). The real app is expected to open the form here; this was not checked.

What can go wrong: a damaged file means no key can open it. The screen says so and offers the start-over path above.

Images: `006-key-file-damaged.png`, `007-key-file-damaged-ticked.png`, `008-key-file-set-aside.png`, `009-key-set-new-key-button-does-nothing.png`.

### 1.4 Fresh PC: setting a master key

**Purpose:** first-time set-up of the key that protects all cases.

1. Launch as "Fresh PC (no key, no cases)". The start screen shows a link: **Protect your cases with a master key**. The list says there are no cases yet.
2. Press the link. You see "Protect your cases with a master key": "A master key locks the cases kept on this computer. You type it once each time Unravis starts." "Nobody can recover it. If you forget it, your cases cannot be opened again."
3. Type the key in **Master key** (at least 12 characters), type it again in the second field, tick the confirmation box.
4. Press **Set master key**. A confirmation page appears (see below); press **Continue** and you return to the start screen, where the header now shows **Lock cases**.
5. **Not now** leaves without setting a key.

What can go wrong: a key under 12 characters is explained at once; two entries that differ are reported. The **Set master key** button stays off until everything is valid.

After **Set master key** the page says "Master key set. Your cases are locked with it. You will type it each time Unravis starts." with a **Continue** button; press it to reach the start screen.

Images: `010-fresh-pc-start-screen.png`, `011-master-key-setup-form.png`, `012-master-key-too-short.png`, `013-master-key-mismatch.png`, `014-master-key-set-done.png`.

### 1.5 Locking again

In the header, **Lock cases** (shown on the start screen and the case home) goes back to the unlock screen. There is no separate screenshot; it is the same screen as `002-key-locked-unlock-screen.png`.

---

## 2. The start screen and its three cards

**Purpose:** choose what to do: open a case, enter a case code, or make a new case.

The screen is headed "Start an investigation: Open a case you already have, enter a case code, or create a new case." Image: `015-start-three-cards.png`.

| Card | What it is for | Image |
|---|---|---|
| **Use an existing case** | The list of cases saved on this computer. Press **Open** on a row. | `016-start-card-existing-list.png` |
| **Enter an existing case** | A code field with **Open case**, and **Restore from a case file**. | `017-start-card-enter.png` |
| **Create a new one** | **Create a new case** starts the guide. | `018-start-card-create.png` |

A footer link, **Go to the analysis**, opens the analysis of the demo audit source with no case behind it (section 10.9).

### 2.1 The list of existing cases

1. Each row shows the case title, when it started, how many imports and sources it has, and its code ("Code PAYROLLR").
2. Row notes: **Closed**; **Key ending ey01** (the case has its own key); **Legacy case from an older version**; **The working copy is missing** (with an **Erase** button); **Archiving 45%** (no Open button).
3. Press **Open** to open the case. The browser Back button returns to the list.

Other list states:
- No cases: "No cases yet. Create a new case to begin." The three cards stay. Image: `024-start-no-cases-yet.png`.
- The list cannot be read: "Your cases could not be loaded." and **Try again**; Try again works the second time in the prototype. Images: `025-start-list-fails.png`, `026-start-list-after-retry.png`.
- Long list: it shows 50 and a line such as "50 of 128 cases shown" with **Show more cases**. Images: `027-start-list-paging.png`, `028-start-list-show-more.png`.
- A case being archived: `030-start-case-archiving.png`.
- A note about older cases left in the user folder (from before the PC was set up for everyone): `029-start-older-cases-note.png`.
- A case whose working copy is missing: `134-start-missing-working-copy-row.png`.

### 2.2 Erasing a case from the list

1. On a row marked "The working copy is missing", press **Erase**.
2. The row asks: "This removes the working copy, notes, suspects and checklist from this computer. Type movedcop to confirm." (The code in the sentence is the case's code.)
3. Type the code, press **Erase this case**, or **Cancel**.
4. The list then keeps one line: "Case ERASEDTW was erased on 14 Oct 2026 09:00. Only this line is left." with **Clear**.

Images: `019-start-erase-missing-copy.png`, `020-start-erase-typed-code.png`, `021-start-erased-line-after-erase.png`, `022-start-erased-lines-cleared.png`.

### 2.3 Archived cases

Press **Archived cases (2)**. The card shows the archived cases, each with **Open**, and **Back to the working list**. Image: `023-start-archived-list.png`. (Paging of the archived list is not reproduced in the prototype.)

---

## 3. Entering a case by code, and restoring from a case file

### 3.1 Enter a case by code

**Purpose:** open a case when you know its code.

1. On the middle card, type the code in the code field. Spaces, dashes and capital letters are ignored.
2. Press **Open case**. The case home opens. If the case has its own key you land on the case key card instead (section 5.3).
3. The **Open case** button stays off while the field is empty.

What can go wrong:
- Not a code: "That code does not match a case on this computer."
- The 8-character short code shown in lists is refused. The card asks for the full code. This is how the real app behaves too (open question in `FLOW-COVERAGE.md`). In the prototype, the Prototype panel has a button that shows the full code (a test aid).

Images: `031-enter-code-invalid.png`, `032-enter-code-short-refused.png`, `033-enter-code-full-opens-case.png`, `034-enter-code-keyed-case-key-card.png`.

### 3.2 Restore from a case file

**Purpose:** bring back a case you saved earlier as a `.unravis-case` file. Restoring always makes a **new** case.

1. Press **Restore from a case file**. Two fields appear: the file path and the master key.
2. Type the path (a drive path such as `C:\Cases\orders-slowdown.unravis-case`) and the key.
3. Press **Restore as a new case**. The new case opens on its case home with a new code.

What can go wrong, each with one sentence:
- Empty path: "Type the path of the case file."
- Wrong ending: the file name must end in `.unravis-case`.
- A network path (`\\server\share\...`): "Network paths are not accepted."
- Wrong key: the single failure sentence "...could not be opened with that master key." (Tries are throttled like the unlock screen.)
- On a fresh PC with no master key: restoring needs a key first; a set-up link is offered.

A prototype scenario that opens the card ready-made: `041-restore-scenario-card.png`.

Test values in the prototype: path `C:\Cases\orders-slowdown.unravis-case`, key `correct horse battery staple`. The situation "Restore from a case file (open the card)" opens the card for you.

Images: `035-restore-card-open.png`, `036-restore-empty-path.png`, `037-restore-wrong-extension.png`, `038-restore-network-path.png`, `039-restore-wrong-key.png`, `040-restore-success-new-case.png`, `042-restore-needs-master-key.png`.

---

## 4. Creating a new case (the guide)

**Purpose:** make a case in three steps: Name, Sources, Review. The strip at the top reads "1 Name  2 Sources  3 Review". A link, **← Leave the guide**, is always available. A line says "Nothing is saved until the last step." Back and Next move between steps and every entry is kept.

### 4.1 Step 1: Name

1. Press **Create a new case**. You see "Step 1 of 3: Name", "Name your case".
2. Type a **case name**. Rules: 2 to 80 characters. One letter shows "at least 2 characters". Over 80 shows "at most 80 characters" at once.
3. Optionally type **environments**, separated by commas (for example "Production, Staging"). Rules: duplicates are left out ("Left out, as the name is already in the list: production"), up to 32 environments, up to 60 characters each, and the character `›` is not allowed. A line counts them ("2 environments").
4. Optionally tick the box to give the case **its own key** (the case key). It must be at least 12 characters and be typed twice. "too short" and "not the same" are shown while you type.
5. **Next** stays off until everything above is valid.

Images: `043-create-name-step-empty.png`, `044-create-name-too-short.png`, `045-create-name-too-long.png`, `046-create-environments-deduplicated.png`, `047-create-own-case-key-too-short.png`, `048-create-own-case-key-mismatch.png`, `049-create-name-step-valid.png`.

Leaving and resuming:
- **← Leave the guide** with something typed asks "Leave without creating a case? What you typed here will be thrown away." Choose **Keep working** or **Leave and discard**. With nothing typed it leaves without asking. Image: `089-create-leave-guide-asks.png`.
- If you leave without discarding and come back, the guide says "Your earlier answers are still here. Nothing has been saved yet." with **Start over**. Image: `095-create-resume-draft-banner.png`.

### 4.2 Step 2: Sources

**Purpose:** choose the logs and data the case looks at. You can skip this step and add data after the case is made.

The step is headed "Choose what to look at: Choose the logs and data to look at." There are four **methods** across the top, each with a one-line hint:

1. **This computer**: "Auto-detect logs and databases here".
2. **Import files**: "Drag and drop, or give a file location".
3. **MongoDB**: "The API audit database".
4. **SQL Server**: "By address, with a login".

On the right is the **Sources in this case** panel (section 4.3). Image: `050-create-sources-this-computer-start.png`.

#### 4.2.1 Method 1: This computer

Use this when Unravis runs on the server itself. It looks at this computer only, reads file names and sizes, and opens nothing until the case is made. For another server use **Import files**.

1. Press **Scan this computer**.
2. You see "Found 8 sources and 1 place that could not be used." with "Host name: PAYROLL-WEB-01". Results are grouped under **Web server (IIS)**, **Windows**, **Databases**, and **Places that could not be used**.
3. Each found source shows its state: **Ready**, **Needs an export** (it cannot be read directly; export it first and use Import files), or it sits under "Places that could not be used" (cannot be read). Ready sources show size and age ("Large 184 MB, newest 12 minutes ago") and a line "What will be filled in automatically:" listing the fields the importer takes (for IIS: request time in UTC, method, URL path, status and sub-status, Windows error code, time taken, bytes, site and server name; client IP, referer and a cookie hash are kept but the export hides them by default).
4. Press **Add** on a ready source (for example "Add IIS web log · Default Web Site (W3SVC1)"), or **Add all N ready sources**. Added sources appear in the right-hand panel; their button becomes **Remove**.
5. For a place that was not found, press **Where is it?**. It explains what Unravis needs for that kind of log: how to switch the log on (for IIS: choose W3C format and tick at least date, time, cs-method, cs-uri-stem, sc-status, sc-substatus, sc-win32-status, time-taken), where the default folders are, that the HTTP.sys error log needs administrator rights and writes server local time, and that the Windows account that runs Unravis needs read access.
6. **Scan again** repeats the scan.

What can go wrong: a source that cannot be read or needs an export has no Add button. The screen says why.

Images: `051-create-sources-scan-results.png`, `052-create-sources-where-is-it.png`, `053-create-sources-one-found-added.png`, `054-create-sources-add-all-ready.png`.

#### 4.2.2 Method 2: Import files

**Purpose:** add files by folder, by one file, or by drag and drop. Nothing is read until the case is made.

1. Choose the **Import files** method. You see a field **Folder or file location** with **Look here**, and a drop zone "Drag and drop files here or Choose files…".
2. **By folder:** type a folder path (for example `C:\inetpub\logs\LogFiles\W3SVC1`), press **Look here**. You see "Found 6 files in this folder. Unravis suggested what each one is." A table lists each file: **File**, **Size**, **What it is** (a drop-down with the suggested type and an option "Don't use this file"), **Time zone of its times**.
3. **By one file:** type the full path of one file. The list then holds just "The file".
4. **By drag and drop (or Choose files…):** drop files on the drop zone. Each gets a suggested type ("Looks like: HTTP.sys error log (HTTPERR)").
5. Read the suggestions. You may switch a suggestion off ("Don't use this file") or give an unrecognised file a type. Types offered: IIS web log (W3C), HTTP.sys error log (HTTPERR), IIS failed request trace, SQL Server extended events, SQL Server error log, SQL Server Query Store export, Windows event log.
6. **Time zone:** a type that writes UTC (for example the IIS web log) shows "UTC (as this type writes it)". A type that writes server local time (HTTPERR) shows "Choose the time zone…" with options such as UTC, "Server local time, UTC+04:00", "Server local time, UTC−05:00". You must choose before adding.
7. Press **Add N files** (it counts the files that will be added). The added files appear in the right panel and a line reports what was left out ("2 files left out").

What can go wrong:
- A file Unravis does not recognise: "Not recognised, so it is left out." You can give it a type.
- A compressed file: "Compressed files are refused. Extract them first."
- A relative path (such as `logs\x`): "Use the full path..." with an example.
- A file type that writes server local time needs you to choose its time zone before you add it.

Images: `074-create-files-method-empty.png`, `078-create-files-by-folder.png`, `079-create-files-folder-adjusted.png`, `080-create-files-folder-added.png`, `081-create-files-by-one-file.png`, `082-create-files-relative-path-refused.png`, `075-create-files-drag-drop-file.png`, `076-create-files-timezone-chosen.png`, `077-create-files-added.png`.

#### 4.2.3 Method 3: MongoDB

**Purpose:** add the API audit database (and any application database) from a MongoDB server. One connection can serve several databases. Unravis only reads.

1. Choose **MongoDB**. Fields: **Use a saved connection** (a drop-down; "Type a new connection" is the first choice), **Server address**, **User name**, **Password**, **Login database**, **Encryption (TLS) and server trust** (On: verify the certificate; On: skip verification (advanced); Off), and a tick box **Remember the password on this computer**.
2. **Saved connection:** choose "Payroll audit Mongo (password saved)". The address fills in (`mongodb://audit-mongo:27017`).
3. **Address:** you can leave out `mongodb://` and the port. Typing `audit-mongo` shows "mongodb://audit-mongo:27017" and a note that the default port 27017 is used. A bad port such as `host:99999` says "port must be a number from 1 to 65535".
4. Press **Test and list databases**. Only after a successful test a checklist appears: "databases found", each with a **history chip** (for example "Profiler off" or "Profiler on"). System databases such as admin and local are left out.
5. Tick the databases. A database with "audit" in its name is set to **audit data** by default; others to **application data**. A database can be both: tick both boxes.
6. Press **Add N databases**. They are added as sources in the right panel, grouped under one connection.
7. Below the form, **Before you add MongoDB: the user needs** shows the permission script: `clusterMonitor` on admin (server health, given once) and `read` on each database; reading the profiler needs a small extra role. Press **Copy script**. Unravis shows the script and never runs it.

What can go wrong: nothing can be added until a database is ticked; a failed test shows the failure in words and no checklist.

Images: `055-create-sources-mongo-empty.png`, `056-create-mongo-saved-connection.png`, `057-create-mongo-address-no-port.png`, `058-create-mongo-bad-port.png`, `059-create-mongo-databases-listed.png`, `060-create-mongo-databases-ticked.png`, `061-create-mongo-added.png`, `062-create-mongo-permission-script.png`.

#### 4.2.4 Method 4: SQL Server

**Purpose:** add one or more SQL Server databases from one connection.

1. Choose **SQL Server**. Fields: **Use a saved connection**; **Server name**; **Where it runs** ("SQL Server or Managed Instance (one connection, several databases)" or "Azure SQL Database (one connection per database)"); **Authentication** ("SQL Server Authentication" or "Windows Authentication"); **Encrypt** (Mandatory, Optional, Strict); tick boxes **Trust server certificate** and **Remember password**; **User name**; **Password**.
2. **Server name:** with no port, the usual port 1433 is used and a note says so.
3. **Where it runs:** choosing Azure SQL Database changes the wording to one connection per database.
4. **Authentication:** Windows Authentication hides the user and password.
5. **Saved connection:** choose "PROD-SQL-01 (orders) (password saved)" and the form fills in.
6. Press **Test and list databases**. A wrong login is refused in words: "Login failed for user unravis_reader".
7. After a good test a checklist of databases appears, each with a **Query Store chip**: "Query Store on · 60 min steps", "Query Store off". System databases (master, model, msdb, tempdb) are hidden.
8. Tick the databases (for example `orders` and `PayrollApp`). Nothing can be added until one is ticked.
9. Press **Add 2 databases and keep details** (or **Add databases**). They are added together, grouped as "SQL Server · db1 · One connection · 2 databases" in the right panel, each with its own environment.
10. The page shows a **permission script** per server and per database, with a version switch ("SQL Server 2022 or later" / "SQL Server 2019 or earlier") and **Copy script**: it grants `VIEW SERVER PERFORMANCE STATE` and `VIEW DATABASE PERFORMANCE STATE`, creates the user in each database (`USE [orders]; CREATE USER ...`), and notes that Query Store must be on for each database. Unravis shows it and never runs it.

Images: `063-create-sql-form-empty.png`, `064-create-sql-no-port.png`, `065-create-sql-azure-flavor.png`, `066-create-sql-windows-auth.png`, `067-create-sql-encrypt-trust-remember.png`, `068-create-sql-saved-connection.png`, `069-create-sql-login-failed.png`, `070-create-sql-databases-listed.png`, `071-create-sql-databases-ticked.png`, `072-create-sql-added-grouped.png`, `073-create-sql-permission-script.png`.

### 4.3 The chosen-sources panel ("Sources in this case")

**Purpose:** see and tidy what you have chosen. It is always visible on the right of the Sources step.

1. By default it is a short **collapsed summary**: each source with its type and its environment, plus **Edit** and **Remove**. A line says "Press Edit to name an environment, portal and API. All of them are optional."
2. **Edit all** opens every source at once; **Collapse all** returns to the summary.
3. **Environment pills:** the environment is free text. Type a name and press Enter to make a pill; add several per source. Suggestions from earlier names appear ("Suggestions:").
4. **Portal** and **API** fields name what the source belongs to (optional).
5. **Remove** takes a source out.
6. Sources from one connection are grouped, and each database has its own environment.

Images: `083-create-chosen-sources-summary.png`, `084-create-chosen-sources-edit-all.png`, `085-create-chosen-sources-env-pills.png`, `086-create-chosen-sources-collapsed-again.png`.

(The prototype does not run the real "scan is stale" rule.)

### 4.4 Step 3: Review

1. Press **Next** on the Sources step. You see "Step 3 of 3: Review" and "Check what will be made".
2. The review lists the case name, environments, whether it has its own key, each source (and what each file source will read), how far back the data goes, the admin notice, and a note if a name is a duplicate.
3. With no sources the button is **Create empty case**. Otherwise it is **Create case**. A blocking problem disables it and gives the reason.
4. If a chosen source needs administrator rights to read (for example the HTTP.sys error log), the page shows "Notice: some chosen sources need administrator rights to read. Unravis will ask Windows for approval when it reads them, and says what it will and will not change." You must **tick the box to confirm the notice**. Until you do, the page says "Tick the box above to confirm the notice." and **Create case** stays off.
5. The page also shows "How far back to read the logs of this computer": **1 day**, **7 days**, **30 days** ("Each log is cut to this period before it is kept. You can add more later."), and "A shared source has no portal named, so it serves all of them." where it applies.

Images: `087-create-review-step.png`, `088-create-review-admin-notice-ticked.png`, `096-create-review-empty-case.png`, `097-create-review-one-source-will-fail.png`.

### 4.5 Creation progress, a failed source, and retry

1. Tick the notice box if it is shown, then press **Create case**. You see "Creating your case" with four rows, each **Done**, **Working**, **Waiting** or **Failed**: Make the case; Save the environment names; Save the source names; Read the sources.
2. If every source reads, the case opens.
3. If one cannot be read (in the prototype, the Windows event log source): "1 source could not be read: Windows event log · Application. The case was kept. Open it and use Add more data to try the failed sources again." Press **Open the case anyway**.
4. The case home opens with a **Needs another try** list (section 5.4). **Try again** fails the first time ("It did not go through again.") and works the second time (simulated).

Images: `090-create-creating-progress.png`, `091-create-failed-source-open-anyway.png`, `092-create-opened-with-retry-list.png`, `093-create-retry-fails-once.png`, `094-create-retry-succeeds.png`.

Not reproduced: the "Continue" choice if the app is locked in the middle of creating.

---

## 5. Opening an existing case, and every state it can be in

### 5.1 Open from the list

1. On the start screen press **Open** on a row.
2. The case home opens (section 6). The button **All cases: back to the start screen** returns.

Image: `098-open-case-from-list.png`.

### 5.2 Closed case

"This case is closed. You can read it, but nothing can be added." The Investigation Timeline button and every add button are off, with the reason. Press **Reopen this case** to open it again. A closed case is erased 30 days after it was closed (this is said in the Import files drawer, next to **Close case**).

Images: `121-case-closed.png`, `122-case-reopened.png`, `153-import-close-case-button.png`.

### 5.3 Case with its own key

1. Opening it shows "This case has its own key. The PC key is not used for this case." One field, **Case key** (at least 12 characters), and **Open case** or **Cancel**.
2. Wrong key: "That key did not open this case. Nothing was opened or changed."
3. Three wrong tries: "Too many tries. Try again in 5 seconds."
4. The right key opens the case. (Test aid: `vendor-case-key-01`.)
5. A warning reads: "Case keys cannot be recovered. If it is lost, only erasing the case frees the space." The card also offers **Erase this case**.

Images: `123-case-key-card.png`, `124-case-key-wrong.png`, `125-case-key-throttled.png`, `126-case-key-right.png`.

### 5.4 Needs another try

A failed import and an unsaved source name sit on a **Needs another try** list: "These did not go through. The case is kept as it is." Each row has **Add the file again** (opens the Import drawer) or **Try again**, and **Remove from list**.

Images: `119-case-needs-another-try.png`, `120-case-retry-add-file-again.png`.

### 5.5 Archived case, and restoring

An archived case shows "This case is archived. It is read-only here. To read the analysis, check the sources or add files, restore the case first." with the case file's name and folder. There is no analysis, timeline, files or checks. Three choices:

1. **Restore:** a drawer "Restore this case" says the case goes back to open, exactly as archived. Press **Restore this case**; the page says "The case is open again, as it was archived."
2. **Restore as a new case:** a drawer says it makes a second case from the case file and the two have different codes. Press **Restore as a new case**; a line reports "Restored as a new case ... with code ...". This case is left as it was.
3. **Free the working copy:** a drawer says freeing gives back disk space, the case file becomes the only copy, and you can still restore from it. After it, the page says "The working copy was freed. The case file is the only copy."

Images: `127-case-archived.png`, `138-archive-restore-drawer.png`, `139-archive-restored-case-home.png`, `140-archive-restore-as-new-drawer.png`, `141-archive-restore-as-new-done.png`, `142-archive-free-working-copy-drawer.png`, `143-archive-free-working-copy-done.png`.

### 5.6 Legacy case

"This case cannot be opened. It was made by an older version that did not seal cases, so Unravis cannot tell whether it was changed. Nothing was read and nothing was changed. If you have a case file you made yourself, restore that instead." Buttons: **Restore from a case file** (goes to the start screen with the restore card open) and **Erase this case**.

Images: `128-case-legacy.png`, `129-case-legacy-restore-intent.png`.

### 5.7 Missing working copy

On the start list a row says "The working copy is missing" with **Erase**. The prototype does not make a separate case-home screen for it; erasing from the list is the flow (section 2.2). Image: `134-start-missing-working-copy-row.png`.

### 5.8 Loading, could not open, gone, changed in another tab

- Loading: "Opening the case…". Image: `130-case-loading.png`.
- Could not open: "The case could not be opened. Try again." with **Try again**. Image: `131-case-load-failed.png`.
- Gone: "This case could not be found. It may have been erased." Image: `132-case-gone.png`.
- Changed in another tab: "This case changed in another tab." and **Reload**. Until you reload, moves that change the case are off. Image: `133-case-changed-in-another-tab.png`.

These four states are forced by prepared situations; the prototype has no real race.

---

## 6. The case home, block by block

**Purpose:** everything about one case in one page. The header shows "Your case · PAYROLLR", the title, "Everything about this case is here. See what happened, your theories, the health checks, the analysis and the files.", the start time and three counts (Files, Theories, Health checks), a link **All cases: back to the start screen**, and **Archive this case**. The top bar has **Home**, **Ledger** (with a count), **Import**, **Blackbox**, **Glossary**, **Lock cases**, **UTC / Local(UTC)** and the theme button.

Whole page: `099-case-home-full.png`. Header: `100-case-home-header.png`.

| Block | What it does | Image |
|---|---|---|
| **Analysis** | **Open the analysis** opens the charts and the ranked list of operations (section 10). A second line explains Nearby activity. | `101-case-block-analysis.png` |
| **Investigation Timeline** | **Open the Investigation Timeline** (section 12). It replaced the old "Guided" block. Off for a closed case or a case with no sources. | `102-case-block-timeline.png` |
| **Databases** | Database connections grouped by connection, each database with its environment and history chip (Query Store or profiler). New in the prototype. | `103-case-block-databases.png` |
| **Story so far** | One list, oldest first, of what the case has saved (case started, theories, health checks, imports). | `104-case-block-story.png` |
| **Theories** | What you think may have happened. Each can be **Open**ed. **Open the theories** (or **Add a theory**) opens the ledger drawer. | `105-case-block-theories.png` |
| **Health checks** | Read-only looks at the SQL Server or MongoDB your logs depend on. **Run checks** (nothing runs by itself); **Earlier runs** with **Show**. | `106-case-block-health.png`, `107-case-health-checks-run.png` |
| **Saved connections** | One list of every saved connection (SQL Server and MongoDB) with what its last test found and whether a password is saved. **+ SQL Server**, **+ MongoDB**, **Refresh**, and **Test**, **Edit**, **Forget** on each. | `108-case-block-saved-connections.png` |
| **Files** | What is in the case, and **Add a file** (section 7). | `111-case-block-files.png` |
| **Case key** | "Off by default." **Add a case key** (section 6.3). | `112-case-block-case-key.png` |
| **Erase this case** | Section 6.4. | `113-case-block-erase.png` |

### 6.1 Theories (the ledger drawer)

1. Press **Open the theories**. A drawer "Theories" opens.
2. Type your idea in the box labelled "A theory" and press **Add a theory**. It joins the list with the date.
3. Each theory has the states **Open**, **Testing**, **Supported**, **Ruled out**, and a **Remove** button. (The state names are a prototype guess.)
4. **Close** returns to the case.

Images: `114-theories-ledger-drawer.png`, `115-theories-after-add.png`.

### 6.2 Saved connections

- **Test** checks the connection and says what it found; **Edit** (not expanded in the prototype); **Forget** asks "Forget this connection?" before removing it. Image: `109-case-saved-connection-test.png`, `110-case-saved-connection-forget-asks.png`.
- **+ SQL Server** and **+ MongoDB** open a drawer "SQL Server connection": "Fill this in the way you do in SQL Server Management Studio. Press Test and list databases..." Image: `116-connections-drawer.png`.

### 6.3 Case key: add, change, remove

1. Press **Add a case key**. You see "Case key (optional): Sealed with: PC key. A case key is separate from your Windows sign-in and from the PC key."
2. Type **New case key** (at least 12 characters) and repeat it. "If you lose this key, the case and any archive made from it cannot be opened. There is no reset."
3. Press **Save the case key**. Afterwards the block offers change and remove.

Image: `117-case-key-add-page.png`.

### 6.4 Erase this case

1. Press **Erase this case**. You see "This removes the working copy, notes, suspects and checklist from this computer. An import or recording for this case stops. A case file you saved is not touched. This cannot be undone. Type PAYROLLR to confirm."
2. Type the code and press **Erase this case**, or **Cancel**, or **Back to the list**. After erasing, the start screen keeps one "was erased" line.

Image: `118-erase-case-page.png`.

### 6.5 Archiving a case

**Purpose:** save the whole case in one file you can keep or move. The case need not be closed first.

1. Press **Archive this case**. A drawer opens: "Save the whole case in one file you can keep, copy or move."
2. **Folder for the case file:** a drive path you can write to. Not a network drive, not a relative path, not inside Unravis's own data folder. The drawer shows the case size ("6.4 MB").
3. **Sealed with:** choose "The master key of this computer" or "The passphrase you type below" and type the passphrase.
4. Press **Archive the case**, or **Cancel**.
5. The case page shows "An archive of this case is running. 70%" with **Show progress**. When it ends the case is archived (section 5.5).

Not reproduced: the "verify the file" drawer.

Images: `135-archive-drawer.png`, `136-archive-progress.png`, `137-archive-result-case-home.png`.

---

## 7. Adding a file to an existing case (the Import drawer)

**Purpose:** bring a log or export into a case that already exists.

1. On the case home, in **Files** press **Add a file** (or **Import** in the top bar). The drawer "Import files" opens: "Bring in files exported from your servers. They are read on this computer only and kept in an encrypted case."
2. Under **Choose a file** pick a file from the list. (The prototype uses a fake list instead of a file picker: `u_ex261020.log`, `u_ex261020_siteB.log`, `orders_xe_2026-10-20.xel.csv`, `sql_errorlog_2026-10-20.txt`, `u_ex261020_copy.log`, `empty.log`, `huge_export_2026.log`, `logs-october.zip`.) "Text and CSV exports only. Compressed files are refused, so extract them first."
3. Unravis says what it looks like ("Looks like: IIS web log.") and shows a **Host** drop-down ("The file names its own hosts", or web-a, web-b, sql-01). "Pick the host this file came from. A host that is not in the host list is refused."
4. Press **Preview**. You see "Nothing is saved until you press Import file." and: Rows read, Rows kept, Rows rejected, Time covered, **Time zone rule**, Known gaps. "Imported rows are symptoms or correlations. They are never proof of a cause."
5. Press **Import file**. The file appears in the table "Imports in this case" (File, Type, Host, Rows, Rejected, Status).

What can go wrong:
- No time-zone rule: "Not set. Times are not checked, so the timeline cannot line this file up with other sources." The Investigation Timeline then refuses to draw a multi-source chart (section 12.5).
- Duplicate: "Blocked: this file has the same content as u_ex261020.log, which is already in the case. Nothing was added."
- Empty file: "This file is empty."
- Too big: "This file is 5 GB. The limit for this file type is 1.9 GB."
- Compressed: "Compressed files are refused, so extract them first."
- **Close case** (also in this drawer) closes the case. Section 5.2.

Images: `144-import-drawer-open.png`, `145-import-file-chosen.png`, `146-import-preview-no-timezone-rule.png`, `147-import-file-imported.png`, `148-import-sql-error-log-preview.png`, `149-import-duplicate-file.png`, `150-import-empty-file.png`, `151-import-too-big-file.png`, `152-import-compressed-file.png`, `153-import-close-case-button.png`, `154-header-import-and-ledger.png`.

---

## 8. Other drawers from the top bar

- **Patterns:** three canned entries ("Slow after a deploy", "Errors with the slowdown", ...), each with **Use this window**. "A pattern is a place to look, not a cause." Image: `175-drawer-patterns.png`.
- **Ledger:** the theories list. Image: `176-drawer-ledger.png`.
- **Glossary:** plain definitions (P95, Reference period, Bucket, ...). Image: `177-drawer-glossary.png`.
- **Blackbox:** a small recorder that runs on a server and keeps a few readings. It is a stub in the prototype. Image: `178-drawer-blackbox.png`.
- **UTC / Local(UTC)** switches the displayed zone; **Change theme** switches light and dark.

---

## 9. Quick map: where each flow is

| You want to... | Go to section |
|---|---|
| Unlock, set or reset the master key | 1 |
| Understand the start screen | 2 |
| Open a case by code or restore from a file | 3 |
| Make a new case and add sources | 4 |
| Open an existing case in any state | 5 |
| Use every block of the case home | 6 |
| Add a file to a case | 7 |
| Run the analysis, read evidence, records | 10 |
| Export | 11 |
| Use the Investigation Timeline | 12 |
| See a story end to end | 13 |
| Find a picture | 14 |

## 10. Running the analysis and reading its screens

### 10.1 Open the analysis

1. On the case home press **Open the analysis**. The analysis opens on the last hour against the equal period right before it.
2. The **scope bar** at the top has: **Range** chips (**Last 15 min, Last hour, Last 4 hours, Last 24 hours, Last 7 days**), **Type** (**All, Sync, Async, Hangfire, HTTP**), **More filters ▾**, and **Run analysis**. Pressing a chip or a type re-runs the analysis at once. Editing the windows in More filters needs **Run analysis**; until then a note says "The figures below are from the last run. Press Run analysis to apply your changes."
3. **More filters** shows tabs (When, What, Details, Ranges, Chart; the Details and Ranges groups are stubs in the prototype). **When** has **Investigate** From/To and **Reference** From/To, with the time zone and a UTC echo line.

Images: `155-analysis-open-from-case-home.png`, `156-analysis-scope-bar.png`, `157-analysis-more-filters-when.png`, `158-analysis-quick-range-4-hours.png`, `160-analysis-kind-filter-async.png`.

### 10.2 The pulse (first screen)

Five figures with change against the comparison period: **Requests**, **Completion**, **Observed P95**, **4xx**, **5xx**, each with a **Where is it? ›** link; a headline; and the chart (bars = requests, line = P95, dashed line = comparison period). Changes against the comparison period are **withheld** unless the comparison period has at least 30 events and is at least 80% covered; then the screen says why.

Image: `159-analysis-slowdown-pulse.png` (the slowdown against a good earlier hour: P95 about 2.4 times higher, 5xx up).

### 10.3 Window rules and what can go wrong

- Comparison period overlaps the investigated period: "The reference window must end at or before the current window starts." **Run analysis** is off. Image: `161-analysis-reference-overlap-blocked.png`.
- A window over 7 days: "Each window can be at most 7 days." Image: `162-analysis-window-over-7-days.png`. (The timeline allows 30 days. This difference is deliberate.)
- No data in the comparison period: "No audit data was recorded in the comparison period, so nothing is compared." No deltas, no ratios. Image: `163-analysis-reference-no-data.png`.
- Comparison period only partly covered: "The comparison period is only 50% covered by data (the source was not recording for the rest), so differences may be wrong." Image: `164-analysis-reference-half-covered.png`.
- No data in the investigated period: "No requests in this period." Image: `165-analysis-no-data-in-period.png`.
- A source failed: a banner "Some sources failed: Payroll API audit (MongoDB): The read timed out after 60 s. Figures use the other sources." Image: `166-analysis-source-failed-banner.png`. The sources button opens the source list: `167-analysis-sources-popover.png`.

### 10.4 Where is it?

1. Press **Where is it?** (or the link on a figure). You see "Where is it?" with lenses: **All operations, Compare to earlier, Slowest (P95), Most requests, Most 5xx, Biggest change**, and a ranked table (Operation, Requests, P95, Change vs comparison period, 5xx).
2. Press a lens to re-rank. Press a row (for example "Payroll.Run") to go to the evidence.

Images: `169-where-is-it-view.png`, `170-where-is-it-biggest-change.png`.

### 10.5 Evidence

1. After pressing a row you see "Evidence · Payroll.Run" with tabs **Slowest requests**, **Request history**, **Failures**, and **Export CSV** (simulated), plus **Back to all of this operation**.
2. Each row has **Inspect record** and **Nearby**.

Image: `171-evidence-rows-for-operation.png`.

### 10.6 Record detail

**Inspect record** opens "Record 0e2e07ab45805f": "Every field stored for this audit record." with the **Stored fields** and a **Derived by Unravis** section. **← Back to the evidence** returns. Image: `172-record-detail.png`.

### 10.7 Nearby activity

**Nearby** shows one call beside what SQL, Mongo and IIS did around it. The screen says plainly: "Call trace for one request is not wired" (the prototype does not invent a link). Image: `173-nearby-activity.png`.

### 10.8 Breadcrumb and Back

The breadcrumb ("Pulse › Where is it? › Evidence › Record") and **Back** move up one level. Image: `174-analysis-breadcrumb-back.png`.

### 10.9 Analysis without a case

The footer link **Go to the analysis** on the start screen opens the analysis of the demo audit source with no case behind it: no **Case home** button and no timeline link. Image: `168-analysis-without-a-case.png`.

---

## 11. Export

**Purpose:** save what the analysis shows as a file. It leaves this computer only when you copy it.

1. In the top bar press **Export**. A drawer opens with: Sources ("Audit source + 2 stored imports"), From, To, Time zone, and Options (for example "Replace user ids with pseudonyms"). **Scrub is on by default**: client IP, referer and cookie hash are left out.
2. Press **Preview**. A summary of what will be written and what is scrubbed appears, and **Save the export** becomes available.
3. Press **Save the export**. The drawer says "Saved."

What can go wrong:
- **409 changed:** if the case moved after the preview (for example a re-import), **Save the export** gives "409" and is off until you press **Preview again**. The same happens if you switch the scrub option after the preview, because the scrub choice is part of the preview stamp.

Images: `179-export-drawer-ready.png`, `180-export-preview-scrub-on.png`, `181-export-saved.png`, `182-export-changed-409.png`, `183-export-preview-again.png`, `184-export-scrub-switched-409.png`.

---

## 12. The Investigation Timeline (new)

**Purpose:** see the API audit log, the web server (IIS) and the database (SQL Server) side by side on one time axis for an investigation window, next to a comparison period, and see which suspects the data supports. The old working name was "tracing"; some file names and addresses still say that. Screens for this section were captured at 1920 by 1080.

### 12.1 Opening it

1. On the case home press **Open the Investigation Timeline** (it is off for a closed case or a case with no sources).
2. **The timeline opens by itself.** There is no need to press anything: the default window (last hour against the hour before) is drawn, and the **Suspects** rail starts checking ("Checking 25 suspects… 6 of 16"), one suspect about every 140 ms.
3. The page shows, top to bottom: title and **← Case home**; the controls card; a summary line; a note about mixed times; the overall figures; the trace board; a reading aid "What Unravis sees"; a bucket panel; the folded evidence card; three folded details sections; and **Open the analysis** and **Export**. The Suspects rail sits at the right.

Images: `185-timeline-opens-by-itself-loading.png`, `186-timeline-default-last-hour-full.png`.

### 12.2 Choosing the period: quick ranges and the two pickers

1. **Quick range** chips: **Last 15 min, Last hour, Last 4 hours, Last 24 hours, Last 7 days, Last 30 days**. A chip sets the investigation window to that period ending "now" and the comparison period to the equal-length period right before it. The chip that matches is highlighted.
2. **Investigation window** ("When did the problem happen? Up to 30 days; the default is the last hour.") shows a button with the dates, the zone and the length (for example "20 Oct 2026 11:00 → 12:00 UTC · 1 h"), and the UTC form beneath it. Press it to open the **date-time range picker**: quick ranges (the six chips plus **The slowdown in the demo data**), a calendar (click a start day, then an end day), start and end times in the chosen zone, **Apply** and **Cancel**.
3. **Compare it with (comparison period)** ("This earlier period is only used for comparison. It may not have been healthy.") has its own picker with the quick ranges **The equal period right before the investigation window** and **Same time yesterday**.
4. A click outside the picker closes it without changing anything.
5. After you change a window by hand the timeline does not re-run by itself. Press **Refresh timeline** (see 12.4).
6. A note says times show in the zone chosen by the **UTC / Local** switch, while the timeline always reads and stores UTC.

Images: `187-timeline-controls-card.png`, `188-timeline-quick-range-4-hours.png`, `189-timeline-picker-investigation-window.png`, `190-timeline-picker-applied.png`, `191-timeline-picker-comparison-period.png`, `192-timeline-picker-closed-by-outside-click.png`.

### 12.3 Sources chips and Add evidence

Chips such as "API audit · Payroll API audit", "IIS · IIS web log · Default Web Site", "SQL · SQL Server extended events · orders" choose which lanes are drawn. Press one to switch it off, then **Refresh timeline**. **Add evidence** opens the Import drawer (section 7). Database connections added in the wizard are not chips; they feed the evidence card (12.10).

Image: `193-timeline-sources-chip-off.png`.

### 12.4 Refresh timeline

**Show timeline** becomes **Refresh timeline** once a timeline exists. Press it after changing the windows or sources. Image: `194-timeline-refresh-timeline.png`. It never imports, pulls from a server or changes the case.

### 12.5 Rules shown as notices

| Condition | What the screen does | Image |
|---|---|---|
| The two windows **overlap** | A non-blocking notice: "The investigation window and the comparison period overlap. The comparison period works best when it shares no time with the investigation window. This is only a notice: you can still show the timeline." with a button **Move it to just before the investigation window**. | `233-timeline-overlap-notice.png`, `234-timeline-overlap-fix.png` |
| A window is longer than **30 days** | A blocking message ("The longest period is 30 days"); **Show timeline** is off. | `235-timeline-30-day-limit-refused.png` |
| A window is missing its start or end, or ends before it starts | A blocking message; the button is off. | (same area; not captured separately) |
| The case is **closed** | **Show timeline** is off with the reason; nothing is drawn. | `239-timeline-closed-case.png` |
| A file has no checked time zone | If more than one source is chosen, the whole chart is switched off: "Time zone not checked, so nothing is drawn." | `240-timeline-timezone-gate.png` |

Note the difference with the analysis: the timeline warns about an overlap and allows 30 days; the analysis refuses an overlap and allows 7 days.

### 12.6 The summary line, figures and reading aid

1. **Summary line:** "Each bar = 5 minutes · 36 bars on the axis · window 2 h 30 min · 6 end slots outside the window". Longer windows use bigger bars (at most 120 bars). The axis always starts on a whole UTC hour.
2. **Mixed-times note** (cannot be dismissed): "Some data was collected at different times. API data was read just now, while SQL and web-server data came from saved logs. Compare the timing and trends, but do not expect totals from the different sources to match exactly."
3. **Seven overall figure cards** ("Overall figures: investigation window against the comparison period"): Requests to the application; Slow-request time (P95, estimated); Application server errors (5xx); Web server errors (IIS 5xx); Slow web requests (IIS P95, estimated); Slow database calls (over 1 s); Database errors and blocking. With the slowdown situation: 4,216 requests; P95 522 ms, 2.4 times the comparison period; 185 API 5xx (4.4%); 210 IIS 5xx (4.4% of 4,816); IIS P95 1,046 ms (4.0 times); 2,005 slow SQL calls (40% of 5,059); 130 errors and 15 blocking reports.
4. Where the comparison period is not covered (fewer than 30 events or less than 80% covered) a card says "Cannot compare with the earlier period" and gives the reason on hover instead of guessing. An estimated P95 is labelled "estimated".
5. **What Unravis sees:** three tiles (Application, Web server, Database) in plain words ("Slower than usual", "Returning errors and slower responses", "Many slow calls and blocking"), a "where the evidence points" line with a strength chip, and a next step with a button such as **Look at 10:50 →** that selects the busiest moment.

Images: `196-timeline-summary-line-and-mixed-note.png`, `197-timeline-figure-cards.png`, `198-timeline-what-unravis-sees.png`, `195-timeline-slowdown-full.png`.

### 12.7 The trace board

Three lanes on one shared time axis. Each lane has a left column (name, a question such as "Was the application slow or failing?", a state sticker, event count, comparison, legend) and panels:

- **API requests (audit log):** one panel. Pale green bars = requests per bucket (left axis); solid line = P95 time (right axis, estimated); dashed grey line = the comparison period's P95.
- **Database (SQL Server):** three panels: slow calls (over 1 s) and errors; how long calls took (P95); blocking and deadlocks.
- **Web server (IIS):** three panels: requests by result (OK, client error 4xx, server error 5xx); how long web requests took (P95, as logged); web connection errors (HTTP.sys).

Each panel names what it plots, with a titled y-axis and ticks. A shared x-axis states the bar length ("Time (UTC). Each bar covers 5 minutes...").

Lane stickers: **Events found**, **No logs**, **No events found** (logs cover the period and contain nothing: a flat zero, not hatched), **No logs cover this time**, **Log time zone needs checking**, **Some data could not be read**. Hatched bands mean "no log data for this time" with the exact range and length. A lane with logs for under 15% of the window folds to one short panel with **Show all panels**.

Images: `199-timeline-board-three-lanes.png`, `241-timeline-nothing-stored.png`, `242-timeline-half-stored.png`, `243-timeline-saved-lanes-only.png`, `237-timeline-no-comparison-sparse.png`, `238-timeline-sparse-lanes.png`.

### 12.8 Hover and select a bucket; hand-off to Run analysis and back

1. **Hover** over the chart: a tooltip lists the bucket's time range and one line per lane (for example "IIS: 118 requests (0 4xx, 6 5xx) · P95 time-taken 1100 ms · 1 HTTP.sys error"). Hovering inside a gap says "no log data for this time".
2. **Click** a bar (or use Left and Right arrow keys, Home, End) to select a bucket. The outline spans all lanes. The **bucket panel** shows the time ("2026-10-20T10:50:00Z → 2026-10-20T10:55:00Z"), "This bar is fully inside your investigation window.", what each lane saw ("API audit 141 · SQL 166 · IIS 174") and **Open Run analysis for this bucket**.
3. Press **Open Run analysis for this bucket** (or Enter). The analysis opens with a strip: "Opened from the Investigation Timeline. Incident 20 Oct 10:55 UTC → 20 Oct 11:00; reference (comparison period) 20 Oct 06:30 → 20 Oct 09:00." If the comparison period would not be valid in the analysis, Unravis moves it to just before the selected period and says so. **Open the analysis** is off for a window longer than 7 days, with the reason.
4. Press **← Back to the timeline**. Both windows and the selection are kept.

Images: `200-timeline-hover-bucket.png`, `201-timeline-select-bucket.png`, `202-timeline-bucket-panel.png`, `203-timeline-keyboard-arrow.png`, `204-timeline-handoff-to-analysis.png`, `205-timeline-handoff-back.png`.

### 12.9 The three layouts: A, B, C (prototype choice)

The layout pill in the bottom bar switches layouts; the prototype lets a reviewer pick one.

- **A Report:** one column, top to bottom. (default)
- **B Workbench:** a side panel for windows, range chips and sources; the right side shows figures, a taller chart, then the bucket panel and details side by side.
- **C Lane ledger:** a table with one row per lane (name, trust stickers: state, live or stored, consistency, complete, as-of time, comparison; event count; sparkline). Opening a lane row shows its full panels.

Images: `208-timeline-layout-a-report.png`, `209-timeline-layout-b-workbench.png`, `210-timeline-layout-c-lane-ledger.png`.

### 12.10 The seven-day view, gaps and the 30-day view

- **Last 7 days:** bars are 2 hours, 84 on the axis. The audit lane shows its daily rhythm and a thin hatched sliver for the 19 Oct 02:00 to 04:00 outage. SQL and IIS show "No logs for this time: 13 Oct 12:00 → 20 Oct 06:00 · 6 d 18 h" and only the last six hours drawn; each lane's left column says "No log data for ... of this window". Lines break at a gap instead of joining across it.
- **30 days starting at 00:01:** bars coarsen to 8 hours, 91 on the axis, two boundary raw scans. "How this axis was worked out" explains it.

Images: `229-timeline-seven-days-gaps.png`, `230-timeline-seven-days-board.png`, `231-timeline-seven-days-show-all-panels.png`, `232-timeline-seven-days-evidence-card.png`, `236-timeline-30-days-8-hour-bars.png`.

### 12.11 The folded evidence card ("Where does the evidence point?") and its scope switch

**Purpose:** one SQL Server or MongoDB connection can hold several environments. When something is slow, this card says where the evidence points: one environment, the whole server, or another database. It says how strong the evidence is and says plainly when there is not enough.

1. Below the chart there is a folded card. Folded, it shows only a title and a strength chip, for example "The problem appears isolated to Production · Strong evidence · Open to see the evidence behind it".
2. Open it. The control **View evidence for** has three choices: **This environment** (default), **All databases on this server**, **Server only**. A hint says it changes only what is shown below, not the conclusion.
3. In **All databases on this server** an unnamed **Other databases on this server** row shows the share of file I/O used by databases you did not pick. **Show their names** reveals them with a warning ("Hide them before you share the export"); **Hide their names** hides them again. **Server only** shows only the server-wide block ("instance-wide: these cannot be split by database") with a Source line.
4. The verdicts: "The problem appears isolated to X", "The whole database server appears affected", "Another database may be affecting yours", "There isn't enough evidence to narrow it down", "No clear performance problem was found in this period". Each has a strength chip (Strong evidence, Some evidence, Not enough evidence), what was seen, what it means and what to check next. The not-enough-evidence verdict adds "What you can still do" and "For the next incident" (switch on Query Store, start recording server counters).
5. Tables below: the server as a whole (disk latency, CPU, memory grants waiting, top wait), by database (P95 incident vs comparison, change, share of file I/O, numbers-from column, history chip), a MongoDB block (queued operations, profiler chip, limits), and "Rules used" (thresholds tagged "from vendor guidance" or "prototype default").
6. Five prepared situations (prototype bar): one environment, whole server, noisy neighbour, nothing kept, a quiet window.

Images: `217-evidence-card-folded.png`, `218-evidence-card-open-one-environment.png`, `219-evidence-scope-all-databases.png`, `220-evidence-scope-show-names.png`, `221-evidence-scope-server-only.png`, `222-evidence-verdict-whole-server.png`, `223-evidence-verdict-noisy-neighbour.png`, `224-evidence-verdict-not-enough-evidence.png`, `225-evidence-verdict-quiet-window.png`.

### 12.12 The Suspects rail (right side)

**Purpose:** list possible explanations and what the data says about each. "Suspects, not causes. Nothing here says why it happened." It re-uses the idea of the guided analysis engine; there is no AI. It loads by itself when the timeline appears.

1. Legend: **Red** most likely (its tier clearly moved first); **Orange** likely; **Green** ruled out by the data we have; **Grey** not sure (could not be tested, or does not apply; grey is never a pass).
2. A summary line when done: "Checked 25 suspects: 1 red, 1 orange, 5 grey, 18 ruled out (green)."
3. **Default view = the top five:** every red, then the best orange, then grey until five are shown. "Q1 (clock) and Q2 (audit coverage) are pinned above the five as a Data check while they are not green."
4. **See all (25)** expands in place: every suspect, red then orange then grey, green ones folded under "Ruled out by the data (n)". **Show fewer** returns.
5. Each card: a colour dot with the colour word, an id (for example D1) and title ("Blocking chain (head blocker or open transaction)"), a one-sentence reading, "Its tier (SQL) first moved at 20 Oct 09:11.", expandable evidence rows (Signal, Role Must or Expect, Reading yes, no or unknown, Why), the cheapest check, what would overturn it, and for grey a **Needs:** line.
6. **Show on chart** selects the bucket where that suspect's tier first moved.
7. Under 1180 px width the rail moves below the chart.

Demo outcomes: one environment: D1 blocking chain red. Whole server: D6 (memory), D9 (disk), D10 (SQL scheduler) red. Noisy neighbour: D9 red, M10 orange. No history: nothing red, mostly grey.

Images: `211-suspects-rail-top-five.png`, `212-suspects-rail-see-all.png`, `213-suspects-show-on-chart.png`, `214-suspects-ruled-out-folded.png`, `215-suspects-show-fewer.png`, `216-suspects-card-open-evidence.png`, `227-suspects-rail-no-history.png`, `226-timeline-no-history-full.png`, `228-timeline-one-environment-full.png`.

### 12.13 What-if switches (prototype bar only)

On the timeline the bottom bar shows **What if:** buttons. They are **not part of the product**; they let a viewer see how the screen reacts.

- Timeline conditions: **Audit read exceeds its budget** (the lane is flagged "Some data could not be read" and nothing is shortened), **Audit source unreachable**, **Boundary scan exceeds 50,000 rows**. Press **Refresh timeline** after switching.
- Suspect conditions that turn a suspect orange or red: **Payload capture slows (S8)**, **IIS rejects requests by filter (S9)**, **App pool goes offline (S10)**, **HTTP.sys receive timeouts (S11)**, **Slow clients drain responses (S12)**, **SQL commands time out (D16)**, **SQL login storm (D17)**, **MongoDB spills to disk (M11)**, **MongoDB rejects connections (M12)**.
- On the analysis, **A source failed (analysis)**.
- Four more appear only with the MVP-2 stage: **SQL source gets a late row**, **SQL rows restated upstream**, **Pull: source drops mid-read**, **Pull: save fails halfway**.

Images: `244-whatif-audit-over-budget.png`, `245-whatif-audit-unreachable.png`, `246-whatif-boundary-scan-over.png`, `247-whatif-suspect-d16-sql-timeouts.png`, `248-whatif-suspect-s10-app-pool-offline.png`, `249-whatif-suspect-m12-mongo-rejects.png`.

### 12.14 Auto-run notes

- The timeline runs by itself when you open it, because reading stored evidence is read-only and repeatable. It never imports, pulls from a server or changes the case.
- It stays off (the button still says **Show timeline** and is off) when a window was changed by hand and has not been refreshed, when a window is refused, or when the case is closed.
- A redraw never restarts the Suspects rail.

### 12.15 No-history situation

When the comparison period is before the SQL and IIS files start, or Query Store is off and there are no samples, the evidence card says "There isn't enough evidence to narrow it down", the rail shows nothing red and mostly grey with "Needs" lines, and server numbers are not invented. Images: `224-evidence-verdict-not-enough-evidence.png`, `226-timeline-no-history-full.png`, `227-suspects-rail-no-history.png`.

### 12.16 Details at the bottom

Four folded sections: **What each lane shows**, **How this axis was worked out** (start and end you chose, axis start and end in whole UTC hours, bucket size, boundary raw scans 0, 1 or 2), **Timeline details** (response flags per lane), **Words used on this screen**. Images: `206-timeline-details-sections.png`, `207-timeline-details-axis-open.png`.

### 12.17 MVP-2 (planned, behind the prototype stage switch)

With **Release stage: MVP-1 + MVP-2** (prototype panel) a coverage ledger appears and **Pull this window** saves a live SQL read into the case. Pulling the same window again says "Nothing new"; a changed one replaces the stored pull as a whole; a window that partly overlaps a stored pull is refused: "409 window-overlap; re-pull the exact stored window or import a disjoint window." This part is planned work, shown only for discussion.

Images: `250-mvp2-coverage-ledger-no-coverage.png`, `251-mvp2-first-pull-done.png`, `252-mvp2-overlap-409.png`.

### 12.18 Not in the prototype

Not shown (intentionally "Later"): route filter, saved timeline window, win32 series, deep link to the call trace, delta overlay. The old "Guided" analysis block is not shown anywhere. There is no real server, no real file picker and no real encryption.

### 12.19 Narrow screens

At phone width (390 px) the pages stack in one column, with no sideways scroll. Images: `256-narrow-timeline-phone-width.png`, `257-narrow-case-home-phone-width.png`.

---

## 13. A short worked story (for narration)

1. Launch. Start screen. Press **Open** on "Payroll.Run slowdown, 20 Oct".
2. Case home. Press **Open the Investigation Timeline**.
3. The timeline draws itself. Press the **Last 4 hours** chip, then open the **Investigation window** picker and choose **The slowdown in the demo data**.
4. Read the figures: the P95 is about 2.4 times the comparison period; 15 database blocking reports.
5. The Suspects rail shows D1 "Blocking chain" red, because the SQL lane moved first (09:11), 3 minutes before the API (09:14).
6. Press **Show on chart**, click the bar, press **Open Run analysis for this bucket**, press **Where is it?**, open "Payroll.Run", **Inspect record**. Then **← Back to the timeline**.
7. Open the folded card: "The problem appears isolated to Production · Strong evidence".

---

## 14. Image index

Each row: file, what it shows, how to reach it (clicks, or the prepared-situation id from the bottom bar's picker).

| File | What it shows | How to reach it |
|---|---|---|
| `001-launch-start-screen.png` | The start screen on a normal launch: three cards. | Open the file with the key unlocked (prototype panel > Launch the app as > Normal). |
| `002-key-locked-unlock-screen.png` | Master key locked: the unlock screen comes first, no case titles or counts. | Scenario s-locked (Launch conditions: Master key locked at launch). |
| `003-key-wrong-first-try.png` | A wrong master key: one sentence, nothing revealed. | Unlock screen: type any wrong text, press Unlock. |
| `004-key-wrong-throttled.png` | After 3 wrong tries the screen asks to wait a few seconds. | Press Unlock with a wrong key 3 times in a row. |
| `005-key-right-after-wait.png` | Waited, typed the right key: back on the start screen. | Wait about 6 seconds, type orange-sunrise-2026 (test aid), press Unlock. |
| `006-key-file-damaged.png` | The master key file is damaged: the only way forward is to start over. | Scenario s-damaged (Launch conditions: Master key file damaged). |
| `007-key-file-damaged-ticked.png` | The start-over choice, with the acknowledgement box ticked. | Tick the box on the damaged-key screen. |
| `008-key-file-set-aside.png` | After Start over: the old file is set aside (kept, not deleted) and the screen offers Set a new master key. | Press Start over with a new master key. |
| `009-key-set-new-key-button-does-nothing.png` | After pressing Set a new master key the prototype stays on this screen: its own move rules refuse the step from the unlock screen to the set-up form (a prototype gap; the fresh-PC route, Launch the app as Fresh PC, reaches the form). | Press Set a new master key (nothing visible happens). |
| `010-fresh-pc-start-screen.png` | First launch on a new PC: the start screen offers to protect cases with a master key. | Scenario s-fresh (Launch conditions: Fresh PC). |
| `011-master-key-setup-form.png` | The set-up form for a master key. | Press Protect your cases with a master key. |
| `012-master-key-too-short.png` | A master key that is too short is explained at once. | Type 3 characters in the first field. |
| `013-master-key-mismatch.png` | The two entries differ: the form says so. | Type a long key in the first field and a different one in the second. |
| `014-master-key-set-done.png` | Key set: back on the start screen (the header now shows Lock cases). | Make both fields equal, tick the confirmation box if shown, press Set master key. |
| `015-start-three-cards.png` | The start screen: Use an existing case, Enter an existing case, Create a new one. | Launch normally. |
| `016-start-card-existing-list.png` | The case list with the different row states: open, closed, own key, needs another try, working copy missing, legacy, erased. | Start screen, left card. |
| `017-start-card-enter.png` | The Enter an existing case card: code field, Open case, Restore from a case file. | Start screen, middle card. |
| `018-start-card-create.png` | The Create a new one card. | Start screen, right card. |
| `019-start-erase-missing-copy.png` | Erasing a case whose working copy is missing asks for its code. | Press Erase: Case whose files were moved. |
| `020-start-erase-typed-code.png` | The erase prompt with the code typed: Type movedcop to confirm. | Type movedcop in the box. |
| `021-start-erased-line-after-erase.png` | After Erase this case: only one line is left, saying the case was erased, with Clear. | Press Erase this case. |
| `022-start-erased-lines-cleared.png` | Clear removes the erased lines from the list. | Press Clear. |
| `023-start-archived-list.png` | The Archived cases toggle opened. | Press Archived cases (2). |
| `024-start-no-cases-yet.png` | A new user: the list says there are no cases yet; the three cards are still there. | Scenario s-empty (No cases yet). |
| `025-start-list-fails.png` | The case list could not be loaded, with Try again. | Scenario s-fail (Case list fails to load, then works). |
| `026-start-list-after-retry.png` | Try again works the second time. | Press Try again. |
| `027-start-list-paging.png` | Long list: 50 shown, with Show more cases. | Scenario s-many (120 cases). Only the bottom of the long list is shown. |
| `028-start-list-show-more.png` | After Show more cases. | Press Show more cases. Only the bottom of the long list is shown. |
| `029-start-older-cases-note.png` | The note about older cases left in the user folder. | Scenario s-old (Older cases left in the user folder). |
| `030-start-case-archiving.png` | A case being archived shows Archiving 45% and has no Open button. | Scenario s-archiving. |
| `031-enter-code-invalid.png` | A code that is not a code: the one not-found sentence. | Type "not a code" in the code field, press Open case. |
| `032-enter-code-short-refused.png` | The 8-character code shown in lists is refused (the card asks for the full code). | Type payrollr, press Open case. |
| `033-enter-code-full-opens-case.png` | The full code (spaces, dashes and case ignored) opens the case home. | Prototype panel > Show the full case code, type it, press Open case. |
| `034-enter-code-keyed-case-key-card.png` | A code for a case with its own key lands on the case key card. | Type the full code of the Vendor portal case, press Open case. |
| `035-restore-card-open.png` | Restore from a case file: path and master key fields. | Press Restore from a case file. |
| `036-restore-empty-path.png` | Empty path refused. | Press Restore as a new case with nothing typed. |
| `037-restore-wrong-extension.png` | The file name must end in .unravis-case. | Path C:\Cases\x.txt, any key, Restore as a new case. |
| `038-restore-network-path.png` | Network paths are not accepted. | Path \\server\share\a.unravis-case, Restore as a new case. |
| `039-restore-wrong-key.png` | Wrong master key: the single failure sentence. | Valid path C:\Cases\orders-slowdown.unravis-case with the key "wrong". |
| `040-restore-success-new-case.png` | Restore always makes a NEW case and opens it. | Same path, key "correct horse battery staple", Restore as a new case. |
| `041-restore-scenario-card.png` | Scenario with the restore card already open and a hint for the test values. | Scenario s-restore. |
| `042-restore-needs-master-key.png` | On a fresh PC, restoring needs a master key first (set-up link). | Scenario s-fresh, press Restore from a case file. |
| `043-create-name-step-empty.png` | Step 1 of 3: Name. Next stays off until the name is valid. | Start screen > Create a new case. |
| `044-create-name-too-short.png` | A one-letter name: at least 2 characters. | Type "a" in the name field. |
| `045-create-name-too-long.png` | A name over 80 characters is refused at once. | Type 81 letters. |
| `046-create-environments-deduplicated.png` | Environments typed with a duplicate: the duplicate is left out and the note says so. | Name "Checkout slowdown", environments "Production, production, Staging". |
| `047-create-own-case-key-too-short.png` | Own case key switched on, too short. | Tick the own-key box, type "short". |
| `048-create-own-case-key-mismatch.png` | The two key entries differ. | Type a long key, then a different repeat. |
| `049-create-name-step-valid.png` | Name step filled in correctly: Next is on. | Make the repeat equal to the key. |
| `050-create-sources-this-computer-start.png` | Step 2 of 3: Sources. Four methods across the top; This computer is first. | Press Next. |
| `051-create-sources-scan-results.png` | Scan results: ready sources, needs-an-export and unreadable places. | Press Scan this computer. |
| `052-create-sources-where-is-it.png` | The Where is it? helper next to a place that could not be used (look in a folder yourself). | Press Where is it? beside the place that could not be used. |
| `053-create-sources-one-found-added.png` | One found source added with its Add button; it appears in Sources in this case on the right. | Press Add on IIS web log · Default Web Site (W3SVC1). |
| `054-create-sources-add-all-ready.png` | Add all ready sources adds every source that is ready in one go. | Press Add all N ready sources. |
| `055-create-sources-mongo-empty.png` | MongoDB method: saved connection list, address, user, TLS, password, Test and list databases, and the roles script. | Tab MongoDB. |
| `056-create-mongo-saved-connection.png` | Choosing a saved connection fills in the address. | Pick Payroll audit Mongo (password saved) in the saved-connection list. |
| `057-create-mongo-address-no-port.png` | An address with no scheme and no port: the default port 27017 is used and shown. | Type audit-mongo in the address field. |
| `058-create-mongo-bad-port.png` | A bad port is named in words. | Type host:99999. |
| `059-create-mongo-databases-listed.png` | After Test and list databases: a database checklist with a profiler chip each; system databases left out. | Address audit-mongo, press Test and list databases. |
| `060-create-mongo-databases-ticked.png` | Two databases ticked; an audit-named one defaults to audit data, the other to application data; each can be both. | Tick PayrollAudit and PayrollApp, then also tick application data on PayrollAudit. |
| `061-create-mongo-added.png` | The two databases added as sources from one connection. | Press Add 2 databases. |
| `062-create-mongo-permission-script.png` | The MongoDB permission script (roles) that Unravis shows but never runs. | MongoDB tab, scroll to the roles script. |
| `063-create-sql-form-empty.png` | SQL Server method: where it runs, authentication, encrypt, trust certificate, remember password. | Tab SQL Server. |
| `064-create-sql-no-port.png` | Server name with no port: the usual port 1433 is used. | Type db1 in the server field. |
| `065-create-sql-azure-flavor.png` | Where it runs: Azure SQL Database (one connection per database). | Choose Azure SQL Database in the where-it-runs list. |
| `066-create-sql-windows-auth.png` | Windows Authentication hides the user and password fields. | Choose Windows Authentication. |
| `067-create-sql-encrypt-trust-remember.png` | Encrypt options, Trust server certificate and Remember password. | SQL authentication, Encrypt Strict, tick Trust server certificate and Remember password. |
| `068-create-sql-saved-connection.png` | Choosing a saved SQL connection fills the form. | Pick PROD-SQL-01 (orders) (password saved) in the saved list. |
| `069-create-sql-login-failed.png` | A wrong login is refused in words. | New connection, server db1, user unravis_reader, password wrong, Test and list databases. |
| `070-create-sql-databases-listed.png` | After a good Test: database checklist with Query Store chips; system databases hidden. | Correct password "secret", Test and list databases. |
| `071-create-sql-databases-ticked.png` | Two databases ticked; Add button counts them. | Tick orders and PayrollApp. |
| `072-create-sql-added-grouped.png` | One connection, two databases, added together and grouped in the sources panel. | Press Add 2 databases and keep details. |
| `073-create-sql-permission-script.png` | The SQL permission script (per server and per database), with a version switch and Copy. | SQL Server tab, scroll to the permission script. |
| `074-create-files-method-empty.png` | Import files method: folder or file location, Look here, and a drop zone. | Tab Import files. |
| `075-create-files-drag-drop-file.png` | A dropped file is recognised: suggested type and, where needed, a time-zone choice. | Drop (or choose in the drop zone) a file named HTTPERR1.log. |
| `076-create-files-timezone-chosen.png` | HTTPERR writes server local time, so the time zone must be chosen; Add 1 file is then available. | Choose a time zone (+04:00) for the dropped file. |
| `077-create-files-added.png` | The file lands in the sources panel. | Press Add 1 file. |
| `078-create-files-by-folder.png` | A folder location lists its files, each with a suggested type; unrecognised ones are left out; compressed ones are refused. | Tab Import files, type C:\inetpub\logs\LogFiles\W3SVC1, press Look here. |
| `079-create-files-folder-adjusted.png` | The user switches one suggestion off and gives an unrecognised file a type and a time zone. | Change the type and time-zone selects in the list. |
| `080-create-files-folder-added.png` | The chosen files added in one go; the rest are reported as left out. | Press Add N files. |
| `081-create-files-by-one-file.png` | A single file location selects just that file. | Type C:\inetpub\logs\LogFiles\W3SVC1\u_ex261020.log, Look here. |
| `082-create-files-relative-path-refused.png` | A relative path is refused with an example. | Type logs\x, Look here. |
| `083-create-chosen-sources-summary.png` | The chosen-sources panel as a short collapsed summary. | After adding sources, look at the right-hand panel. |
| `084-create-chosen-sources-edit-all.png` | Edit all opens every source with its environment pills, portal and API choices and Remove. | Press Edit all. |
| `085-create-chosen-sources-env-pills.png` | Environment is free text: several pills per source, with suggestions from earlier names. | In the first source, type "Client A Staging", Enter, "Client B Live", Enter. |
| `086-create-chosen-sources-collapsed-again.png` | Collapse all returns to the summary. | Press Collapse all. |
| `087-create-review-step.png` | Step 3 of 3: Review lists what will be made, with the admin notice and how far back. | Press Next. |
| `088-create-review-admin-notice-ticked.png` | Review: some sources need administrator rights; the notice box must be ticked before Create case is on (until then the page says Tick the box above to confirm the notice). | Tick the administrator notice box. |
| `089-create-leave-guide-asks.png` | Leaving a draft with entries asks first: Keep working or Leave and discard. | Press Leave the guide. |
| `090-create-creating-progress.png` | Creation progress: the case, environments and each source read in turn. | Press Keep working, then Create case (the screen is brief; captured as soon as it appears). |
| `091-create-failed-source-open-anyway.png` | One source could not be read; the case was kept; Open the case anyway. | Wait for the progress to end. |
| `092-create-opened-with-retry-list.png` | The new case home with Needs another try (the failed source). | Press Open the case anyway. |
| `093-create-retry-fails-once.png` | Try again fails the first time and stays listed (simulated). | Press Try again. |
| `094-create-retry-succeeds.png` | Second Try again succeeds; the list is gone. | Press Try again again. |
| `095-create-resume-draft-banner.png` | A left-behind draft is offered again, with Start over. | Scenario w-resumed (Resume an earlier draft). |
| `096-create-review-empty-case.png` | Review with a name and no sources: the button says Create empty case. | Scenario w-empty. |
| `097-create-review-one-source-will-fail.png` | Review where one source will fail to read. | Scenario w-review. |
| `098-open-case-from-list.png` | The list row to press: Open case. | Start screen, left card. |
| `099-case-home-full.png` | Case home, whole page: header, Analysis, Investigation Timeline, Databases, Story so far, Theories, Health checks, Saved connections, Files, Case key, Erase. | Press Open case on Payroll.Run slowdown, 20 Oct. |
| `100-case-home-header.png` | The case header: title, counts, All cases and Archive this case. | Case home, top. |
| `101-case-block-analysis.png` | Analysis block: Open the analysis. | Case home. |
| `102-case-block-timeline.png` | Investigation Timeline block: Open the Investigation Timeline. | Case home. |
| `103-case-block-databases.png` | Databases block: grouped by connection, with environment and history chip. | Case home. |
| `104-case-block-story.png` | Story so far. | Case home. |
| `105-case-block-theories.png` | Theories block. | Case home. |
| `106-case-block-health.png` | Health checks block. | Case home. |
| `107-case-health-checks-run.png` | Health checks after Run checks, with history. | Press Run checks. |
| `108-case-block-saved-connections.png` | Saved connections: SQL Server and MongoDB, with Test, Edit, Forget, + SQL Server, + MongoDB, Refresh. | Case home. |
| `109-case-saved-connection-test.png` | Testing a saved connection. | Press Test on the first connection. |
| `110-case-saved-connection-forget-asks.png` | Forgetting a saved connection asks first. | Press Forget. |
| `111-case-block-files.png` | Files block: what is in the case and Add a file. | Case home. |
| `112-case-block-case-key.png` | Case key block: Add a case key. | Case home. |
| `113-case-block-erase.png` | Erase this case block. | Case home. |
| `114-theories-ledger-drawer.png` | The theories (ledger) drawer. | Press Open the theories. |
| `115-theories-after-add.png` | A theory added, with its state. | Type a theory, press Add. |
| `116-connections-drawer.png` | Adding a saved connection from the case home (+ SQL Server). | Press + SQL Server. |
| `117-case-key-add-page.png` | Add a case key: the form. | Press Add a case key. |
| `118-erase-case-page.png` | Erase this case: you must type the case code. | Press Erase this case. |
| `119-case-needs-another-try.png` | A failed import and an unsaved source name on the Needs another try list. | Scenario c-failed. |
| `120-case-retry-add-file-again.png` | Add the file again opens the import drawer. | Press Add the file again. |
| `121-case-closed.png` | A closed case: read-only, the Timeline and add buttons are off with the reason; Reopen this case. | Scenario c-closed. |
| `122-case-reopened.png` | Reopen this case: everything is on again. | Press Reopen this case. |
| `123-case-key-card.png` | A case with its own key: the key card replaces the page. | Scenario c-keyed. |
| `124-case-key-wrong.png` | A wrong case key. | Type a wrong key, press Open case. |
| `125-case-key-throttled.png` | Three wrong tries: wait. | Repeat twice more. |
| `126-case-key-right.png` | Right key (vendor-case-key-01, test aid): the case opens. | Wait, type the key, press Open case. |
| `127-case-archived.png` | Archived case: only the record, the case file and the restore choices. | Scenario c-archived. |
| `128-case-legacy.png` | Legacy case that cannot be opened: Restore from a case file or Erase. | Scenario c-untrusted. |
| `129-case-legacy-restore-intent.png` | Restore from a case file takes you to the start screen with the restore card open. | Press Restore from a case file. |
| `130-case-loading.png` | Opening the case... | Scenario c-loading. |
| `131-case-load-failed.png` | The case could not be opened, with Try again. | Scenario c-loadfail. |
| `132-case-gone.png` | The case could not be found; it may have been erased. | Scenario c-gone. |
| `133-case-changed-in-another-tab.png` | Page is out of date; changes are off until Reload. | Scenario c-changed. |
| `134-start-missing-working-copy-row.png` | A case whose working copy is missing, with Erase. | Start screen list (row: Case whose files were moved). |
| `135-archive-drawer.png` | Archive this case: folder, sealed-with choice, passphrase, Archive the case. | Press Archive this case. |
| `136-archive-progress.png` | Archiving: folder and passphrase filled in, Archive the case pressed; the screen shows progress. | Fill the folder and passphrase, press Archive the case. |
| `137-archive-result-case-home.png` | The case after archiving: read-only, with Restore, Restore as a new case, Free the working copy. | After the progress ends. |
| `138-archive-restore-drawer.png` | Restore: the case goes back to open exactly as archived. | Press Restore on an archived case. |
| `139-archive-restored-case-home.png` | After Restore this case: the case is open again. | Press Restore this case. |
| `140-archive-restore-as-new-drawer.png` | Restore as a new case: a second case is made from the case file; this one stays as it is. | Press Restore as a new case. |
| `141-archive-restore-as-new-done.png` | A line says Restored as a new case with a new code; this archived case is left as it was. | Press Restore as a new case in the drawer. |
| `142-archive-free-working-copy-drawer.png` | Free the working copy: gives back disk space; the case file stays the only copy. | Press Free the working copy. |
| `143-archive-free-working-copy-done.png` | After freeing: the case is archived with no working copy. | Press Free the working copy in the drawer. |
| `144-import-drawer-open.png` | Import files drawer on an existing case: Choose a file, host list, Preview, and the history of imports. | Press Add a file in the Files block. |
| `145-import-file-chosen.png` | A file chosen: Unravis says what it looks like and asks which host it came from. | Choose u_ex261020_siteB.log in Choose a file. |
| `146-import-preview-no-timezone-rule.png` | Preview: rows read, kept and rejected, time covered. This file has no time-zone rule, so the timeline cannot line it up. | Press Preview. |
| `147-import-file-imported.png` | After Import file: the file is added to the history table. | Press Import file. |
| `148-import-sql-error-log-preview.png` | A SQL Server error log previewed with a validated UTC time-zone rule. | Choose sql_errorlog_2026-10-20.txt, press Preview. |
| `149-import-duplicate-file.png` | A copy of a file already imported is refused as a duplicate. | Choose u_ex261020_copy.log, Preview, Import file. |
| `150-import-empty-file.png` | An empty file is refused. | Choose empty.log. |
| `151-import-too-big-file.png` | A file over the size limit is refused with its size and the limit. | Choose huge_export_2026.log. |
| `152-import-compressed-file.png` | A compressed file is refused: extract it first. | Choose logs-october.zip. |
| `153-import-close-case-button.png` | Close case lives in this drawer: a closed case is erased 30 days after it was closed. | Press Close case in the Import drawer. |
| `154-header-import-and-ledger.png` | The case header buttons: Home, Ledger, Import. | Case home, top bar. |
| `155-analysis-open-from-case-home.png` | The analysis as it opens from the case home: last hour against the equal period right before it. | Case home > Open the analysis. |
| `156-analysis-scope-bar.png` | The scope bar: quick ranges, Type, More filters and Run analysis. | Analysis, top. |
| `157-analysis-more-filters-when.png` | More filters opened: Investigate From/To and Reference From/To with the time zone and UTC echo. | Press More filters. |
| `158-analysis-quick-range-4-hours.png` | A quick range chip re-runs the analysis at once. | Press Last 4 hours. |
| `159-analysis-slowdown-pulse.png` | Pulse for the slowdown against a good earlier hour: five figures with change against the reference, headline, chart. | Scenario a-slow. |
| `160-analysis-kind-filter-async.png` | Type filter: only one kind of request. | Press Async in the scope bar. |
| `161-analysis-reference-overlap-blocked.png` | The analysis refuses a comparison period that overlaps the investigated period; Run analysis is off and the hint says why. | Scenario a-overlap. |
| `162-analysis-window-over-7-days.png` | Each window can be at most 7 days here (the timeline allows 30). | Scenario a-long. |
| `163-analysis-reference-no-data.png` | The comparison period has no data: no deltas, ratios or headline. | Scenario a-sparse. |
| `164-analysis-reference-half-covered.png` | The comparison period is only partly covered: deltas stay off, the note says why. | Scenario a-partial. |
| `165-analysis-no-data-in-period.png` | No requests in the investigated period. | Scenario a-empty. |
| `166-analysis-source-failed-banner.png` | A banner names the failing source; the figures use the others. | Scenario a-srcfail. |
| `167-analysis-sources-popover.png` | The sources button opens the list of sources used. | Press the sources button (the dot and the number of sources) in the sub-bar. |
| `168-analysis-without-a-case.png` | Analysis of the demo audit source with no case behind it: no Case home button and no timeline. | Start screen > Go to the analysis (scenario a-nocase). |
| `169-where-is-it-view.png` | Where is it?: operations ranked by lens, with comparison to the earlier period. | Analysis > Where is it? |
| `170-where-is-it-biggest-change.png` | Ranking by Biggest change. | Press Biggest change. |
| `171-evidence-rows-for-operation.png` | Evidence: slowest requests for one operation, with Export CSV, Inspect and Nearby. | Press the Payroll.Run row. |
| `172-record-detail.png` | Record detail: stored fields and fields derived by Unravis. | Press Inspect record on a row. |
| `173-nearby-activity.png` | Nearby activity: one call beside what SQL, Mongo and IIS did. Call trace is not wired (the screen says so). | Back to the evidence, press Nearby. |
| `174-analysis-breadcrumb-back.png` | The breadcrumb and Back return up one level. | Press Pulse in the breadcrumb. |
| `175-drawer-patterns.png` | Patterns drawer (three canned entries in the prototype). | Press Patterns in the top bar. |
| `176-drawer-ledger.png` | Ledger drawer from the top bar (theories). | Press Ledger in the top bar. |
| `177-drawer-glossary.png` | Glossary from the header. | Press Glossary. |
| `178-drawer-blackbox.png` | Blackbox from the header (stub in the prototype). | Press Blackbox. |
| `179-export-drawer-ready.png` | Export drawer: window, options, scrub on by default, Preview. | Analysis > Export. |
| `180-export-preview-scrub-on.png` | After Preview: what will be left out (client IP, referer, cookie hash) and the Save the export button. | Press Preview. |
| `181-export-saved.png` | After Save the export: Saved. | Press Save the export. |
| `182-export-changed-409.png` | The case changed after the preview: Save the export gives 409 and a new preview is needed. | Scenario x-changed, press Save the export. |
| `183-export-preview-again.png` | Preview again after a 409. | Press Preview again. |
| `184-export-scrub-switched-409.png` | Scrub switched after the preview also gives 409. | Scenario x-scrub, press Save the export. |
| `185-timeline-opens-by-itself-loading.png` | The timeline opens by itself: the chart is drawn and the Suspects rail starts checking one suspect at a time. | Case home > Open the Investigation Timeline (captured a moment after the click). |
| `186-timeline-default-last-hour-full.png` | The whole screen with the default window: last hour against the hour before, all three lanes, the rail finished. | Wait a few seconds after opening; nothing else pressed. |
| `187-timeline-controls-card.png` | The controls card: quick ranges, Investigation window, Compare it with, Sources chips, Add evidence, Refresh timeline. | Timeline, top. |
| `188-timeline-quick-range-4-hours.png` | A quick range chip sets the window to that period ending now and the comparison period to the equal period right before. | Press Last 4 hours. |
| `189-timeline-picker-investigation-window.png` | The Investigation window picker: quick ranges, calendar, start and end times in the chosen zone, Apply and Cancel. | Press the Investigation window button. |
| `190-timeline-picker-applied.png` | After choosing "The slowdown in the demo data": the window reads 09:00 to 12:00 UTC. | In the picker press The slowdown in the demo data. |
| `191-timeline-picker-comparison-period.png` | The Compare it with picker: The equal period right before the incident, Same time yesterday. | Press the comparison period button. |
| `192-timeline-picker-closed-by-outside-click.png` | A click outside the picker closes it without changing anything. | Click anywhere outside the picker. |
| `193-timeline-sources-chip-off.png` | Sources chips: switching IIS off leaves two lanes to draw. | Press the IIS chip, then Refresh timeline. |
| `194-timeline-refresh-timeline.png` | Refresh timeline re-reads and redraws after the windows or sources changed. | Press Refresh timeline. |
| `195-timeline-slowdown-full.png` | The slowdown with a real comparison (09:00 to 11:30 against the 2.5 hours before): the whole screen. | Scenario t-slow (The slowdown, with a real comparison). |
| `196-timeline-summary-line-and-mixed-note.png` | The summary line (bar size, bars on the axis, window) and the mixed-time note that cannot be dismissed. | Timeline, under the controls. |
| `197-timeline-figure-cards.png` | The seven overall figure cards: requests, P95, 5xx, IIS 5xx, IIS P95, slow SQL calls, errors and blocking. | Timeline, above the charts. |
| `198-timeline-what-unravis-sees.png` | The reading aid: Application, Web server, Database tiles in plain words. | Timeline, under the chart. |
| `199-timeline-board-three-lanes.png` | The trace board: three lanes on one shared time axis with titled panels and legends. | Timeline, the big chart card. |
| `200-timeline-hover-bucket.png` | Hovering a bar shows one line per lane for that moment. | Move the pointer over the chart. |
| `201-timeline-select-bucket.png` | Clicking a bar selects that bucket across all lanes and fills the bucket panel. | Click a bar. |
| `202-timeline-bucket-panel.png` | The bucket panel: the chosen time, what each lane saw, and Open Run analysis for this bucket. | After selecting a bar. |
| `203-timeline-keyboard-arrow.png` | Left and Right arrow keys move the selection; Home and End jump to the ends. | Press the Right arrow key. |
| `204-timeline-handoff-to-analysis.png` | Open Run analysis for this bucket: the analysis opens with a strip saying it came from the Investigation Timeline and shows both windows. | Press Open Run analysis for this bucket. |
| `205-timeline-handoff-back.png` | Back to the timeline keeps both windows and the selection. | Press Back to the timeline. |
| `206-timeline-details-sections.png` | Three folded sections: What each lane shows, How this axis was worked out, Timeline details; plus Words used on this screen. | Scroll to the bottom of the timeline. |
| `207-timeline-details-axis-open.png` | How this axis was worked out, opened: start and end you chose, axis start and end, bucket size, raw scans. | Open How this axis was worked out. |
| `208-timeline-layout-a-report.png` | Layout A (Report): one column, top to bottom. | Prototype controls (bottom bar): layout pill, choose A. |
| `209-timeline-layout-b-workbench.png` | Layout B (Workbench): windows and sources in a side panel, figures and a taller chart on the right. | Prototype controls: layout pill, choose B. |
| `210-timeline-layout-c-lane-ledger.png` | Layout C (Lane ledger): one row per lane with trust stickers, event count and a sparkline; open a lane for its panels. | Prototype controls: layout pill, choose C. |
| `211-suspects-rail-top-five.png` | The Suspects rail: legend, summary line, and the top five suspects (red first, then orange, then grey). | Timeline, right side. |
| `212-suspects-rail-see-all.png` | See all expands in place: every suspect, red then orange then grey, green ones folded as Ruled out by the data. | Press See all. |
| `213-suspects-show-on-chart.png` | Show on chart selects the bucket where that suspect first moved. | Press Show on chart on the red suspect. |
| `214-suspects-ruled-out-folded.png` | Green suspects are folded away under Ruled out by the data. | Open the Ruled out by the data fold at the bottom of the list. |
| `215-suspects-show-fewer.png` | Show fewer returns to the top five. | Press Show fewer. |
| `216-suspects-card-open-evidence.png` | One suspect opened: evidence rows (signal, Must or Expect, yes, no or unknown, why), the cheapest check and what would overturn it. | Open the evidence on a suspect card. |
| `217-evidence-card-folded.png` | The Where does the evidence point card, folded: title and strength chip only. | Scroll below the chart. |
| `218-evidence-card-open-one-environment.png` | Opened: The problem appears isolated to Production, with the server block and the by-database table. | Open the folded card. |
| `219-evidence-scope-all-databases.png` | Scope switch: All databases on this server, with an unnamed Other databases row. | Press All databases on this server. |
| `220-evidence-scope-show-names.png` | Show their names reveals the unpicked databases, with a warning to hide them before sharing an export. | Press Show their names. |
| `221-evidence-scope-server-only.png` | Scope switch: Server only. | Press Server only. |
| `222-evidence-verdict-whole-server.png` | The whole database server appears affected (every picked database slowed, disk saturated). | Scenario a-server (Investigation Timeline: where does the evidence point). |
| `223-evidence-verdict-noisy-neighbour.png` | Another database may be affecting yours (unpicked databases take most of the disk). | Scenario a-noisy (Investigation Timeline: where does the evidence point). |
| `224-evidence-verdict-not-enough-evidence.png` | There is not enough evidence to narrow it down (Query Store off, no samples). | Scenario a-blind (Investigation Timeline: where does the evidence point). |
| `225-evidence-verdict-quiet-window.png` | No clear performance problem in this period. | Scenario a-quiet (Investigation Timeline: where does the evidence point). |
| `226-timeline-no-history-full.png` | No history (nothing kept): most suspects are grey with a Needs line, nothing is red. | Scenario a-blind: the whole screen. |
| `227-suspects-rail-no-history.png` | Suspects rail in the no-history situation. | Scenario a-blind. |
| `228-timeline-one-environment-full.png` | One environment: D1 blocking chain is the only red suspect. | Scenario a-env. |
| `229-timeline-seven-days-gaps.png` | Last 7 days: 2-hour bars, SQL and IIS say there are no logs for most of the window (hatched, with exact range), the audit outage is a thin gap. | Press Last 7 days. |
| `230-timeline-seven-days-board.png` | Close-up of the seven-day board with the gap labels. | Same screen, chart card. |
| `231-timeline-seven-days-show-all-panels.png` | A lane with logs for under 15% of the window folds to one short panel; Show all panels expands it. | Press Show all panels on a folded lane. |
| `232-timeline-seven-days-evidence-card.png` | Evidence card in the seven-day situation. | Same screen, open the evidence card. |
| `233-timeline-overlap-notice.png` | The two windows overlap: a non-blocking notice. The timeline still runs. | Scenario w-overlap. |
| `234-timeline-overlap-fix.png` | Move it to just before the investigation window fixes the overlap in one press. | Press Move it to just before the investigation window. |
| `235-timeline-30-day-limit-refused.png` | A window of 30 days and 1 second is refused; Show timeline is off. | Scenario w-30dx. |
| `236-timeline-30-days-8-hour-bars.png` | 30 days starting at 00:01: bars coarsen to 8 hours, 91 on the axis, two boundary raw scans. | Scenario w-30d. |
| `237-timeline-no-comparison-sparse.png` | The comparison period has no data: cards say Cannot compare with the earlier period. | Scenario w-sparse. |
| `238-timeline-sparse-lanes.png` | Each lane says No comparison with the reason. | Same screen, chart card. |
| `239-timeline-closed-case.png` | A closed case: Show timeline is off with the reason; nothing is drawn. | Scenario t-closed. |
| `240-timeline-timezone-gate.png` | A file with no checked time zone switches the multi-source chart off as a whole. | Scenario t-tz, Show timeline. |
| `241-timeline-nothing-stored.png` | A case with nothing stored: SQL and IIS say this case has no file for this source. | Scenario t-empty, Show timeline. |
| `242-timeline-half-stored.png` | Only half the window is stored for SQL: a gap is labelled on that lane. | Scenario t-partial, Show timeline. |
| `243-timeline-saved-lanes-only.png` | Saved lanes only (audit removed): nothing is a live read, so no mixed-time note. | Scenario t-saved, Show timeline. |
| `244-whatif-audit-over-budget.png` | What if: audit read exceeds its budget: the lane is flagged incomplete and nothing is shortened. | Prototype controls > What if > Audit read exceeds its budget, then Refresh timeline. |
| `245-whatif-audit-unreachable.png` | What if: audit source unreachable. | Prototype controls > What if > Audit source unreachable, then Refresh timeline. |
| `246-whatif-boundary-scan-over.png` | What if: boundary scan exceeds 50,000 rows. | Prototype controls > What if > Boundary scan exceeds 50,000 rows, then Refresh timeline. |
| `247-whatif-suspect-d16-sql-timeouts.png` | What if: SQL commands time out (D16) turns that suspect orange or red. | Prototype controls > What if > SQL commands time out (D16). |
| `248-whatif-suspect-s10-app-pool-offline.png` | What if: app pool goes offline (S10). | Prototype controls > What if > App pool goes offline (S10). |
| `249-whatif-suspect-m12-mongo-rejects.png` | What if: MongoDB rejects connections (M12). | Prototype controls > What if > MongoDB rejects connections (M12). |
| `250-mvp2-coverage-ledger-no-coverage.png` | MVP-2 (planned): coverage ledger says no coverage and offers Pull this window. | Scenario m-first (prototype panel: MVP-1 + MVP-2). |
| `251-mvp2-first-pull-done.png` | MVP-2: a pull saves the incident window. | Press Pull this window. |
| `252-mvp2-overlap-409.png` | MVP-2: a window that partly overlaps a stored pull is refused (409) with the remedy. | Scenario m-overlap, press Pull this window. |
| `253-prototype-dock-bottom-bar-timeline.png` | The prototype-only bottom bar on the timeline: layout pill, prepared-situation picker, What if switches, last action, Prototype and State buttons. | Always visible at the bottom of the prototype page. |
| `254-prototype-panel-launch-and-stage.png` | The Prototype panel: launch the app as Normal, Key locked, Key damaged or Fresh PC; release stage MVP-1 or MVP-2; test aids; assumptions and questions. | Press Prototype in the bottom bar. |
| `255-prototype-state-panel.png` | The State panel: full state after every action and the action log. | Press State in the bottom bar. |
| `256-narrow-timeline-phone-width.png` | The timeline at phone width: single column, rail below the chart, no sideways scroll. | Resize the window to 390 pixels wide. |
| `257-narrow-case-home-phone-width.png` | The case home at phone width. | Resize the window to 390 pixels wide, open the case. |
