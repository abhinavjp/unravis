# Owner rulings A: results

| Ruling | File | Change | Test |
|---|---|---|---|
| UI-R5-03 | web/src/lib/copy/enterCopy.ts | New `CODE_ARCHIVED` = "That case is archived. Open it from Saved cases on this screen." (UI list is named "Saved cases"); added to `enterLintEntries` | web/tests/caseCode.test.cjs (new test; lint test already covers entries) |
| UI-R5-03 | web/src/lib/caseCode.ts | `resolveCode` returns `CODE_ARCHIVED` when the refusal reason is `case-archived` (409); every other refusal keeps `CODE_NOT_FOUND`; locked/untrusted still open | web/tests/caseCode.test.cjs (archived, other 409/404 unchanged, locked still opens); last old test now asserts `message: CODE_ARCHIVED` instead of "never says archived" |
| UI-R3-01 | web/src/lib/tracing/lanes.ts | SQL `PANEL_TITLE` = "Slow database calls (over 1 s)" | web/tests/tracingChart.test.cjs (new test) |
| UI-R3-01 | tools/e2e/specs/tracing-screen.spec.ts | Expected title updated | not run (e2e) |

Verified from web/: `npm run check` 0 errors 0 warnings; `node --test tests/*.test.cjs` 2197 pass, 0 fail.
