# TSK-08: Classify each Mongo database (audit logs / other data / not used)
risk: medium — touches the Mongo source flow that analysis depends on.

## Outcome
After a Mongo connection test lists databases, the user's per-database choice is stored as a case source with `classification` `audit`, `other` or `unused` (R10). Only `audit` databases become analysis sources (`audit.Source`); `other` is recorded as context; `unused` is remembered so the wizard does not ask again. Persisted with the case, no credentials stored.

## Write scope
- `internal/server/case_sources.go` (accept `classification`; add `GET /api/cases/{id}/mongo-sources`), `internal/server/case_sources_test.go`
- `internal/server/connections.go` or the file that holds `testConnection` (return the database list in the existing response shape only; no behaviour change) — touch only if the list is not already returned
- `web/src/lib/types.ts` is NOT touched here (TSK-19 does it)

## Must not change
- `POST /api/connections/test` request/response compatibility and its rate limit; the 8-source Mongo cap; the DPAPI vault format.
- Connection strings and passwords never enter `case_sources`; the label is the database name only.

## Changes
1. Validate the classification value and that the label equals a database name previously returned for that connection test (prevents arbitrary text); the check uses a short-lived server-side record of the last test result for the session.
2. `audit` rows are listed by the new `mongo-sources` route so the wizard can open them as analysis sources through the existing connect flow.

## Proof
Tests: audit/other/unused round-trip; database name not in the last test result → 422; a connection string containing a password never appears in any stored row or response (assert by scanning the JSON and the db bytes); missing token → refused. `go test ./internal/server/...` green.

## Depends on
TSK-06
