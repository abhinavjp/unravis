# Round 5 independent review and verification ledger

Scope: spec validation only. No repository build, no runtime tests and no live Mongo/SQLite run was performed. No claim of guaranteed correctness is made.

## Reviewers

- Round A: three independent reviewers on `cursor-composer-2-5` (a different model family from the cloud fixers), sliced by spec part, on the post-mop-up spec.
- Round B: the same three slices re-run on the post-final-fix spec to check closure of every round A finding.

## Round A findings (27) and how each was handled

| slice | id | sev | where | problem | resolution |
|---|---|---|---|---|---|
| R1 | c1 | P0 | SC-PRV-199 | Coverage gap fill via GET vs GET never fills gaps | patched in final-fix pass; re-checked in round B |
| R1 | c2 | P1 | §12.1 | R3-011 recommended default still mandates serverWorkId dedup while normative API text removes it (DEC8 REVERSE) | patched in final-fix pass; re-checked in round B |
| R1 | c3 | P1 | §7 | Bucket axis anchored at window start vs UTC floor-aligned axisStart | patched in final-fix pass; re-checked in round B |
| R1 | c4 | P1 | §5.1 | Audit lane described as paginated remote failure vs single $match+$group with no raw paging | patched in final-fix pass; re-checked in round B |
| R1 | c5 | P1 | SC-API-220 | Misaligned 30-day window must coarsen rung (e.g. 8 h) vs 6 h W=span/120 clause in same scenario | patched in final-fix pass; re-checked in round B |
| R1 | c6 | P1 | §4.3 | Normative coverage DDL includes content_digest; Appendix P7 DDL block omits it | patched in final-fix pass; re-checked in round B |
| R1 | c7 | P1 | §12.1 | R4-005 recommended default names fromStored true while API field is allLanesStored (DEC8 CONFIRM) | patched in final-fix pass; re-checked in round B |
| R1 | c8 | P2 | §12.1 | Open-question rows lack DEC8 verdict labels; R3-011 default contradicts body | patched in final-fix pass; re-checked in round B |
| R1 | c9 | P2 | §2 | Glossary bucket definition uses window-start slices vs aligned grid elsewhere | patched in final-fix pass; re-checked in round B |
| R1 | c10 | P2 | WP-07 | Packet text implies byte-level same-input re-derivation vs snapshot+scrub-class guarantee | patched in final-fix pass; re-checked in round B |
| R2 | c1 | P0 | SC-PRV-199 vs §5.3 | Coverage scenario implies GET fills gaps by reading sources; §5.3 says GET never fills gaps and opens no source connection | patched in final-fix pass; re-checked in round B |
| R2 | c2 | P1 | EV-UI-220 vs §7 coarser rung | Eval expects 6h width for 30d window at 00:01; coarser-rung rule selects 8h when aligned 6h count exceeds 120 | patched in final-fix pass; re-checked in round B |
| R2 | c3 | P1 | TR-06 vs §7 | TR-06 states unconditional W=span/120 for any 30-day span; §7 requires coarser rung when aligned count >120 | patched in final-fix pass; re-checked in round B |
| R2 | c4 | P1 | SC-PRV-181 vs TR-21 | Scenario promises re-derive from same inputs; TR-21 promises same revision and scrub class, not byte-identical output | patched in final-fix pass; re-checked in round B |
| R2 | c5 | P1 | WP-07 vs TR-21 | WP-07 goal still describes stateless re-derivation on the same inputs | patched in final-fix pass; re-checked in round B |
| R2 | c6 | P1 | §12.1 R3-011 vs §6 | Open-questions table still recommends serverWorkId dedup; normative UI/API text removes it | patched in final-fix pass; re-checked in round B |
| R2 | c7 | P1 | §12.1 R4-005 vs §5.1 | R4-005 default names fromStored; response schema uses allLanesStored | patched in final-fix pass; re-checked in round B |
| R2 | c8 | P1 | WP-06 row vs §9.3 | Dependency graph requires WP-65a before WP-06; packet table shows no dependency | patched in final-fix pass; re-checked in round B |
| R2 | c9 | P1 | §9.3 WP-58 edges vs WP-58 row | WP-58 table lists WP-65a; compact edge list omits it | patched in final-fix pass; re-checked in round B |
| R2 | c10 | P2 | Appendix P global done-check vs WP-62 | Release gate text relies on non-empty report count; R5-12 requires test-ID manifest coverage | patched in final-fix pass; re-checked in round B |
| R2 | c11 | P2 | Appendix P intro vs §0.2 | Appendix P forbids all repo exploration; §0 allows reported dependency-adjacent edits | patched in final-fix pass; re-checked in round B |
| R2 | c12 | P2 | §12.1 | DEC8 verdicts (CONFIRM/REFINE/REVERSE) not recorded; Owner answer remains OPEN for all ten items | patched in final-fix pass; re-checked in round B |
| R3 | c1 | P1 | SC-UI-203 | Refresh UI copy denies any customer DB connection while §5.1 allows POST .../tracing to run an ephemeral read-only live audit read | patched in final-fix pass; re-checked in round B |
| R3 | c2 | P1 | EV-UI-220 | Eval setup is a misaligned 30-day start but expected result keeps the 00:00 nominal W=span/120 width instead of coarser rung | patched in final-fix pass; re-checked in round B |
| R3 | c3 | P1 | EV-UI-220 | Same misaligned-window eval contradicts TR-06 coarser-rung canary already used elsewhere | patched in final-fix pass; re-checked in round B |
| R3 | c4 | P1 | WS-26 | Wireframe fixes 6-hour/120 buckets for long windows without the coarser-rung rule used in UI scenarios | patched in final-fix pass; re-checked in round B |
| R3 | c5 | P2 | SC-UI-201 | Stale subscriber wording from the retired dedup model | patched in final-fix pass; re-checked in round B |

## Round B result (re-review of the final fixes)

All 13 rulings R5-01..R5-13 were reported closed by at least one slice, except the partial closures below, which round B itself raised.

| slice | id | sev | where | problem | resolution |
|---|---|---|---|---|---|
| R1 | c1 | P1 | EV-API-227 vs SC-API-225 | Eval still expected a paging failure reason naming a page | EV-API-227 rewritten to reason in {timeout,rowcap,error} |
| R1 | c2 | P1 | SC-API-124 vs 5.1 | Audit failure still framed as failing after a first page | SC-API-124 rewritten to one aggregation command ending in timeout, rowcap or error |
| R1 | c3 | P1 | EV-API-31 vs 5.1 | Eval still described unreachable audit between pages | EV-API-31 rewritten to aggregation failure mid-run |
| R1 | c4 | P1 | EV-PRV-221 vs SC-PRV-221 | Eval said uncovered slivers are read, contradicting gaps-only coverage | EV-PRV-221 aligned: gaps reported, filled only by POST .../pulls |
| R1 | c5 | P2 | EV-API-33 vs EV-API-220 | 30 d fixed at 6 h without the misaligned 8 h case | EV-API-33 qualified; points to EV-UI-220 |
| R2 | c1 | P1 | AC-10 vs EV-UI-220 | AC-10 capped the 30-day label at 6 h | AC-10 reworded: 6 h hour-aligned, 8 h when the coarser rung applies, at most 120 buckets |
| R2 | c2 | P1 | AC-10 vs TR-06 | Same as c1 | Same fix |
| R2 | c3 | P2 | 9.1 vs 0.2 | 9.1 lacked the dependency-adjacent exception | Sentence added to 9.1, aligned to 0.2 wording |
| R3 | c1 | P1 | SC-ADV-120 | Ladder fixed 30 d at 21600 s without the 00:01 case | Line qualified: 00:00 start keeps 6 h; 00:01 start gives 8 h (28800 s), n=91 |
| R3 | c2 | P1 | EV-UI-02 | Treated every 30 d window as 6 h | Limited to a 00:00 hour-aligned start; misaligned starts point to EV-UI-220 |

Format observation from round B (R3): Area E archive Gherkin was not in a fenced block. Verified true (indented only). Fixed: two prose description lines moved above the block and the Feature wrapped in a gherkin fence; validator still reports 0 problems.

Checked and left as is: scenario titles and EV-PRV-181 that say confirm re-derives. Their bodies already state the snapshot plus scrub-policy-class guarantee (TR-21), not byte equality.

## Verification performed on the final spec

- Structure validator: 345 scenarios, 415 unique evaluation rows, 53 workstream rows; no missing TR/AC/BM coverage; no AC/BM without a scenario tag; no WP id outside the spec; 0 problems.
- Stale-term grep: no remaining `source failed on page` or `21600500`; `BroadScanBuckets` and `serverWorkId` appear only as retired terms (glossary, rulings table).
- Independent review: round B ran before the last residual edits. Those 12 edits were specified verbatim by the round B reviewers and validated structurally, but **no fourth independent review pass was run on them**.

## Remaining P0/P1/P2

None known after the residual edits. This is not a claim that none exist: the residual edits have not been independently re-reviewed, and nothing has been run.

## Blockers

None for the spec. Open owner questions (spec 12.1) still need your answers; the reversals of R4-009 and R3-011 and the refinements of R3-001, R4-001 and R4-010 are recommendations awaiting your OK.
