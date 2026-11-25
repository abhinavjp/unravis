# RC cadence: one rc every 2 hours when develop changed (25 Nov 2026)

Owner rule (nachi, 25 Nov 2026): "Try to release a new version every 2 hours if changes are there." The rc is not installed by hand: the app auto-updates itself.

Related: `docs/ci/RELEASE-TRAIN.md` (promotion, rollback), `docs/auto-updater/RELEASE-COORDINATION.md` (release lock), `docs/auto-updater/CONTROL.md` (pause or withdraw a release).

## The slot

A scheduled task fires every 2 hours. At each slot, in this order. The first "no" ends the slot.

1. **Changed?** Does `develop` hold merged commits that are not in the last rc tag? No: skip, say nothing.
2. **Green?** Is the newest push run of `ci.yml` on `develop` green, including `build` (the full CI that master requires)? No: do "Red build" below, skip the slot.
3. **Lock free?** Read `lock.json` on branch `release-lock`. 404 or `free`: go. `held` with `holdMerges: true`: wait 2 minutes, read again, up to 30 minutes; still held: skip, try next slot.
4. **Promote and release (on the PC, DESKTOP-QQDVSD1):** `pwsh tools/release/Promote-Develop.ps1`, then `pwsh tools/release/Release.ps1 -Channel rc -Yes -Requester <thread> -WaitMinutes 30`. Never hand-roll a release, never write the lock branch.
5. **No install.** Do not install on nachi's PC. Check `Release.ps1 -Status` says the rc is published, then report once: rc number plus a short list of what it carries. Batch reports: never more than one message per slot.

Stop only for: the signing passphrase (Proton Pass), Windows UAC, secrets, deleting user data.

## What can go wrong, and what to do

| Case | What to do |
|---|---|
| `build` red on develop | Skip the slot. Find the red job on the newest `ci.yml` run, fix forward through a normal pull request into `develop` (or revert the bad commit by pull request). Never skip or disable a test. If only older commits are green: `Promote-Develop.ps1 -UpTo <sha>` promotes up to the last green commit. Windows-only failures hide behind the Linux fast gate: look at the Windows jobs first. |
| A pull request merges into develop during the release | Fine. Promotion takes one fixed head and merges only that; the new commit goes in the next slot. Do not re-run promotion to "include" it. |
| Release lock held by another thread | Wait and re-read (step 3). Never write the lock. A stuck run: `Release.ps1 -Status` says why; the next run on that PC takes over a crashed one. |
| PC asleep or off at the slot | The promote and release run on the PC, so the slot cannot run. Do not fake it. Next time the PC is awake, run one catch-up slot. One rc covers everything missed: never two rcs to "make up" for a missed slot. If the lock or `-Status` shows a release in progress or just finished, do nothing. |
| GitHub Actions or runner outage | Check githubstatus. If runs are queued or failing for GitHub's own reason, skip the slot, retry at the next slot. A job gets one re-run at most; a repeat failure is a real failure. |
| Flaky or Windows-only test | One re-run only. If it fails again, it is a defect: fix the test or the code (timing waits, platform file modes), record it in `docs/BACKLOG.md`. Never mark it skipped to get green. |
| Promotion conflict or unresolved review conversations on master | Master needs every review conversation resolved. Fix the valid findings (Codex threads), answer the invalid ones with what you traced, resolve them. Never admin-merge. A merge conflict: the script merges master into the promote branch; if it still conflicts, fix by hand on `promote/<sha7>`, never force-push, never squash a `promote/*` pull request. |
| No changes since the last rc | Skip silently. |
| Bad rc (found after publishing) | Pause or withdraw it with the control file (`tools/release/Set-Control.ps1`, see `docs/auto-updater/CONTROL.md`): withdrawn versions are never offered, a paused channel offers nothing. A machine that already installed it stays on it until the next rc, so fix forward and cut the next rc at once (outside the 2-hour grid if the bug is serious). Do not delete tags or releases by hand. |
| Local CI cutover changes which check gates master | Read `docs/ci/CUTOVER.md`. Until phase 2 is done by nachi, master still requires GitHub's `build`. Do not change master's required check yourself. After phase 2, step 2 above reads the check that master now requires, and the slot still waits for that exact check. |
| Signing passphrase, UAC or PC permission prompt | Stop that one step and tell nachi which step. Everything else keeps going. |
| Store or data format changes in the rc | List them in the report (for example the MCP grants store v5 is one-way: an older binary refuses it). Rollback of such an rc needs the warn-or-restore step in the backlog (BL-353). |

## Report format

One message per rc, plain words:

- `rc.N published` (or `slot skipped: <reason>` only when the skip needs nachi)
- what it carries, 3 to 8 lines from the merged pull request titles
- owed live checks (Windows, UAC, real hosts) that this rc does not prove

## Owner changes

Change the cadence by editing the scheduled task "Unravis rc every 2 hours" (cron `0 */2 * * *`, UTC). Pause by disabling it.
