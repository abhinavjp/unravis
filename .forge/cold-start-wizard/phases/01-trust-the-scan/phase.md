# Phase 01: trust-the-scan
risk: medium — reasons: touches the scan's trust contract (a wrong "ready" is the user's worst surprise); small surface (discover + two new server routes); no schema change.

## Outcome
Every finding the scan offers as importable has been read by its own importer's header check; a finding that cannot be read is demoted with a plain reason. Folder-name suggestions and a locate step exist for log4net/NLog, and a reachability probe exists for services found on this PC.

## Tasks
| Task | Risk | Depends on |
|---|---|---|
| TSK-01 verify pass | high | — |
| TSK-02 folder suggestions | low | — |
| TSK-03 locate endpoint | medium | TSK-01 |
| TSK-04 reachability probe | medium | — |
| TSK-05 web client types | low | TSK-01, TSK-02, TSK-03, TSK-04 |

## Edges
TSK-03 reuses TSK-01's verify function. TSK-05 consumes the JSON shapes defined in TSK-01..04. TSK-01, 02, 04 are independent (different files) and can run in parallel.

## Review focus
A false "ready"; a path leaking to the client; the probe dialling something the user did not type; scan hangs (bounded reads, time limits).

## Phase proof
`.dev\toolchains\go\bin\go.exe test ./internal/discover/... ./internal/server/...` green; `cd web; npm test; npm run check` green.
Commit: one commit for the phase, after a stop. Never push.
