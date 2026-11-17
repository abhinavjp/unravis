# Phase 01: contracts

Goal: every rule, the state machine, the catalogue and copy, and the drives data exist as tested `.ts` and Go code before any UI is built.
Tasks: TSK-01 (state), TSK-02 (catalogue and copy), TSK-03 (folder and keep rules), TSK-04 (drives route), TSK-05 (drives client).
Parallel: TSK-02, TSK-03, TSK-04 and TSK-05 run in parallel. TSK-01 waits for TSK-03 and TSK-05.
Exit check: `cd web && npm test && npm run check`; `go test ./internal/server/... ./internal/blackbox/...`; merge-sentinel review of TSK-04.
Commit: one phase commit.
