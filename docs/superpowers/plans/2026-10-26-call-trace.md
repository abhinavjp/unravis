# Call trace (Nearby activity) plan

Goal: one audited API call as a timeline (parts of the call, then SQL / Mongo / Redis / IIS lanes on the same clock), compare 2-3 calls, and four-status automatic checks. Lives in Nearby activity, not the drawer. Works with or without a case.

## Flow-change analysis (what can break)
Touched: new `internal/calltrace` (pure), new `internal/server/call_trace.go` + 1 route, new web lib/copy/component, small edit in `ActivityContext.svelte` (buttons, tray), one line on Case home.
Untouched: `internal/timeline`, CaseTools drawer, ledger, importers, existing routes.
Risks and guards:
- `ActivityContext` anchor-reset `$effect`: tray state is held in its own store and cleared only on explicit action.
- Copy lint (grade <= 8.5, no causal words): all new text goes through `lintScreenStrings` + `evidence.LintCausal` tests.
- Route table test, static wiring tests (`time.test.cjs` lists ActivityContext for time formatting): add, never remove.
- Stale responses: sequence guard + AbortController. Limits: max 3 calls, 60 events/lane, 50k read cap.
- Privacy: no attrs, no file names, no payload leave the server; labels are fixed phrases.
- No filter timings exist in the audit record: split is audit / api / other (+payload).

## Contract
`internal/calltrace/types.go` (JSON tags = frontend contract).
Slice A `checks.go`: `RunChecks(c Call, lanes []Lane) []Check`, `CompareChecks(traces []Trace) []Check`.
Slice B `lanes.go`: `LaneOf(source string) string`, `CallFromRecord(r audit.RequestRecord) (Call, bool)`, `EventLabel(e evidence.Event) string`, `SourceInfo{Name string; Validated bool}`, `BuildLanes(win Window, haveCase bool, sources []SourceInfo, evs []evidence.Event) []Lane`.
Slice C `internal/server/call_trace.go`: `POST /api/call-trace {calls:[{sourceId,id}] (1-3), beforeMin, afterMin}` and `POST /api/cases/{id}/call-trace` (same body; reads case events). Window = call span +/- margin (default 2 min, max 30).
Slice D+E web: `lib/callTrace.ts` (types, api, geometry), `lib/copy/callTraceCopy.ts`, `components/CallTrace.svelte`, edit `ActivityContext.svelte`.

## Delegation
A, B -> Runner Hub free lane (probe first, I review the patch, compile locally). C, D+E -> Sonnet. Review -> Sonnet x3 (security/adversarial, performance, clumsy user), Opus only for a final design pass if needed.
