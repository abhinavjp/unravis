# Scenario coverage

This file says which catalogue scenarios the end-to-end suite runs, and what is left to a person.
The catalogue is in `.dev/scenarios/groups` (g01 to g12). Status words:

- covered: a spec runs it and it passes.
- expected-fail: a spec asserts the EXPECTED behaviour; the app does not do it yet, so the test is marked as an expected failure with the defect id. When the defect is fixed the test starts to pass, Playwright reports "expected to fail, but passed", and the id must be removed from `known-defects.json`.
- manual-only: not automatable here, with the reason.
- not yet automated: reachable in the demo but no spec written yet.

Totals: 53 covered, 20 expected-fail, 45 manual-only, 117 not yet automated, 235 scenarios.
Extra ids with no catalogue entry (IMP-01 to IMP-04, the file import fixtures) are in `specs/imports.spec.ts`.
A scenario id with a letter after it (for example SRC-07a) is an extra check that belongs to the same scenario.

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
| CASE-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| CASE-07 | case-api.spec.ts | expected-fail (CASE-07b D-A-08) |
| CASE-08 | case-ui.spec.ts | covered |
| CASE-09 | case-ui.spec.ts | covered |
| CASE-10 | case-api.spec.ts, security.spec.ts | expected-fail (CASE-10 D-A-01) |
| CASE-11 | - | not yet automated (see the scenario file; a manual run covers it) |
| CASE-12 | case-api.spec.ts | covered |
| CASE-13 | case-api.spec.ts | covered |
| CASE-14 | - | not yet automated (see the scenario file; a manual run covers it) |
| CASE-15 | case-api.spec.ts | covered |
| CASE-16 | case-drawers.spec.ts | covered |
| CASE-17 | case-api.spec.ts | covered |
| CASE-18 | case-api.spec.ts, case-drawers.spec.ts | expected-fail (CASE-18 D-A-15) |
| CASE-19 | - | manual-only: needs a master key and a real archive round trip |
| CASE-20 | case-api.spec.ts | covered |
| CASE-21 | case-drawers.spec.ts | expected-fail (CASE-21 D-A-16) |
| CASE-22 | case-api.spec.ts | covered |
| CASE-23 | - | not yet automated (see the scenario file; a manual run covers it) |
| CASE-24 | - | not yet automated (see the scenario file; a manual run covers it) |
| CASE-25 | - | manual-only: needs old per-user case folders on a real profile |
| KEY-01 | keys.spec.ts | covered |
| KEY-02 | keys.spec.ts | covered |
| KEY-03 | keys.spec.ts | covered |
| KEY-04 | keys.spec.ts | covered |
| KEY-05 | keys.spec.ts | covered |
| KEY-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEY-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEY-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEY-09 | - | manual-only: needs the PC master key |
| KEY-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| SRC-01 | sources.spec.ts | covered |
| SRC-02 | sources.spec.ts | covered |
| SRC-03 | - | not yet automated (see the scenario file; a manual run covers it) |
| SRC-04 | sources.spec.ts | expected-fail (SRC-04a D-A-28) |
| SRC-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| SRC-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| SRC-07 | sources.spec.ts | expected-fail (SRC-07c D-A-30, SRC-07a D-A-31, SRC-07b D-A-32) |
| SRC-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| SRC-09 | sources-limit.spec.ts | covered |
| SRC-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| SRC-11 | - | not yet automated (see the scenario file; a manual run covers it) |
| SRC-12 | - | manual-only: needs a real SQL Server |
| SRC-13 | - | manual-only: needs a real SQL Server |
| SRC-14 | - | manual-only: needs a real MongoDB |
| INV-01 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-02 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-03 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-04 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-09 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-10 | explorer.spec.ts | expected-fail (INV-10 D-A-45) |
| INV-11 | explorer.spec.ts | expected-fail (INV-11 D-A-46) |
| INV-12 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-13 | plan.spec.ts | covered |
| INV-14 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-15 | - | not yet automated (see the scenario file; a manual run covers it) |
| INV-16 | - | not yet automated (see the scenario file; a manual run covers it) |
| MCP-01 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-02 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-03 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-04 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-05 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-06 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-07 | - | manual-only: needs the MCP bridge process, not started by the demo |
| MCP-08 | - | manual-only: needs the MCP bridge process, not started by the demo |
| GUIDE-01 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-02 | guided.spec.ts | covered |
| GUIDE-03 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-04 | guided.spec.ts | covered |
| GUIDE-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-09 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-11 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-12 | - | manual-only: not reachable in the demo |
| GUIDE-13 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-14 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-15 | plan.spec.ts | covered |
| GUIDE-16 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-17 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-18 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-19 | guided.spec.ts | expected-fail (GUIDE-19 D-B-02) |
| GUIDE-20 | - | manual-only: not reachable in the demo |
| GUIDE-21 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-22 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-23 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-24 | - | not yet automated (see the scenario file; a manual run covers it) |
| GUIDE-25 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-01 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-02 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-03 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-04 | guided.spec.ts | expected-fail (MAN-04 D-B-01) |
| MAN-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| MAN-09 | plan.spec.ts | expected-fail (MAN-09c D-B-15) |
| MAN-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| EXP-01 | - | not yet automated (see the scenario file; a manual run covers it) |
| EXP-02 | - | not yet automated (see the scenario file; a manual run covers it) |
| EXP-03 | exports.spec.ts | expected-fail (EXP-03 D-B-36) |
| EXP-04 | exports.spec.ts | covered |
| EXP-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| EXP-06 | exports.spec.ts | covered |
| EXP-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| EXP-08 | exports.spec.ts | expected-fail (EXP-08 D-B-05) |
| EXP-09 | exports.spec.ts | expected-fail (EXP-09 D-B-41) |
| EXP-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-01 | blackbox.spec.ts | covered |
| BB-02 | blackbox.spec.ts | covered |
| BB-03 | blackbox.spec.ts | covered |
| BB-04 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-09 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-11 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-12 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-13 | blackbox.spec.ts | covered |
| BB-14 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-15 | blackbox.spec.ts | covered |
| BB-16 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-17 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-18 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-19 | blackbox.spec.ts | covered |
| BB-20 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-21 | - | not yet automated (see the scenario file; a manual run covers it) |
| BB-22 | - | manual-only: needs a real Blackbox service |
| BB-23 | - | manual-only: needs a real Blackbox service |
| KEEP-01 | keeper.spec.ts | covered |
| KEEP-02 | keeper.spec.ts | covered |
| KEEP-03 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEEP-04 | keeper.spec.ts | covered |
| KEEP-05 | keeper.spec.ts | covered |
| KEEP-06 | keeper.spec.ts | covered |
| KEEP-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEEP-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEEP-09 | keeper.spec.ts | expected-fail (KEEP-09 D-C-01) |
| KEEP-10 | - | manual-only: needs the Windows key keeper service |
| KEEP-11 | - | manual-only: needs the Windows key keeper service |
| KEEP-12 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEEP-13 | - | not yet automated (see the scenario file; a manual run covers it) |
| KEEP-14 | - | manual-only: needs the Windows key keeper service |
| KEEP-15 | - | not yet automated (see the scenario file; a manual run covers it) |
| UPD-01 | update.spec.ts | covered |
| UPD-02 | update.spec.ts | covered |
| UPD-03 | update.spec.ts | covered |
| UPD-04 | update.spec.ts | covered |
| UPD-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| UPD-06 | update.spec.ts | covered |
| UPD-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| UPD-08 | - | not yet automated (see the scenario file; a manual run covers it) |
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
| HELP-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| HELP-07 | - | manual-only: a web unit-test scenario with no demo path |
| HELP-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| HELP-09 | - | not yet automated (see the scenario file; a manual run covers it) |
| HELP-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| HELP-11 | - | not yet automated (see the scenario file; a manual run covers it) |
| HELP-12 | - | not yet automated (see the scenario file; a manual run covers it) |
| HELP-13 | help.spec.ts | covered |
| HELP-14 | - | not yet automated (see the scenario file; a manual run covers it) |
| SEC-01 | - | not yet automated (see the scenario file; a manual run covers it) |
| SEC-02 | security.spec.ts | covered |
| SEC-03 | - | not yet automated (see the scenario file; a manual run covers it) |
| SEC-04 | - | not yet automated (see the scenario file; a manual run covers it) |
| SEC-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| SEC-06 | security.spec.ts | covered |
| SEC-07 | security.spec.ts | covered |
| SEC-08 | security.spec.ts | expected-fail (SEC-08 D-C-17) |
| SEC-09 | - | not yet automated (see the scenario file; a manual run covers it) |
| SEC-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| SEC-11 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-01 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-02 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-03 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-04 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-05 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-09 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-10 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-11 | - | not yet automated (see the scenario file; a manual run covers it) |
| A11Y-12 | - | not yet automated (see the scenario file; a manual run covers it) |
| DOC-01 | - | not yet automated (see the scenario file; a manual run covers it) |
| DOC-02 | docs.spec.ts | expected-fail (DOC-02 D-C-29) |
| DOC-03 | docs.spec.ts | expected-fail (DOC-03 D-C-30) |
| DOC-04 | - | not yet automated (see the scenario file; a manual run covers it) |
| DOC-05 | docs.spec.ts | expected-fail (DOC-05 D-C-31) |
| DOC-06 | - | not yet automated (see the scenario file; a manual run covers it) |
| DOC-07 | - | not yet automated (see the scenario file; a manual run covers it) |
| DOC-08 | - | not yet automated (see the scenario file; a manual run covers it) |
| DOC-09 | - | not yet automated (see the scenario file; a manual run covers it) |
| DOC-10 | docs.spec.ts | expected-fail (DOC-10 D-C-18) |
| DOC-11 | - | not yet automated (see the scenario file; a manual run covers it) |
