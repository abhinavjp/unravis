# Decisions: blackbox-wizard-popup

## Settled (owner, 2026-11-16; do not reopen)

| ID | Decision |
|---|---|
| S-01 | Variant A: the rail sits on top, inside a native `<dialog>` opened with `showModal()`. Variants B and C are out. |
| S-02 | Steps in order: Name, Where, Record, SQL (only when SQL applies: 8 steps, else 7), Watch, Keep, Folder, Review. |
| S-03 | Place comes first. Remove and Status come later (BL-131). |
| S-04 | Connected progress dots. Reached steps can be clicked; later steps are locked. Progress is honest (step X of N). |
| S-05 | Enter does Next. Esc asks before leaving. Nothing is saved until the last step. |
| S-06 | Targets are 40-44 px. Segmented controls for 2-5 options. Radio cards for either/or choices. Checkboxes, not switches. |
| S-07 | Presets (Recommended, etc.) are worked out from the ticks. Any change moves the highlight to Custom. There is a "Use defaults, go to review" shortcut. |
| S-08 | Record step: every group opens to its real figures, each with a Details disclosure (what it is, why it matters, what to look for, what is stored). The catalogue comes only from `groups_types.go`, `sqlhost.go` and `internal/bbsql`. No VM (BL-113 is not built). |
| S-09 | "Watch closely" is explained with the electricity meter analogy. |
| S-10 | Watch step rules (BL-135): kind `appPool` or `service`, plus a name; at most 32. Name is 1-128 chars, has none of `\ / : * ? " < > |`, has no control characters, and the same kind and name may not repeat (case-insensitive). This PC lists from `GET /api/blackbox/this-pc/targets`: app pools come from applicationHost.config (state unknown), services are running ones only. The pack path types names with a kind picker. Copy is honest about targets that are not running, unavailable or ambiguous. |
| S-11 | Folder step: drive cards. The default picks a visible fixed non-Windows drive; C: only with an explicit tick. One editable full path is always visible, checked as `X:\...` or UNC. Typing a path moves the drive card. There are notices for C:, unlisted, USB and network drives, and a "Reset to <default>" link. Review and the result show the path read-only with a Change link. |
| S-12 | Keep step: only days, size and free space. |
| S-13 | SQL step: the 13 SQL host counters with tick each, select all and clear. **Blocked; see B-01.** |
| S-14 | Beginner-friendly copy everywhere. Every step has Details or Help. |
| S-15 | Honest "Real today vs proposed": always-on machine counters, 3 record switches, targets (≤ 32), the 13 SQL host counters as one group, other host groups automatic. |
| S-16 | Not buildable now, and not planned: per-figure on/off backend (BL-133 backend), choosing the 7 DMV checks one by one, the certificate path, a saved SQL connection or connection string, a named instance or another SQL server (BL-134), VM figures (BL-113), anything needing a new collector or agent. |

## Blockers and conflicts (both sides shown; recommended default marked)

### B-01: the SQL step has nothing to save
- Settled side: S-13 wants tick each / select all / clear for the 13 counters, and the step shows "only when SQL Server is ticked".
- Code side: `Config` has no SQL field and `DisallowUnknownFields` is on. `sqlHost` is an optional, automatic group: it records the 13 counters whenever a SQL Server instance is found on the machine. There is no "SQL Server" tick to hang the step on. BACKLOG BL-133 itself says the per-figure switch "needs backend (deferred, owner go first)".
- Ticks that change nothing would be a demo-only UI, which the project CLAUDE.md forbids.
- **Recommended default:** an information step. It lists the 13 counters read-only, each with Details, under the line "Recorded automatically when SQL Server runs on this machine". No ticks, no select all, no clear. The step shows when the `this-pc` targets scan sees a SQL Server service (`MSSQLSERVER` or `MSSQL$*`) in the running services list. On the pack path it always shows, as "if the server has SQL Server". Ticks come with BL-133 when the owner gives the go.
- Alternative: drop the step (always 7 steps) and keep the 13 counters as one read-only group in Record.
- Owner call needed. TSK-11 builds the recommended default and is the only packet that changes if the owner picks the alternative.

### B-02: deeper checks cannot be switched on
- Wireframe: a "Deeper checks" off/on radio with a GRANT script on the SQL step.
- Code: `Validate` refuses `Probe.Enabled == true` ("the SQL probe is not part of this release").
- **Recommended default:** not built. At most one read-only line in the SQL step's Details: "Deeper checks are not part of this release." BL-134 stays deferred.

### B-03: per-figure ticks in Record (BL-133)
- Wireframe: ticks per figure in every group, and presets that compare those ticks.
- Code: only 3 real booleans (`record.processes`, `record.logsCatalogue`, `record.copyLogsOnSnapshot`) plus `intervalSeconds`.
- **Recommended default:** every other figure is shown read-only as "always recorded", with Details. Presets are worked out from the 3 booleans only:
  - Recommended = processes on, logs catalogue on, copy logs off (this matches `defaultSettings()`).
  - Light = all three off.
  - Custom = anything else.
- Open question for the owner: is "Light" worth showing? Default: show it. It is real, since it records less.

### B-04: UNC folders
- Settled S-11: accept UNC.
- Client: `settingsProblem` refuses anything that is not `X:\`. Server: allows UNC except a share root (`unsafeFolder`).
- Not verified: whether the recorder's service account can write to a network share.
- **Recommended default:** allow UNC on the client, matching the server (TSK-03), and show a network notice: "The Blackbox service must be able to write here. A local drive is safer."

### B-05: drive cards need a drives list
- Settled S-11 needs drive letter, label, type and free/total space for this PC.
- Code: no route returns drives. `ThisPc` has no drives.
- **Recommended default:** add a small read-only `GET /api/blackbox/this-pc/drives` (TSK-04). It reuses GetDriveType and GetDiskFreeSpaceEx and returns letter, label, type (fixed / removable / network / other), system (Windows drive), free and total bytes. It is not a collector and not an agent; it only reads the local machine at request time. On the pack path there are no drive cards; the path is typed only.
- Owner should confirm this does not count as "deferred backend". If it does, the Folder step falls back to the typed path plus the existing `D:` / `C:` default, and TSK-04 and TSK-05 are dropped.

### B-06: the name hint in the wireframe is wrong
- Wireframe hint: "Letters, numbers, spaces and dashes are fine".
- Code: `^[A-Za-z0-9][A-Za-z0-9-]*$`, ≤ 63 chars. No spaces.
- **Decision:** the code wins. The copy says "Letters, numbers and dashes. No spaces. Start with a letter or number."

### B-07: logPaths has no home in the wireframe
- Code: `logPaths` is a real, editable field (≤ 20 absolute paths, default `C:\inetpub\logs\LogFiles`). The current wizard edits it.
- Wireframe: no field. Its Details text says "edit the settings file".
- **Recommended default:** an editable "Log folders" list inside the Record step's "IIS log catalogue" group, shown only when the logs catalogue box is ticked. This keeps the step where the meaning lives. Alternative: put it under Folder → Advanced.

### B-08: keep-days range
- Wireframe: 7 / 30 / 90 segmented control, with "custom" shown only as text. The maxGB stepper starts at 1.
- Code: keep 1-365 whole days; maxGB 0.1-10000; minFree 1-50.
- **Decision:** the segmented control is 7 / 30 / 90 / Other. Other opens a number entry from 1 to 365. The maxGB input allows 0.1.

### B-09: where the C: confirm sits
- Wireframe spec text says the C: confirm is "on Keep". The wireframe code puts it on Folder.
- **Decision:** Folder, as in the code and S-11.

### B-10: BlackboxSettings is shared with Apply
- `BlackboxSettings.svelte` is used by the drawer wizard and by "Apply settings on this PC".
- **Decision:** extract the target picker into `BlackboxTargetPicker.svelte` (TSK-12). Both Apply and the new Watch step use it. Apply must keep working and must look the same.

### B-11: gating for the pack path
- Keep the real wizard's rules: "This PC" only when `thisPc.available && !thisPc.installed`; "Pack" only when `canPack`.

## Open decisions (recommended defaults)

| ID | Question | Recommended default |
|---|---|---|
| OD-1 | How are the always-recorded figures shown? | A read-only list with Details. Real checkboxes only for the 3 record booleans. The 13 SQL counters get ticks only once the backend can store them; verified that it cannot today (B-01). |
| OD-2 | Remove and Status in the pop-up (BL-131)? | A later phase, after Place is live-verified. Not in this plan. The dialog shell is built so it can be reused. |
| OD-3 | Which branch? | New branch `feat/blackbox-wizard-popup`. Best: branch from `master` once 702e6c2f (setup fix) is merged, so the PR holds only this work. If 702e6c2f is not merged when work starts, branch from the current HEAD of `fix/setup-place-blackbox-beside-app`. That avoids a `web/dist` conflict now, but the PR will show 702e6c2f until that PR merges; then rebase onto master. Do not build on master directly (it is protected). The uncommitted `web/dist` and `docs/BACKLOG.md` changes must be committed or parked first. |
| OD-4 | SQL step: info step or drop it? | Info step, shown when SQL Server is seen (B-01). |
| OD-5 | Drives endpoint allowed? | Yes, read-only and this PC only (B-05). |
| OD-6 | Show the "Light" preset? | Yes (B-03). |
| OD-7 | Where do log folders live? | Record → IIS log catalogue group (B-07). |
| OD-8 | Port and bind? | Keep them in Folder → Advanced, closed by default, with the existing `0.0.0.0` warning (as in the wireframe). |
| OD-9 | Old drawer wizard? | Delete `BlackboxWizard.svelte` once the pop-up is mounted. No feature flag: one Place path only. |
