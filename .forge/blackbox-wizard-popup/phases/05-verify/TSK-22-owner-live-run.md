# TSK-22 Owner live-data run and BACKLOG update
Risk: high (only real servers can prove it). Worker size: small (agent part). Reason: the owner runs it; the agent only builds, installs locally and updates the BACKLOG after.

## Outcome
The owner has used the pop-up on real machines. BACKLOG items are ticked only for what worked live.

## Write scope
- `docs/BACKLOG.md`
- `.forge/blackbox-wizard-popup/progress.md`

## Must not change
Source code (live findings become new work items).

## Changes
Agent: build and install locally (no tag, no release, no release CI), hand the owner this checklist:
1. Place on this PC on a real server: service created, `blackbox.json` in the chosen folder, read key sealed, entry in the list.
2. Watch list on a real IIS box: app pools listed with "state unknown", running services listed, a typed stopped service shows the honest note; install records the targets.
3. Build a pack, install it on another server, check its settings match Review.
4. Default folder on a box with D:, without D: (C: needs the tick), with USB and a mapped drive (notices shown, not picked).
5. A UNC folder: does the service write there (not verified by anyone yet).
6. SQL step shown on a box with SQL Server, hidden on one without.
7. Apply settings on this PC still works (target add/remove, save).
A perfmon comparison is not needed.

Then tick BL-130, BL-132 and BL-135 only for the parts that passed live. BL-133 stays open (backend deferred). Add any new items found.

## Proof
Owner's own words in chat for each checklist line, recorded in `progress.md`.

## Depends on
TSK-21.
