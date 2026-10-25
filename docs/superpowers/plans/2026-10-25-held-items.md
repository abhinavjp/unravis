# Held items: what is open, who owns it, how to close it

Status as of 2026-10-25, master after the w4 merges. Everything the build can do without a third party is done. The items below cannot be closed from inside the repository. Each one names the owner, what they must supply, and what happens when they do. Nothing here is a hidden defect: each item is either blocked on access, on a person, or on a ruling.

| Item | Blocked on | What closes it | What I will do when it is closed |
|---|---|---|---|
| Real-export importer runs (R2, R8, R11, R12, R13, R16) | An analyst running each importer on a real export on their own machine. The builder never opens a real export. | The analyst runs the import and reports accepted/rejected counts and any "unsupported format" or time-zone refusal, with no row content. | Fix the parser rule the report names. Importer format assumptions marked "verify" in the code and plans are the first checks. |
| T0.7 Mongo field check (decides P1c per-filter timings) | The Mongo owner, off-peak or on a read-only copy. | The owner runs the read-only T0.7 script and reports whether `PreActionFilter*` fields are present, null or absent. | Present: build P1c as written in the incident-gap addendum. Null or absent: close P1c with a note, as the addendum says. |
| Ops, DBA and Mongo-owner items (T0) | The people who own those systems: collection scripts, permissions (`$collStats`), Mongo 3.4 behaviour, `getMore` and `maxTimeMS`. | Each owner confirms or refuses their item. | Adjust the collection plan and the Mongo reads to what they confirm. |
| 24-hour live scan (D3) | A named person lifting the refusal. | A written yes from the system owner for a 24-hour read on the live source. | Run it and record the cost figures in the performance notes. |
| Five-tester usability test (P14b) | Five people outside the build. | Five sessions on the shipped UI with the task script. | Fix what two or more testers trip on. |
| Theory engine (R-TE) | A governance ruling. The spec says nothing is built before R-TE is granted, and the fallback is the list without a result column. That fallback is what ships today: the Ledger holds theories, a settling observation per theory, and the "Compare theories" matrix, with no automatic verdicts. | The product owner grants or refuses R-TE. | Granted: build in the order of the spec's rollout table, step 1 first. Refused: close the spec as design only. |

## Risks that stay until real data exists

- Nothing is measured on a real data set. Timings, memory and the 10 s `maxTimeMS` limit are from synthetic runs.
- Rollup aggregations are untested on a real Mongo.
- Flaky tests, found by repeating them under `-cpu 1,2,8` and fixed:
  - `TestExportLedgerOnlyRelatedHypotheses` built the demo at one clock reading and the request window at a later one. When the second changed in between, the window missed the demo rows (about 1 run in 200). The test now pins both to one instant.
  - `TestFanoutKeepsOneOperationPerSourceAndFourGlobally` sometimes saw 3 concurrent operations, not 4. `admit` took the global slot before the per-source slot, so a read queued behind another read of its own source held a global slot while idle. It now takes the per-source slot first. 80 repeat runs at `-cpu 8` pass; before, about 1 in 50 failed.
  - A `-count=100` run of the fanout tests hit Go's 10 minute default timeout, not a leak: one pass takes about 8 s by design (200 ms operation delay), and the stuck goroutines in the dump were the tests still running.
- No `-race` run: the encrypted SQLite driver needs CGO off.
