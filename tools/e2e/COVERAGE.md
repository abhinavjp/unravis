# Scenario coverage

This file says which catalogue scenarios the end-to-end suite runs, and what is left to a person.
The catalogue is in `.dev/scenarios/groups` (g01 to g12). Status words:

- covered: a spec runs it and it passes.
- expected-fail: a spec asserts the EXPECTED behaviour; the app does not do it yet, so the test is marked as an expected failure with the defect id. When the defect is fixed the test starts to pass, Playwright reports "expected to fail, but passed", and the id must be removed from `known-defects.json`.
- manual-only: not automatable here, with the reason.
- not yet automated: reachable in the demo but no spec written yet.

Totals: 179 covered, 2 expected-fail, 54 manual-only, 0 not yet automated, 235 scenarios.
Extra ids with no catalogue entry (IMP-01 to IMP-04, the file import fixtures) are in `specs/imports.spec.ts`.
A scenario id with a letter after it (for example SRC-07a) is an extra check that belongs to the same scenario; it has its own row under the scenario and is not counted in the totals. A "covered (...)" status names the part of the scenario that cannot be run in the demo; the reasons are in `.dev/reports/e2e-ui-2.md`.

## How to run

From `tools/e2e` (Playwright comes from the `node_modules` junction to `tools/screens`; do not run npm install):

```
powershell -File run-e2e.ps1                      # everything
powershell -File run-e2e.ps1 -Only CASE-10,SEC-08 # chosen ids (an id with a letter, such as SRC-07a, is its own id)
powershell -File run-e2e.ps1 -Exe D:/path/unravis-demo.exe
powershell -File run-e2e.ps1 -Headed
npm run e2e -- -Only HELP-03                      # the same through npm
```

The exe is `UNRAVIS_EXE` if set, else the prebuilt demo named in `support/launcher.ts`. Each spec file starts its own demo
on port 0 with a new empty data folder and a fixed clock, and stops it at the end. The JSON report is `report/results.json`
(git-ignored). Retries are off, so a flaky test shows as a failure.

## How to add a scenario

1. Pick the spec file for its group, or add `specs/<name>.spec.ts`.
2. Name the test `<ID>: what is expected`. The id must start the title; the suite reads it.
3. Import `test` and `expect` from `../support/fixtures`. Use `api(method, path, body, state)` for calls and `page` for the screen. No sleeps: use web-first assertions or `expect.poll`.
4. Assert the EXPECTED behaviour from the scenario, not what the app does today.
5. If the app does not do it: reproduce the failure by hand, then add `"<ID>": "<defect id>"` to `known-defects.json`.
6. An error you provoke on purpose is allowed for that test with `allow(/pattern/, 'defect id')`. The general list is `support/allow-list.ts`.
7. A demo is shared by all tests in one file, so a test that fills a limit (such as 20 connections) goes in its own file.

## Table

| Scenario | Spec file | Status |
| --- | --- | --- |
| CASE-01 | case-ui.spec.ts | covered |
| CASE-02 | case-ui.spec.ts | covered |
| CASE-03 | case-ui.spec.ts | covered |
| CASE-04 | case-ui.spec.ts | covered |
| CASE-05 | case-ui.spec.ts | covered |
| CASE-06 | case-wizard.spec.ts | covered |
| CASE-06a | case-wizard.spec.ts | covered |
| CASE-06b | case-wizard.spec.ts | covered |
| CASE-06c | case-wizard.spec.ts | covered |
| CASE-06d | case-wizard.spec.ts | covered |
| CASE-06e | case-wizard.spec.ts | covered |
| CASE-06f | case-wizard.spec.ts | covered |
| CASE-06g | case-wizard.spec.ts | covered |
| CASE-06h | case-wizard.spec.ts | covered |
| CASE-06i | case-wizard.spec.ts | covered |
| CASE-07 | case-api.spec.ts | covered |
| CASE-07b | case-api.spec.ts | covered |
| CASE-08 | case-ui.spec.ts | covered |
| CASE-09 | case-ui.spec.ts | covered |
| CASE-10 | case-api.spec.ts, security.spec.ts | covered |
| CASE-10a | case-api.spec.ts | covered |
| CASE-10b | case-api.spec.ts | covered |
| CASE-10c | security.spec.ts | covered |
| CASE-11 | case-retries.spec.ts | covered |
| CASE-11a | case-retries.spec.ts | covered |
| CASE-11b | case-retries.spec.ts | covered |
| CASE-11c | case-retries.spec.ts | covered |
| CASE-11d | case-retries.spec.ts | covered |
| CASE-11e | case-retries.spec.ts | covered |
| CASE-12 | case-api.spec.ts | covered |
| CASE-13 | case-api.spec.ts | covered |
| CASE-14 | - | manual-only: a Go test scenario, no path through the demo (the Go tests cover it) |
| CASE-15 | case-api.spec.ts | covered |
| CASE-16 | case-drawers.spec.ts | covered |
| CASE-17 | case-api.spec.ts | covered |
| CASE-18 | case-api.spec.ts, case-drawers.spec.ts | covered |
| CASE-18a | case-api.spec.ts, case-drawers.spec.ts | covered |
| CASE-19 | - | manual-only: needs a master key and a real archive round trip |
| CASE-20 | case-api.spec.ts | covered |
| CASE-21 | case-drawers.spec.ts | covered |
| CASE-21a | case-drawers.spec.ts | covered |
| CASE-22 | case-api.spec.ts | covered |
| CASE-23 | case-start.spec.ts | covered (run A; run B needs a real store failure) |
| CASE-24 | case-start.spec.ts | covered (step 1 only: steps 2 to 4 need a master key) |
| CASE-24a | case-start.spec.ts | covered |
| CASE-24b | case-start.spec.ts | covered |
| CASE-25 | - | manual-only: needs old per-user case folders on a real profile |
| KEY-01 | keys.spec.ts | covered |
| KEY-02 | keys.spec.ts | covered |
| KEY-03 | keys.spec.ts | covered |
| KEY-04 | keys.spec.ts | covered |
| KEY-05 | keys.spec.ts | covered |
| KEY-06 | keys-ui.spec.ts | covered (break-it c needs the real key keeper) |
| KEY-06a | keys-ui.spec.ts | covered |
| KEY-06b | keys-ui.spec.ts | covered |
| KEY-07 | keys-ui.spec.ts | covered (break-it a and b need a master key) |
| KEY-07a | keys-ui.spec.ts | covered |
| KEY-08 | keys-ui.spec.ts | covered (the Erasing waiting text is too short to see) |
| KEY-08a | keys-ui.spec.ts | covered |
| KEY-09 | - | manual-only: needs the PC master key |
| KEY-10 | keys-ui.spec.ts | covered |
| SRC-01 | sources.spec.ts | covered |
| SRC-02 | sources.spec.ts | covered |
| SRC-03 | sources-ui.spec.ts | covered |
| SRC-04 | sources.spec.ts | covered |
| SRC-04a | sources.spec.ts | covered |
| SRC-05 | sources-ui.spec.ts | covered |
| SRC-06 | sources-ui.spec.ts | covered |
| SRC-06a | sources-ui.spec.ts | covered |
| SRC-07 | sources.spec.ts | covered |
| SRC-07a | sources.spec.ts | covered |
| SRC-07b | sources.spec.ts | covered |
| SRC-07c | sources.spec.ts | covered |
| SRC-08 | sources-ui.spec.ts | covered |
| SRC-09 | sources-limit.spec.ts | covered |
| SRC-10 | sources-ui.spec.ts | covered |
| SRC-11 | sources-ui.spec.ts | covered (Not now then reload is KEEP-04) |
| SRC-11a | sources-ui.spec.ts | covered |
| SRC-11b | sources-ui.spec.ts | covered |
| SRC-12 | - | manual-only: needs a real SQL Server |
| SRC-13 | - | manual-only: needs a real SQL Server |
| SRC-14 | - | manual-only: needs a real MongoDB |
| INV-01 | investigate.spec.ts | covered |
| INV-02 | investigate.spec.ts | covered |
| INV-03 | investigate.spec.ts | covered |
| INV-04 | investigate.spec.ts | covered |
| INV-05 | investigate.spec.ts | covered |
| INV-06 | investigate.spec.ts | covered |
| INV-07 | investigate.spec.ts | covered |
| INV-08 | investigate.spec.ts | covered |
| INV-08a | investigate.spec.ts | covered |
| INV-09 | investigate.spec.ts | covered |
| INV-10 | explorer.spec.ts | covered |
| INV-10a | explorer.spec.ts | covered |
| INV-11 | explorer.spec.ts | covered |
| INV-11a | explorer.spec.ts | covered |
| INV-11b | explorer.spec.ts | covered |
| INV-12 | investigate.spec.ts | covered |
| INV-12a | investigate.spec.ts | covered |
| INV-13 | plan.spec.ts | covered |
| INV-13a | plan.spec.ts | covered |
| INV-13b | plan.spec.ts | covered |
| INV-13c | plan.spec.ts | covered |
| INV-14 | - | manual-only: a Go test scenario, no path through the demo (the Go tests cover it) |
| INV-15 | investigate.spec.ts | covered |
| INV-16 | investigate.spec.ts | covered (a ninth tick cannot be made in the demo) |
| MCP-01 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-02 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-03 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-04 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-05 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-06 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-07 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-08 | - | manual-only: needs the MCP bridge process, not started by the demo |
| GUIDE-01 | guide-steps.spec.ts | covered |
| GUIDE-01a | guide-steps.spec.ts | covered |
| GUIDE-01b | guide-steps.spec.ts | covered |
| GUIDE-02 | guided.spec.ts | covered |
| GUIDE-03 | guide-steps.spec.ts | covered |
| GUIDE-03a | guide-steps.spec.ts | covered |
| GUIDE-03b | guide-steps.spec.ts | covered |
| GUIDE-04 | guided.spec.ts | covered |
| GUIDE-05 | guide-steps.spec.ts | covered |
| GUIDE-05a | guide-steps.spec.ts | covered |
| GUIDE-05b | guide-steps.spec.ts | covered |
| GUIDE-06 | guide-steps.spec.ts | covered |
| GUIDE-06a | guide-steps.spec.ts | covered |
| GUIDE-07 | guide-steps.spec.ts | covered |
| GUIDE-08 | guide-steps.spec.ts | covered |
| GUIDE-09 | guide-steps.spec.ts | covered |
| GUIDE-09a | guide-steps.spec.ts | covered |
| GUIDE-10 | guide-steps.spec.ts | covered |
| GUIDE-10a | guide-steps.spec.ts | covered |
| GUIDE-11 | guide-steps.spec.ts | covered |
| GUIDE-11a | guide-steps.spec.ts | covered |
| GUIDE-11b | guide-steps.spec.ts | covered |
| GUIDE-11c | guide-steps.spec.ts | covered |
| GUIDE-12 | - | manual-only: not reachable in the demo |
| GUIDE-13 | guide-checklist.spec.ts | covered |
| GUIDE-13a | guide-checklist.spec.ts | covered |
| GUIDE-13b | guide-checklist.spec.ts | covered |
| GUIDE-13c | guide-checklist.spec.ts | covered |
| GUIDE-13d | guide-checklist.spec.ts | covered |
| GUIDE-14 | guide-checklist.spec.ts | covered |
| GUIDE-14a | guide-checklist.spec.ts | covered |
| GUIDE-14b | guide-checklist.spec.ts | covered |
| GUIDE-14c | guide-checklist.spec.ts | covered |
| GUIDE-14d | guide-checklist.spec.ts | covered |
| GUIDE-15 | plan.spec.ts | covered |
| GUIDE-15a | plan.spec.ts | covered |
| GUIDE-15b | plan.spec.ts | covered |
| GUIDE-16 | guide-steps.spec.ts | covered |
| GUIDE-17 | guide-steps.spec.ts | covered |
| GUIDE-17a | guide-steps.spec.ts | covered |
| GUIDE-18 | guide-steps.spec.ts | covered |
| GUIDE-19 | guided.spec.ts | covered |
| GUIDE-19a | guided.spec.ts | covered |
| GUIDE-20 | - | manual-only: not reachable in the demo |
| GUIDE-21 | sources-steps.spec.ts | covered (the Out of date mark after adding data cannot be seen) |
| GUIDE-21a | sources-steps.spec.ts | covered |
| GUIDE-22 | sources-steps.spec.ts | covered |
| GUIDE-22a | sources-steps.spec.ts | covered |
| GUIDE-22b | sources-steps.spec.ts | covered |
| GUIDE-22c | sources-steps.spec.ts | covered |
| GUIDE-22d | sources-steps.spec.ts | covered |
| GUIDE-22e | sources-steps.spec.ts | covered |
| GUIDE-22f | sources-steps.spec.ts | covered |
| GUIDE-22g | sources-steps.spec.ts | covered |
| GUIDE-22h | sources-steps.spec.ts | covered |
| GUIDE-23 | sources-steps.spec.ts | covered (the Out of date mark on steps cannot be seen) |
| GUIDE-23a | sources-steps.spec.ts | covered |
| GUIDE-23b | sources-steps.spec.ts | covered |
| GUIDE-23c | sources-steps.spec.ts | covered |
| GUIDE-23d | sources-steps.spec.ts | covered |
| GUIDE-24 | sources-steps.spec.ts | covered |
| GUIDE-24a | sources-steps.spec.ts | covered |
| GUIDE-25 | - | manual-only: needs real data (the demo has no second day to compare) |
| MAN-01 | manual-tools.spec.ts | covered |
| MAN-01a | manual-tools.spec.ts | covered |
| MAN-01b | manual-tools.spec.ts | covered |
| MAN-01c | manual-tools.spec.ts | covered |
| MAN-02 | manual-tools.spec.ts | covered |
| MAN-03 | manual-tools.spec.ts | covered |
| MAN-03a | manual-tools.spec.ts | covered |
| MAN-04 | guided.spec.ts | covered |
| MAN-04a | guided.spec.ts | covered |
| MAN-05 | manual-tools.spec.ts | covered |
| MAN-05a | manual-tools.spec.ts | covered |
| MAN-05b | manual-tools.spec.ts | covered |
| MAN-05c | manual-tools.spec.ts | covered |
| MAN-06 | manual-tools.spec.ts | covered |
| MAN-06a | manual-tools.spec.ts | covered |
| MAN-06b | manual-tools.spec.ts | covered |
| MAN-07 | manual-tools.spec.ts | covered |
| MAN-07a | manual-tools.spec.ts | covered |
| MAN-07b | manual-tools.spec.ts | covered |
| MAN-08 | manual-tools.spec.ts | covered |
| MAN-08a | manual-tools.spec.ts | covered |
| MAN-08b | manual-tools.spec.ts | covered |
| MAN-08c | manual-tools.spec.ts | covered |
| MAN-09 | plan.spec.ts | covered |
| MAN-09a | plan.spec.ts | covered |
| MAN-09b | plan.spec.ts | covered |
| MAN-09c | plan.spec.ts | covered |
| MAN-10 | manual-tools.spec.ts | covered |
| MAN-10a | manual-tools.spec.ts | covered |
| MAN-10b | manual-tools.spec.ts | covered |
| EXP-01 | export-ui.spec.ts | covered |
| EXP-01a | export-ui.spec.ts | covered |
| EXP-01b | export-ui.spec.ts | covered |
| EXP-02 | export-ui.spec.ts | covered |
| EXP-02a | export-ui.spec.ts | covered |
| EXP-02b | export-ui.spec.ts | covered |
| EXP-03 | exports.spec.ts | covered |
| EXP-03a | exports.spec.ts | covered |
| EXP-03b | exports.spec.ts | covered |
| EXP-03c | exports.spec.ts | covered |
| EXP-04 | exports.spec.ts | covered |
| EXP-04a | exports.spec.ts | covered |
| EXP-05 | export-ui.spec.ts | covered |
| EXP-05a | export-ui.spec.ts | covered |
| EXP-06 | exports.spec.ts | covered |
| EXP-06a | exports.spec.ts | covered |
| EXP-07 | export-ui.spec.ts | covered |
| EXP-07a | export-ui.spec.ts | covered |
| EXP-07b | export-ui.spec.ts | covered |
| EXP-08 | exports.spec.ts | covered |
| EXP-08a | exports.spec.ts | covered |
| EXP-09 | exports.spec.ts | covered |
| EXP-09a | exports.spec.ts | covered |
| EXP-10 | export-ui.spec.ts | covered |
| EXP-10a | export-ui.spec.ts | covered |
| BB-01 | blackbox.spec.ts | covered |
| BB-01a | blackbox.spec.ts | covered |
| BB-02 | blackbox.spec.ts | covered |
| BB-02a | blackbox.spec.ts | covered |
| BB-03 | blackbox.spec.ts | covered |
| BB-04 | bb-add.spec.ts | covered |
| BB-04a | bb-add.spec.ts | covered |
| BB-04b | bb-add.spec.ts | covered |
| BB-05 | bb-add.spec.ts | covered |
| BB-05a | bb-add.spec.ts | covered |
| BB-05b | bb-add.spec.ts | covered |
| BB-06 | bb-add.spec.ts | covered |
| BB-06a | bb-add.spec.ts | covered |
| BB-07 | bb-add.spec.ts | covered |
| BB-08 | bb-add.spec.ts | covered |
| BB-09 | bb-add.spec.ts | covered |
| BB-09a | bb-add.spec.ts | covered |
| BB-09b | bb-add.spec.ts | covered |
| BB-09c | bb-add.spec.ts | covered |
| BB-10 | bb-change.spec.ts | covered |
| BB-10a | bb-change.spec.ts | covered |
| BB-11 | bb-change.spec.ts | covered |
| BB-11a | bb-change.spec.ts | covered |
| BB-11b | bb-change.spec.ts | covered |
| BB-11c | bb-change.spec.ts | covered |
| BB-11d | bb-change.spec.ts | covered |
| BB-12 | bb-change.spec.ts | covered |
| BB-12a | bb-change.spec.ts | covered |
| BB-12b | bb-change.spec.ts | covered |
| BB-12c | bb-change.spec.ts | covered |
| BB-13 | bb-change.spec.ts, blackbox.spec.ts | covered |
| BB-13a | bb-change.spec.ts | covered |
| BB-14 | bb-add.spec.ts | covered |
| BB-14a | bb-add.spec.ts | covered |
| BB-14b | bb-add.spec.ts | covered |
| BB-14c | bb-add.spec.ts | covered |
| BB-15 | blackbox.spec.ts | covered |
| BB-15a | blackbox.spec.ts | covered |
| BB-16 | bb-add.spec.ts | covered |
| BB-16a | bb-add.spec.ts | covered |
| BB-16b | bb-add.spec.ts | covered |
| BB-16c | bb-add.spec.ts | covered |
| BB-17 | bb-add.spec.ts | covered |
| BB-17a | bb-add.spec.ts | covered |
| BB-18 | bb-change.spec.ts | covered |
| BB-18a | bb-change.spec.ts | covered |
| BB-19 | blackbox.spec.ts | covered |
| BB-19a | blackbox.spec.ts | covered |
| BB-20 | bb-add.spec.ts, bb-change.spec.ts | covered |
| BB-20a | bb-change.spec.ts | covered |
| BB-21 | - | manual-only: not reachable in the demo |
| BB-22 | - | manual-only: needs a real Blackbox service |
| BB-23 | - | manual-only: needs a real Blackbox service |
| KEEP-01 | keeper.spec.ts | covered |
| KEEP-01a | keeper.spec.ts | covered |
| KEEP-02 | keeper.spec.ts | covered |
| KEEP-02a | keeper.spec.ts | covered |
| KEEP-03 | keeper-update.spec.ts | covered |
| KEEP-03a | keeper-update.spec.ts | covered |
| KEEP-04 | keeper.spec.ts | covered |
| KEEP-05 | keeper.spec.ts | covered |
| KEEP-06 | keeper.spec.ts | covered |
| KEEP-07 | keeper-update.spec.ts | covered |
| KEEP-08 | keeper-update.spec.ts | covered |
| KEEP-08a | keeper-update.spec.ts | covered |
| KEEP-08b | keeper-update.spec.ts | covered |
| KEEP-09 | keeper.spec.ts | expected-fail (KEEP-09 D-C-01) |
| KEEP-10 | - | manual-only: needs the Windows key keeper service |
| KEEP-11 | - | manual-only: needs the Windows key keeper service |
| KEEP-12 | - | manual-only: a Go test scenario, no path through the demo (the Go tests cover it) |
| KEEP-13 | - | manual-only: a Go test scenario, no path through the demo (the Go tests cover it) |
| KEEP-14 | - | manual-only: needs the Windows key keeper service |
| KEEP-15 | keeper-update.spec.ts | covered |
| KEEP-15a | keeper-update.spec.ts | covered |
| UPD-01 | update.spec.ts | covered |
| UPD-02 | update.spec.ts | covered |
| UPD-03 | update.spec.ts | covered |
| UPD-04 | update.spec.ts | covered |
| UPD-05 | keeper-update.spec.ts | covered |
| UPD-05a | keeper-update.spec.ts | covered |
| UPD-05b | keeper-update.spec.ts | covered |
| UPD-06 | update.spec.ts | covered |
| UPD-07 | keeper-update.spec.ts | covered |
| UPD-07a | keeper-update.spec.ts | covered |
| UPD-08 | - | manual-only: a Go test scenario, no path through the demo (the Go tests cover it) |
| UPD-09 | - | manual-only: needs the real release feed |
| INST-01 | - | manual-only: installer and Windows services, not in the demo |
| INST-02 | - | manual-only: installer and Windows services, not in the demo |
| INST-03 | - | manual-only: installer and Windows services, not in the demo |
| INST-04 | - | manual-only: installer and Windows services, not in the demo |
| INST-05 | - | manual-only: installer and Windows services, not in the demo |
| INST-06 | - | manual-only: installer and Windows services, not in the demo |
| INST-07 | - | manual-only: installer and Windows services, not in the demo |
| INST-08 | - | manual-only: installer and Windows services, not in the demo |
| INST-09 | - | manual-only: installer and Windows services, not in the demo |
| INST-10 | - | manual-only: installer and Windows services, not in the demo |
| INST-11 | - | manual-only: installer and Windows services, not in the demo |
| INST-12 | - | manual-only: installer and Windows services, not in the demo |
| INST-13 | - | manual-only: installer and Windows services, not in the demo |
| INST-14 | - | manual-only: installer and Windows services, not in the demo |
| INST-15 | - | manual-only: installer and Windows services, not in the demo |
| INST-16 | - | manual-only: installer and Windows services, not in the demo |
| INST-17 | - | manual-only: needs the Windows installer and services |
| INST-18 | - | manual-only: needs the Windows installer and services |
| INST-19 | - | manual-only: needs the Windows installer and services |
| INST-20 | - | manual-only: needs the Windows installer and services |
| INST-21 | - | manual-only: needs the Windows installer and services |
| INST-22 | - | manual-only: needs the Windows installer and services |
| HELP-01 | help.spec.ts | covered |
| HELP-02 | help.spec.ts | covered |
| HELP-03 | help.spec.ts | covered |
| HELP-04 | help.spec.ts | covered |
| HELP-05 | help.spec.ts | covered |
| HELP-06 | help-guide.spec.ts | covered |
| HELP-06a | help-guide.spec.ts | covered |
| HELP-06b | help-guide.spec.ts | covered |
| HELP-07 | - | manual-only: a web unit-test scenario with no demo path |
| HELP-08 | help-guide.spec.ts | covered |
| HELP-08a | help-guide.spec.ts | covered |
| HELP-09 | help-guide.spec.ts | covered |
| HELP-10 | help-guide.spec.ts | covered |
| HELP-10a | help-guide.spec.ts | covered |
| HELP-11 | help-guide.spec.ts | covered |
| HELP-11a | help-guide.spec.ts | covered |
| HELP-12 | help-guide.spec.ts | covered |
| HELP-12c | help-guide.spec.ts | covered |
| HELP-13 | help.spec.ts | covered |
| HELP-14 | help-guide.spec.ts | covered |
| HELP-14a | help-guide.spec.ts | covered |
| HELP-14b | help-guide.spec.ts | covered |
| SEC-01 | sec.spec.ts | covered |
| SEC-01a | sec.spec.ts | covered |
| SEC-01b | sec.spec.ts | covered |
| SEC-02 | security.spec.ts | covered |
| SEC-03 | sec.spec.ts | covered (the quiet demo log only) |
| SEC-04 | sec.spec.ts | covered (the demo case files only) |
| SEC-05 | sec.spec.ts | covered (the API verbs only) |
| SEC-06 | security.spec.ts | covered |
| SEC-07 | security.spec.ts | covered |
| SEC-08 | security.spec.ts | covered |
| SEC-08b | security.spec.ts | covered |
| SEC-09 | sec.spec.ts | covered |
| SEC-10 | - | manual-only: not reachable in the demo |
| SEC-11 | sec.spec.ts | covered |
| A11Y-01 | a11y.spec.ts | covered |
| A11Y-01a | a11y.spec.ts | covered |
| A11Y-01b | a11y.spec.ts | covered |
| A11Y-02 | a11y.spec.ts | covered |
| A11Y-02a | a11y.spec.ts | covered |
| A11Y-02b | a11y.spec.ts | covered |
| A11Y-02c | a11y.spec.ts | covered |
| A11Y-03 | a11y.spec.ts | covered |
| A11Y-03a | a11y.spec.ts | covered |
| A11Y-03b | a11y.spec.ts | covered |
| A11Y-04 | a11y.spec.ts | covered |
| A11Y-05 | a11y.spec.ts | covered |
| A11Y-05a | a11y.spec.ts | covered |
| A11Y-06 | a11y.spec.ts | covered |
| A11Y-06a | a11y.spec.ts | covered |
| A11Y-07 | a11y.spec.ts | covered |
| A11Y-07a | a11y.spec.ts | covered |
| A11Y-07b | a11y.spec.ts | covered |
| A11Y-07c | a11y.spec.ts | covered |
| A11Y-08 | a11y.spec.ts | covered |
| A11Y-08a | a11y.spec.ts | covered |
| A11Y-09 | a11y.spec.ts | covered (the Help and guide half) |
| A11Y-10 | a11y.spec.ts | covered |
| A11Y-10a | a11y.spec.ts | covered |
| A11Y-11 | - | manual-only: not reachable in the demo |
| A11Y-12 | a11y.spec.ts | covered |
| A11Y-12a | a11y.spec.ts | covered |
| DOC-01 | docs-more.spec.ts | covered |
| DOC-01a | docs-more.spec.ts | covered |
| DOC-02 | docs.spec.ts | expected-fail (DOC-02 D-C-29) |
| DOC-03 | docs.spec.ts | covered |
| DOC-04 | docs-more.spec.ts | covered |
| DOC-04a | docs-more.spec.ts | covered |
| DOC-05 | docs.spec.ts | covered |
| DOC-06 | docs-more.spec.ts | covered |
| DOC-06a | docs-more.spec.ts | covered |
| DOC-07 | docs-more.spec.ts | covered |
| DOC-07a | docs-more.spec.ts | covered |
| DOC-08 | docs-more.spec.ts | covered |
| DOC-08a | docs-more.spec.ts | covered |
| DOC-09 | docs-more.spec.ts | covered |
| DOC-09a | docs-more.spec.ts | covered |
| DOC-10 | docs.spec.ts | covered |
| DOC-10a | docs.spec.ts | covered |
| DOC-11 | docs-more.spec.ts | covered |
| DOC-11a | docs-more.spec.ts | covered |
