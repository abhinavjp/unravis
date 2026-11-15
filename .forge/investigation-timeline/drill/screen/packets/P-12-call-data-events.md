# P-12 Read event markers for the window

model: sonnet, effort: low. Reason: one small fetch helper over an endpoint that already exists.
risk: low. Review batch: RB-1. Wave: W0. Requirement: R-10 (BL-015).

## Outcome
A helper reads the imported SQL Server and HTTP.sys events of a case for a time window from the existing `GET /api/cases/{id}/events`, with no server change.

## Write scope
- `web/src/lib/drill/callData.ts` (new)
- `web/tests/slowcall.data.test.cjs` (new)

## Must not change
- `web/src/lib/cases.ts` (import `send`, `json`, `casePath` from it). No new endpoint.

## Changes
1. Export:
```ts
export const EVENT_SOURCES = ['sql-xe', 'sql-errorlog', 'iis-httperr'] as const;
export const EVENT_LIMIT = 500;
export interface CallEvent { evidenceId: string; source: string; kind: string; host?: string; start: string; end?: string | null; durationMs?: number | null; severity?: string }
export interface CallEvents { events: CallEvent[]; truncated: boolean }
export function eventsPath(caseId: string, fromIso: string, toIso: string): string;
export async function fetchCallEvents(caseId: string, fromIso: string, toIso: string, signal?: AbortSignal): Promise<CallEvents>;
```
2. `eventsPath` = `casePath(caseId, '/events')` + `?from=…&to=…&source=sql-xe,sql-errorlog,iis-httperr&limit=500`, built with `URLSearchParams`. It never sends `includeSensitive`. The handler is `caseEvents` in `internal/server/cases.go` (params `from`, `to` RFC3339, `source` comma list, `limit` 1..50000).
3. `fetchCallEvents` calls `send(path, { method: 'GET' }, signal)` then `json`. It keeps only the fields of `CallEvent` from each row (drops `line`, `attrs`, `value`, `unit`: record text must not travel further into the UI). A reply without an `events` array gives `{ events: [], truncated: false }`.
4. First read `web/src/lib/cases.ts` to copy the exact call style of `send` and `json` from a nearby GET helper.
5. Tests (text + logic): `eventsPath('abc', a, b)` contains `/events?`, `from=`, `to=`, `source=sql-xe%2Csql-errorlog%2Ciis-httperr`, `limit=500`, and does not contain `includeSensitive`; the source file does not contain the words `line` or `attrs` as copied properties (regex `\.(line|attrs)\b` has no match).

## Proof
- [ ] `cd web && node --test tests/slowcall.data.test.cjs` → pass, 0 fail.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] Not live-verified in this packet (no UI yet); P-46 checks markers with real imported files.

## Depends on
Nothing.
