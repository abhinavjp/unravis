# Phase 03: steps

Goal: the 8 steps as small components, the target picker shared with Apply, and the wizard that wires them to the real API.
Tasks: TSK-09..16 run in parallel once TSK-01, TSK-02 and TSK-08 are done (TSK-13 also needs TSK-12; TSK-15 also needs TSK-05). TSK-12 can start on day one. TSK-17 joins them all.
Step components get state and copy as props/imports and never edit the copy file. A missing copy key is written in `progress.md`; TSK-17 adds it.
Exit check: `cd web && npm test && npm run check && npm run build`; merge-sentinel review of TSK-12 and TSK-17.
Commit: one phase commit.
