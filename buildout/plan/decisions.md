# Decisions

Part A lists what the owner has settled. Do not reopen these in a phase.
Part B lists planner proposals. They are marked PROPOSAL and stand until the owner says otherwise. Anything not decidable is in plan.md, Open questions.

## Part A: settled by the owner

### Product rules

- A1. Unravis only reads. Anything that changes a server (grants, profiler set-up, firewall, service removal on another server) is shown as a script the owner runs.
- A2. Passwords sealed with DPAPI are never returned to the UI. The UI only learns "saved" or "not saved".
- A3. UI rule: outline means press me, sticker means information. One primary (accent-filled) button per screen at most.
- A4. "In a nutshell" is the label for every short plain-language box.
- A5. Fixed words. Readiness: Ready, Partly ready, Cannot test yet, Failed. Connection tests: Works, Failed, Not tested. Suspect colours: red (most likely), orange (likely), green (ruled out), grey (not sure or could not be tested). Grey is never a pass.
- A6. Missing data gives "Cannot test yet". It is never a pass.

### Case lifecycle (spec v7)

- A7. States: Open, Closed, Archiving, Archived (kept or freed), Untrusted, Erased. Missing is a listing condition (folder gone).
- A8. Close is reversible (Reopen). Reopen applies the saved Mongo link and filters and offers the sampler; it never starts it.
- A9. Archive does not close first. It writes `.part`, reads back, renames, then marks Archived under one lock (the single commit point). Failure, Cancel, a key lock or a crash returns the case to the state it was in.
- A10. Free = a real verify (test restore into a temporary store, checksum and counts), then one confirmation that names the file and folder and warns about the master key.
- A11. Archived cases are never read in place. Restore in place (only while the working copy is kept, always gives Closed) or Restore from the case file as a new case.
- A12. Keys: an optional PC-wide master key ("Not now" stays) and an optional per-case key (off by default). A master key reset does not touch a case with its own key. Demo never reads or asks for a key.
- A13. The key check value is HMAC-SHA256 under the Argon2id-derived key. The full value lives only inside the sealed record; files show "Key ending xxxx".
- A14. Create carries a request id; a server stop mid-create re-runs the remaining steps. The "needs another try" list is stored in the case and travels in a case file.
- A15. One lock per case. The page sends the state it believes; a mismatch is "This case changed in another tab" (409).
- A16. Erase: short code typed; running import stopped, streams closed, up to 30 s wait for readers; an Erasing mark is the commit; recovery at start-up, unlock or the next call.
- A17. Archive path rules: full local path, no network, no relative or `..`, symlinks and short names resolved, not inside the data folder, at most 240 characters with the file name. File name `<slug>-<short-code>-<yyyymmdd-hhmmss>.unravis-case`. Never overwrites. Free space 1.2 x case size. Size cap is the existing 8 GiB case database limit.
- A18. Unravis never deletes a finished `.unravis-case` file. Orphans are listed in the health panel.
- A19. No new link or token. The existing one-time launch link stays.
- A20. Message ids M1 to M19, acceptance criteria 1 to 10, Appendix C, failure-injection points INJ-1 to INJ-10 are the test contract.

### Investigation

- A21. Time model: D = A + C (time inside the app). P is outside D. O = IIS time-taken - D - P.
- A22. Little's Law uses lambda = 40 requests per second. Concurrency comes from the audit documents. Group sizes 20 and 100. IIS clock tolerance 1 s.
- A23. A change is meaningful only at 30% or more AND 100 ms or more.
- A24. Onset t0 = the first minute P95 stays above twice the reference for 5 minutes in a row.
- A25. Steps 0 to 10: Frame, Trust data, Shape, Time split, Load, Changes, App tier, SQL, Mongo, Host, Origin. Hypothesis groups Q, L, S, D, M, H, C.
- A26. The grey suspects in Release 1 are H2, M2 and H3. N17 (permission-check figures) is dropped.
- A27. Facts first, then signals, then evaluation. Thresholds are named and versioned. Golden tests prove the outputs.

### Sources and grants

- A28. Saved connections exist (`internal/sqlconn`, sealed `sqlconnections.dat`, max 20).
- A29. Mongo grants: clusterMonitor + read.
- A30. SQL Server 2022 grants: VIEW SERVER PERFORMANCE STATE, VIEW ANY ERROR LOG, VIEW DATABASE PERFORMANCE STATE, SQLAgentReaderRole. SQL Server 2019: VIEW SERVER STATE.

### Blackbox

- A31. A separate recorder that samples every 15 s.
- A32. Placed through a 4-part wizard: Name, Where, Settings, Review. Packed as a zip of about 6 MB (program, `blackbox.json`, README).
- A33. Runs as the Windows service `UnravisBlackbox`. Port 7788 serves status. A read key is made at install, shown once, and sealed in Unravis, never in case files.
- A34. Reached by folder, UNC share or URL:port. No network scan and no guest-book. The scan covers this PC only.
- A35. Gap rule: a gap of 2 minutes or less warns and the source is Partly ready. A gap over 2 minutes gives "Cannot test yet" for the minutes in the gap.
- A36. States: Running, Running late (more than 3 intervals without a sample), Stopped, Disk low (below 10% free), Clock differs (more than 1 s), Not found, Unreachable, Incomplete, Not placed.
- A37. Housekeeping cannot be turned off. Defaults: keep 7 days, 2 GB, 10% free disk, folder `D:\UnravisBlackbox`.
- A38. On this PC: Install, Start, Apply settings (restarts), Stop, Remove, "Delete the recordings too" (type the name to confirm), Snapshot. Service changes need admin approval. Another server gets user-run steps only (`sc stop UnravisBlackbox`, `sc delete UnravisBlackbox`).
- A39. Install failure reasons: no admin rights, port busy, folder not writable or less than 1 GB free, service already exists, antivirus. A failed install is undone.
- A40. SQL probe: NOT in Release 1 (owner ruling 2026-10-27, A43). It reverses the earlier "optional, off by default" ruling. The design stays for a later release: optional, OFF by default, ticked per Blackbox, SQL Server only; 5 s timeout, LOCK_TIMEOUT 2 s, DEADLOCK_PRIORITY LOW, every 15 s; read-only, creates nothing on the server; login storage to be decided then.

### Brand

- A41. Dark by default, follows the system theme, `data-theme` forces it. IBM Plex Sans and Mono bundled. Accent `#3fd6b0` (dark) / `#0b6b57` (light). Radius 2 px. Pending is hatched, never green.
- A42. Installer: same tokens and fonts, 56 px header with brand mark, left step rail with a 2 px accent marker, one primary button bottom right, step stickers Waiting / Running / Done / Failed, thin accent progress bar, admin approval explained before the Windows prompt.

### Owner rulings on the open questions (2026-10-27)

Answered in the visual walkthrough. They replace the planner picks for Q1 to Q10 in plan.md.

- A43. Q1: no Blackbox SQL probe in Release 1. P10 moves to Deferred.
- A44. Q2: Unravis stays a per-user app (not a service) but its data is shared by everyone who uses the PC. Owner's words: "Everything shared, but noone can see any passwords ever and they should not be able to bypass and hack anything". Shared means cases, saved connections and settings, kept in one machine-wide data folder (`C:\ProgramData\Unravis`) with locked permissions.
- A45. Q2 safety: a small helper service (the key keeper) holds the sealing key. Saved passwords are sealed to a dedicated service account. Users and the UI can only ask the keeper to open a connection; no one can ask it for the password itself (extends A2). Honest limit, shown in docs: a local administrator can stop the service or take over the machine, and no local scheme fully stops that. The plan states the limit and logs every keeper request.
- A46. Q3: installer is a Go single-file installer with an embedded web UI.
- A47. Q4: installer window is an app window (WebView2), falling back to Edge or Chrome app mode, then the default browser.
- A48. Q5: code signing through SignPath Foundation (free for open source). Condition: the repo must be public and meet their rules, and each release needs an approval. Until approved, releases ship unsigned with SHA-256 checksums and docs about the SmartScreen warning.
- A49. Q6: docs live in the repo (`docs/user/`) and the same pages are embedded as in-app help.
- A50. Q7: case identity is the long id plus an 8-character short code shown to users (spec v7 stands).
- A51. Q8: if the server has no modern browser, the installer warns, links to Edge, and carries on. It never blocks.
- A52. Q9: update check is a manual "Check for updates" button only. No background network calls.
- A53. Q10: the firewall rule for port 7788 is opt-in and scoped to addresses the owner types. Without a rule the Blackbox answers on loopback only.

## Part B: planner proposals

Each stands as the plan's default. Owner can overturn any of them.

- B1. PROPOSAL: Release 1 includes the Blackbox, even though spec v7 lists it as out of scope. Reason: later owner decisions (A31 to A40) put it in.
- B2. SETTLED as A44 and A45 with a change: Unravis stays a per-user app started at sign-in, but its data moves to a shared machine-wide folder and a small key-keeper service holds the sealing key. The Blackbox is also a service. Details of the keeper are PROPOSALS in plan.md P14.
- B3. SETTLED as A46. Installer is a Go self-extracting installer (`cmd/unravis-setup`) with an embedded local web UI.
- B4. SETTLED as A47. WebView2 window, else Edge or Chrome `--app`, else the default browser.
- B5. PROPOSAL: The installer runs as two processes: an unelevated UI process and an elevated worker started with "run as administrator", talking over a named pipe locked to the current user. Reason: an elevated process must never open the browser or the app.
- B6. PROPOSAL: Brand tokens move from the top of `web/src/app.css` to `web/src/brand/tokens.css`. The app and the installer both import that one file. A test fails on colour literals outside it (with a short allowlist).
- B7. PROPOSAL: Version comes from a git tag `vX.Y.Z`, stamped with Go ldflags into `internal/version`. Local builds say `git describe` output; untagged builds say `dev`.
- B8. PROPOSAL: Screenshots are taken by Playwright against `unravis.exe --demo` with a fixed demo clock (`UNRAVIS_DEMO_NOW`, honoured only with `--demo`), viewport 1440x900 (plus 390x844 for a few screens), both themes, timezone UTC, locale en-GB. Captures go to `buildout/screenshots/<phase>/<id>-<theme>.png`. The CI Windows runner is the reference renderer.
- B9. PROPOSAL: Demo gains capture-only cases: with `--demo` and `UNRAVIS_DEMO_CASES=1` and `AUDIT_CONFIG_DIR` set, a seeded case store is opened in that folder. Plain `--demo` is unchanged and still writes nothing.
- B10. Draft docs live in `buildout/docs/` during the build. Final location is settled as A49: `docs/user/` in the repo plus the same pages embedded as in-app help.
- B11. SETTLED as A52. Manual "Check for updates" button that calls the GitHub `releases/latest` API. No background calls, no auto-download.
- B12. SETTLED as A53. The firewall rule is opt-in, scoped to a remote address list the owner types, and shown as a script when the target is another server.
- B13. PROPOSAL: The pipeline signs through SignPath (A48) only when it is approved and the repo is public. Unsigned builds are still published with SHA-256 checksums and docs that explain the SmartScreen warning.
- B14. PROPOSAL: Investigation logic lives in a new package `internal/investigate` (facts, signals, evaluation), pure functions, golden tests. The UI only renders its output.
- B15. PROPOSAL: Blackbox recorder is a new binary `cmd/unravis-blackbox` that reuses `internal/sampler`, writes a manifest plus NDJSON files per hour, and serves status on 7788.
- B16. PROPOSAL: A clumsy-user review follows each phase: someone tries to break the shipped screens; each finding becomes a test or a recorded "won't fix".
- B17. PROPOSAL: `go mod tidy` runs in Phase 1 and CI fails if it leaves a diff.
