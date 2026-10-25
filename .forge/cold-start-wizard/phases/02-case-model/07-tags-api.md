# TSK-07: HTTP routes for environments and source tags
risk: medium — new write routes on case data.

## Outcome
`GET/PUT /api/cases/{id}/environments` and `GET/POST/DELETE /api/cases/{id}/sources` read and write the model; `GET /api/cases/{id}` includes counts. The wizard can create a case, set its environments and tag each source it adds (R4, R5).

## Write scope
- `internal/server/case_sources.go` (new), `internal/server/case_sources_test.go` (new)
- `internal/server/routes.go` (route entries)
- `internal/server/case_views.go` (add the counts to the case view)

## Must not change
- Existing case routes' behaviour and JSON field names; Origin/token/CSRF handling; the body-size limit (use the same small-JSON limit).

## Changes
1. Handlers decode with `DisallowUnknownFields`, call the `Tagger` methods from TSK-06, map store bound errors to 422 with a plain message, closed case to 409.
2. DELETE on a source removes the tag only; it never deletes imported events (state that in the response text and the test).

## Proof
Tests: happy path; unknown field → 400; over-long label → 422; 33rd environment → 422; closed case → 409; wrong case id format → uniform 404; missing token/Origin → refused; `GET /api/cases/{id}` for a v3-migrated case returns zero counts. `go test ./internal/server/...` green.

## Depends on
TSK-06
