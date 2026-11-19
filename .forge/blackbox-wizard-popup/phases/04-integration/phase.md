# Phase 04: integration

Goal: the pop-up is the only Place path in the real app, and `web/dist` is rebuilt.
Tasks: TSK-18 (mount, delete the old wizard), TSK-19 (dist rebuild and full test pass).
Exit check: `go test ./...`; `cd web && npm test && npm run check && npm run build`; `git status` shows only the expected `web/dist` changes.
Commit: one phase commit including `web/dist` (it is committed and embedded in the exe; CI does not check it, BL-031).
