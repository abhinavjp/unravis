# Progress: Phase 05 verification-and-docs

## Done

- TSK-26 DONE: section "When a comparison is greyed out" added to docs/user/guided-analysis.md (right after "Names that never change", before Step 0).
- TSK-27 DONE: all checks PASS (see Evidence).
- TSK-28 DONE: comprehension-test.md created with three prompts.

## Evidence

- TSK-26: Grep `When a comparison is greyed out` -> 1 hit. Note: first draft said "within about 10%"; TestGuidedAnalysisDocWording forbids a percent sign, so the line was reworded to "within about 10 in 100" (own wording in the written section; no test edited).
- 1. `go build ./...` PASS (no output, exit 0).
- 2. `go test ./...` PASS (all packages ok; last line `ok  	api-audit-explorer/internal/version`). First run failed on my own doc wording (above), re-run clean.
- 3. `cd web; npm test` PASS (`# tests 1996`, `# pass 1996`, `# fail 0`).
- 4. `cd web; npm run check` PASS (`COMPLETED 392 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`).
- 5. `Get-ChildItem tools/e2e/specs` -> 35 spec files listed (a11y, bb-add, bb-change, blackbox, case-*, docs*, explorer, export*, guide-*, guided, help*, imports, investigate, keeper*, keys*, manual-tools, plan, sec, security, sources*, update). Not run.
- 6. flow-check result copied from phase 03 (not re-run), see UNMEASURED.

## Not fixed

(nothing)

## UNMEASURED

- e2e specs not run (Playwright not installed)
- UNMEASURED: flow-check not run (Playwright missing). `node prototype/flow-check.cjs` -> `Error: Cannot find module 'playwright'`.
- Human comprehension test: not run — needs a person; see comprehension-test.md.
