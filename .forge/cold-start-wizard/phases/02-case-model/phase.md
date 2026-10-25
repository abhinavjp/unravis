# Phase 02: case-model
risk: high — reasons: schema migration of encrypted per-case data (reversibility, data); new store methods on a security boundary; every other phase reads this shape.

## Outcome
The case store holds environments and a per-source tag table (`case_sources`): environment, portal, API, kind, label, and for Mongo the database classification. Old cases (schema 3) migrate additively to schema 4 and read identically. HTTP routes read and write the model.

## Tasks
| Task | Risk | Depends on |
|---|---|---|
| TSK-06 schema v4 + store methods | high | — |
| TSK-07 tags API | medium | TSK-06 |
| TSK-08 Mongo database classification | medium | TSK-06 |

## Edges
TSK-07 and TSK-08 both add routes in separate files; both depend on TSK-06's `Store` methods and the in-memory test store (`internal/casestore/memory.go`) staying in step with the sqlite store (contract tests in `storetest`).

## Review focus
Migration safety on an existing large case; the in-memory and sqlite stores diverging; credentials or paths landing in a tag; label length and character abuse.

## Phase proof
`go test ./internal/casestore/... ./internal/server/...` green including `storetest` contract suite; migration test opens a v3 fixture.
Commit: one commit for the phase, after a stop. Never push.
