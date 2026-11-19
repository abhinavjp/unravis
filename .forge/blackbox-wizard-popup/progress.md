# Progress: blackbox-wizard-popup

## 2026-11-16 wave 1 applied (uncommitted, branch feat/blackbox-wizard-popup)
- Applied and green (web: 3325 tests, svelte-check 0 errors): TSK-02, 03, 05, 06, 07, 08, 12. TSK-04 (Go) applied but NOT compiled or tested.
- Workers: hub brainpayroll (kimi-k3-1-pro: 02, 04; glm-5-3: 03, 05; terra: 07, 08), native sonnet (06, 12). sslip hub unusable: Gumloop "Insufficient credits".
- Deviations: copy lines in blackboxCatalogue.ts / blackboxWizardCopy.ts reworded by the orchestrator to pass the grade 8.5 lint. WizardDialog shadow now uses var(--scrim) (tokens test). TSK-04: network drives skip label and space reads (a dead share can block the request).
- TSK-06 added a `labels` prop and `data-own-enter` rule for the wizard; TSK-17 must pass them.
- Not verified: no browser run of any component yet; Go code has no toolchain on this PC or on the hub (probe job 86392). Needs: owner go to install Go (or CI after owner pushes).

## 2026-11-16 wave 3 status
- Go 1.27.4 installed (winget, owner approved); TSK-04 drives route verified: go vet clean, tests pass, real call returns C: and D:.
- TSK-01 deviations to handle in TSK-17: `systemDrive` field on WizardState; `stepProblem` codes include `folder-system-drive` (also on pack).
- Delivered and green (check 0 errors, npm test 3357 pass): TSK-13 StepWatch, TSK-15 StepFolder (props `wizard`, `attempted`), TSK-16 StepReview + ResultView, TSK-10 StepRecord (kimi), TSK-11 StepSql (terra). Prop for state is `wizard` everywhere (`state` clashes with `$state`).
- In flight: TSK-09 StepName+StepWhere (glm job 86416), TSK-14 StepKeep (kimi job 86424).
- TSK-17 must move every step's MISSING_COPY into blackboxWizardCopy.ts, render step titles/lead/help in the dialog header, and keep BB_TEXT keys used by ResultView when the old wizard is deleted.
- Nothing rendered in a browser yet. Nothing committed.

## 2026-11-17 review and browser pass
- TSK-17..19 done; web/dist rebuilt. TSK-20 merge-sentinel: no-ship until findings 1-2; findings 1-5 fixed (dup log paths, Where change from Review, system-drive letter in copy, lost-contact reload, maxSeen remap). Finding 6 skipped (server catches it).
- TSK-21 browser pass (live machine, helper beside app): 21 items; fixed A-J (Enter on Review needs the button, 375px close/rail overlap, 32-target cap on tick, invalid port opens Advanced, bad path chars and slashes, scan-failed notice, name hints, first-field focus, Keep defaults, copy). Skipped: service display names (backend), duplicate targets fetch, chips, port-80 warning, missing-drive check.
- Checks: svelte-check 0/0, npm test 3391/3391, go vet and go test ok. 375px: rail ends 315, close starts 323, no overlap (7-step path).
- Not verified: Place/Build click (forbidden), SQL step with a real SQL service, nested dialog inside drawer with real focus, install on UNC/missing drive. Owner live run (TSK-22) pending; BACKLOG BL-130..135 not ticked.

## 2026-11-17 live-app pass (installer v0.1.0-rc.45-107-bbwizard2)
- Owner reported "still broken"; I walked every step at 1440, 768 and 375 widths (light and dark), keyboard focus trap, Esc/leave. Fixed: drawer CSS leaking into the pop-up (figures grid, picker rows, details/summary), tiny picker rows, rail overflow and connector overlap on phones, folder Change row, duplicate SQL-step sentence, mobile footer height, Where scan without admin (read-only SCM state).
- Real bug found by building a pack: the 8 MB cap was applied to the raw 15 MB program, so Build the pack always failed. Now MaxPackBytes 16 MB for the zip, MaxProgramBytes 64 MB for the program; the refusal shows the real reason. Go tests cover a shipped-size program.
- Installed (silent upgrade, cases kept) and verified in the INSTALLED app: Name, Where (real drives C: and D:), Record, SQL (pack path), Watch, Keep, Folder, Review, Build the pack gives "Pack built" with correct steps.
- Checks: svelte-check 0/0, npm test 3391/3391, go vet clean, go test ./internal/... ok.
- Not verified: This PC install (needs UAC, owner side), failed-install result view, real SQL service. BL-130..135 not ticked.

## 2026-11-19 BL-137 and BL-131
- BL-137: USB and network drive cards greyed out with the reason; Next refused for a UNC path and for a USB or network letter of this PC (`folder-not-local`, `nonLocalDrives` on WizardState).
- BL-131: Status, Remove, Apply and Snapshot moved from the drawer into WizardDialog over the list. Remove reports busy through `onBusy`.
- e2e `tools/e2e/specs/blackbox.spec.ts` updated for the pop-up (it still targeted the old drawer wizard and two old copy strings) and BB-31, BB-31a, BB-32 added; 13/13 pass against a Linux demo build (cases off).
- Not verified: real remove/apply with the service installed; a real USB stick or mapped drive on Windows.
