# Silent updates: what the owner asked for, what is built, what can break

Owner rule (2026-11-21): an update is just an update. No terminal, no setup window. If the app is in use, say the update is ready and let the person apply now or later. If it is not in use (idle, or running in the background), update by itself and start again. If it does not come back, tell the person and go back to the old version. Every way the flow can break must be handled.

## How it works now

1. **Check** (unchanged): every 30 minutes, signed feed, no telemetry.
2. **Download** (new, automatic): when a verified newer version exists, it downloads in the background (size capped, SHA-256 checked, signature already checked). A failed download waits 5 min, 15 min, 1 h, then 6 h before the next try.
3. **In use?** The page tells the program it is being used (every 45 s, only while the page is visible and was touched in the last 5 minutes). "In use" = a sign within the last 2 minutes. A program with no page open, or a page left alone, counts as not in use.
4. **In use:** the bar says "Version X is ready. It installs by itself when you are not using Unravis, or press Apply now." Buttons: Apply now, Later. Nothing is interrupted.
5. **Not in use:** the program starts the setup program silently (`--silent --upgrade --close-app --expect-sha256 --expect-version`, hidden). Setup asks Unravis to quit (it finishes its work), replaces the files, **starts Unravis again**, and writes a plain result note.
6. **Result:** the next start of the page shows the note ("Updated", or "did not work and was undone, staying on X") with OK. If Unravis could not be started again, setup shows a message box (the one case with no other way to tell the person).
7. **Apply now** uses the same silent path (no window). Windows asks for approval only when the install needs it.

Automatic apply happens only when no administrator approval is needed (a just-for-me install with no Blackbox). Otherwise there is nobody to click, so the bar asks the person (Apply now, then Windows asks). Settings: `applyAuto` (default on) can switch automatic apply off; checking stays as it was.

## Failure scenarios

Status: **done** = handled and tested before this work; **new** = built and tested in this change; **gap** = tracked in the backlog.

| # | What breaks | What happens | Status |
|---|---|---|---|
| 1 | No network / feed unreachable | Check fails, retries with back-off (x2 up to 6 h), nothing else changes | done |
| 2 | Download cut off or wrong size | Download refused, file deleted; automatic retry waits 5 min, 15 min, 1 h, 6 h | new (backoff) |
| 3 | Bad or missing signature, wrong channel, expired, older version | Update ignored, nothing downloaded or run, status "unverified" | done |
| 4 | Downloaded file changed on disk | Re-hashed right before start (held open); refused | done |
| 5 | Disk full | Download fails ("not enough space" shown); setup checks free space before changing anything and refuses | done |
| 6 | Antivirus blocks or deletes the setup file | Start fails ("closed right away"), state failed, shown in the bar; automatic apply stops after 2 tries for that version | new (tries) |
| 7 | A file is locked (another program holds a program file) | Setup step fails, the engine undoes every step, old version is started again, note says so | done (engine) + new (restart, note) |
| 8 | No administrator rights / UAC not answered | Automatic apply is skipped for installs that need it; bar asks. Apply now: UAC refused = nothing changed, exit 3, note | new |
| 9 | Two copies running (another Windows user, or another install folder) | Setup refuses ("open for another"), nothing changed, note | done (setup) + new (note) |
| 10 | The person starts using the app while it applies | Setup asks the app to quit and waits (up to 5 s), so work is saved; the app restarts and the page reconnects | new |
| 11 | New version crashes on start | After 3 starts that did not last a minute, the app puts the version on the skip list and runs the kept old setup silently; note says so | done (U5) + new (silent) |
| 12 | Update done but the app does not start | Setup tries to start it; if that fails, a message box and the note say so, with the Start menu hint | new |
| 13 | PC sleeps or hibernates mid-update | Process resumes; setup is transactional | done (engine) |
| 14 | Power loss or setup killed mid-update | Journal rollback runs the next time setup runs; the app itself does not run it | **gap** BL-170 |
| 15 | Automatic apply keeps failing | Stops after 2 tries for that version; the bar shows it | new |
| 16 | The same signed file offered as another version | Setup is told `--expect-sha256` and `--expect-version` and checks itself | done |
| 17 | Blackbox service on the PC | Updated by setup in the same run (needs approval, so Apply now); a standalone service updates itself | done (U6) |
| 18 | A console or setup window appears | Setup is a windowless program; the silent run starts it hidden; nothing starts a console | new |
| 19 | Updater cannot show the note (app not running) | Note stays on disk, shown at the next start | new |

## Not proven yet

Verified by Windows CI tests: the flag rules, the result note, the idle and apply logic, back-off, the no-approval rule. **Not verified on live data**: a real silent update on a PC (close, replace, restart, note) and a real rollback. The first live run is rc.53 to the next release on the owner's PC.
