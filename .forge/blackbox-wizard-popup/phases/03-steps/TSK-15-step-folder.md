# TSK-15 Folder step
Risk: medium (wrong default could fill the Windows drive). Worker size: medium. Reason: drive cards, path sync and several notices.

## Outcome
`StepFolder.svelte`: drive cards (this PC), one always-visible full path, notices (C:, unlisted, USB, network/UNC), "Reset to <default>", the C: tick, Advanced (port, bind, with the existing 0.0.0.0 warning).

## Write scope
- `web/src/components/blackboxWizard/StepFolder.svelte` (new)

## Must not change
`blackboxDrives.ts`, `blackboxWizard.ts`.

## Changes
- pc path: `fetchDrives()` once; on failure show the typed path only, with a short note.
- pack path: no cards; typed path only, default `D:\UnravisBlackbox`.
- Typing a path moves the selected card via `driveOfPath`; picking a card rewrites the drive letter and keeps the rest of the path.
- `folderTouched` stops the auto default once the owner chooses.

## Proof
`cd web && npm run check`. Default and sync logic are tested in TSK-01.

## Depends on
TSK-01, TSK-02, TSK-05, TSK-08.
