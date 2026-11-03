# Phase 06: mvp1-gate

risk: low (a verification gate; the only file written is a progress record, no source edits)

## Goal

Prove the whole branch, with every packet of phases 01 to 05 and 07 merged, passes the full main test suite on a freshly built binary, that the verification outputs of WP-45 and WP-59 agree, and that the Phase 07 acceptance record has no failed automated row (its manual rows may stay PENDING), before MVP 1 is handed to the owner (plan.md section 10). The gate fixes nothing: any failure is recorded and the phase stops.

## Packets

| Packet | Title |
|---|---|
| WP-58 | Main-suite gate for MVP 1 |

WP-58 depends on all other packets of the plan (the split ids of plan.md §3, for example WP-16a/16b, WP-17a/17b/17c and WP-62a/62b/62c, and the 32 packets of phase 07 background-run; the packet lists them). Phase 07 runs before this gate (plan.md §5). Waves and dependencies live only in plan.md §3 and in each packet header (`wave:`) and `## Depends on`; check-plan.ps1 fails if a phase.md table carries them.

## Phase proof (repo root, PowerShell; full recipe in WP-58)

- `cd web; npx vite build; npm test; npm run check` -> build ok, `# fail 0` and `# skipped 0`, 0 errors.
- Go (toolchain env as set by `build.ps1`, but do not run `build.ps1`: it rewrites `cmd/unravis-setup/payload`): `go build ./...`, `go vet ./...`, `go test ./... -count=1` -> all `ok`.
- `.unravis.db`: `git ls-files -- "*.unravis.db"` prints nothing; `Get-ChildItem -Recurse -Force -Filter *.unravis.db` outside `node_modules` and `.forge` prints nothing; `git diff master...HEAD -- '*.go' '*.ts' '*.svelte' | Select-String 'unravis\.db'` prints nothing.
- Build the branch exe `.dev\run\scn\unravis-gate.exe` from `./cmd/audit-explorer`, then `cd tools/e2e; powershell -File run-e2e.ps1 -Exe ..\..\.dev\run\scn\unravis-gate.exe` -> exit 0; `report/results.json` stats `unexpected` 0, `flaky` 0, `skipped` 0; `known-defects.json` still holds only DOC-02 and KEEP-09.
- Archive: `playwright test --list -c playwright.archive.config.ts` lists 80 tests. A pass of the archive run is NOT required (plan.md §10.4: the guided entry is an open owner decision).
- WP-45 and WP-59 outputs agree: the `TestVerifyNotes` and `TestQuerystoreMicrosecondsToMilliseconds` tests pass and the `VF-01h:` comment in `querystore.go` equals the `Vf01h` note.

## Additions to plan.md section 7

- Browser (final acceptance walk, in-app browser, seeded demo case, 1280 px and 375 px, light and dark): walk the Investigation Timeline screen against wireframes 08, 08a, 09 and 13 and write a differences table (wireframe element, seen in app, same or different, note) into `progress.md`. Differences are recorded, not fixed here. Accepted deviations (phase 04): comparison-period rows, 4xx/5xx splits, HTTP.sys and blocking panels.
- Security canaries: the `.unravis.db` check above; no external host in the network panel while the walk runs; the privacy canaries still in the main suite (for example UPD-07a) pass.
- Performance: no hard timing assertions anywhere in the gate (INV-5); record wall time of each command in `progress.md` as information only.
- Clumsy-user review: not repeated; it was done per phase. The gate records any leftover from the phase progress files.

## Commit (one commit, after the owner has seen the record; never push)

- `.forge/investigation-timeline/phases/06-mvp1-gate/progress.md`
