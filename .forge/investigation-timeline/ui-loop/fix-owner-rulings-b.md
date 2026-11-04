# Fix owner rulings B: closed case opens the timeline as a viewer (UI-R2-03 / WEB-R1-25, option A)

Server finding: the tracing route does not reject a closed case. `Store.Open` succeeds for a closed case; `ErrCaseClosed` is only returned by writes. The handler only reads. No Go source change was needed; one Go test was added to pin it.

| File | Change | Test |
|---|---|---|
| internal/server/tracing_test.go (CRLF kept) | New `TestTracingClosedCaseReads`: closed sqlite case with an IIS import answers 200 with sources and lanes, and the case fingerprint is unchanged | `go test ./internal/server/...` ok |
| web/src/lib/copy/tracingCopy.ts | `closedReason` replaced by `closedView`: "This case is closed. You can look at the timeline but not change the case." (`errClosed` keeps the old sentence for a 409) | copy lint tests, tracingPaper.test.cjs |
| web/src/components/TracingPaper.svelte | `run()` no longer refuses when closed; inputs, Start tracing and overlap fix disable on `readOnly` only; Try again always shown; one `closed-line` status paragraph (data-testid `tp-closed-line`) under the Back button | tracingPaper.test.cjs, tracingRound4.test.cjs |
| web/src/lib/components/CaseHome.svelte | Timeline button no longer disabled and the "home-tracing-closed" note removed; fallback effect now only for archived (`tracingOpen && isArchived`); `timelineShown = tracingOpen && !isArchived`; passes `closed={isClosed}` and `readOnly={readOnly && !isClosed}`. A case closed in another tab stays on the timeline. Case-not-found/erased fallback untouched | tracingPaper.test.cjs, tracingRound4.test.cjs, tracingRound5.test.cjs |
| web/tests/tracingPaper.test.cjs | Entry-enabled test, `disabled={readOnly}` count, closed-line test (replaces the disabled-button and aria-describedby tests) | node --test |
| web/tests/tracingRound4.test.cjs | Archived fallback regex now `isArchived` only; closed-case test now asserts Try again present and no closed short-circuit | node --test |
| web/tests/tracingRound5.test.cjs | UI-R5-04 test rewritten: only archived falls back, closed stays, button not disabled | node --test |
| tools/e2e/specs/tracing-handoff.spec.ts | HAND-13 now expects the entry enabled, opens the timeline, sees the closed line, goes back (not run) | not run (orchestrator) |

Checks: `npm run check` 0 errors 0 warnings; `node --test tests/*.test.cjs` 2197 pass, 0 fail; `go vet ./internal/... ./cmd/...` clean; `go test ./internal/server/...` ok; `GOOS=linux go vet` clean (env var removed).

Not touched: start-screen archived-code message, web/src/lib/tracing/lanes.ts, web/dist, cmd/unravis-setup/ui/assets/index.css. docTitle.ts needed no change (title follows `timelineShown`).
