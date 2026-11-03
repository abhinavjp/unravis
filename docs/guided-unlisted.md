# Guided analysis: kept but unlisted

Owner decision D-2 = A (INV-8): guided analysis stays in the product code base but is no longer listed. Start tracing replaced the Guided section on the case home.

## What "unlisted" means

- Stays: the guided code (`web/src/lib/guided/`, `web/src/lib/components/guided/`), its unit tests, its API routes and its copy.
- Goes: only the entry point on the case home. Nothing else is removed or changed.
- Do not delete any of the items below. `web/tests/guidedKept.test.cjs` fails if one goes missing.

## Kept API routes

These stay registered in `internal/server/routes.go`:

- `/api/cases/{id}/investigation`
- `/api/cases/{id}/views`
- `/api/cases/{id}/checklist`
- `/api/cases/{id}/report`

## Guided unit tests

- `web/tests/guided.logic.test.cjs`
- `web/tests/guided.steps.test.cjs`
- `web/tests/guided.ui.test.cjs`
- fixture: `web/tests/guided.fixture.cjs`

Run them with `cd web; node --test tests/guided.*.test.cjs`.

## Archived e2e specs

The browser specs live in `tools/e2e/archive/guided/` (seven `*.spec.ts` files plus `support.ts` and `README.md`). Run them with either:

- `cd tools/e2e; powershell -File run-e2e.ps1 -Archive`
- `cd web; npm run e2e:guided`

## Related

- Reuse of guided parts elsewhere: see [guided-reuse-table.md](guided-reuse-table.md).

## Known gap

After the case-home swap no screen opens `GuidedAnalysis`. The `#/case/guided/N` route is defined in `web/src/lib/screenRoute.ts`, but nothing in `src` reads it. The archived e2e specs therefore need a build from before the swap, or an entry hook. The owner has to decide which.
