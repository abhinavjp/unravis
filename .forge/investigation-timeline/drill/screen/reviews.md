# Review plan: Slow call screen

Reviews run in batches, not per packet. A batch starts when all its packets report "Proof ticked". All findings files go to `screen/reviews/`.

## Batches

| Batch | Packets | Starts after | Reviewer model / effort | Why this model |
|---|---|---|---|---|
| RB-1 Models and copy | P-02..P-12 | W0 + P-07 | sonnet / medium | pure functions with unit tests; low blast radius; copy rules are mechanical |
| RB-2 Backend lanes | P-14, P-15, P-16, P-19, P-20, P-21, P-22 | W5 (first pass on P-14..P-16, P-22 may start after W1) | sonnet / high | number plumbing where null vs 0 matters; security half goes to RB-6 |
| RB-3 Activity and light totals | P-01 report, P-13, P-17, P-18, P-34..P-39 | W6 | sonnet / high | Mongo pipelines; correctness and bounds; security half goes to RB-6 |
| RB-4 Navigation and mount | P-23, P-33 | W4 | opus / high | the two riskiest edits: global key handling, history, a file pinned by about twenty text tests; a miss breaks the whole app |
| RB-5 Screen components | P-24..P-32 | W3 | sonnet / high | many files, moderate risk each; accessibility and the lane-fit rule |
| RB-6 Security | P-13, P-16, P-17, P-18, P-20, P-21, P-34..P-40 | W6 (this is packet P-43) | opus / high | owner rule: all SQL / data access gets an Opus security review |
| RB-7 Proof | P-41, P-42, P-44..P-48 | after each of them | sonnet / medium (the orchestrator, Fable, reads the reports itself; a Sonnet reviewer only for P-41 + P-42 spec quality) | reports and tests, low risk |

Fable is used for the orchestrator only. Opus is used twice: RB-4 and RB-6. Everything else is Sonnet. No Haiku reviewer.

## What each reviewer reads

Give the reviewer, in the prompt: the packet files of the batch (paths), `plan.md` sections "Rulings" and "Ground rules", and the exact diff command. Nothing else; no implementer summary (the reviewer must not be anchored on it).

| Batch | Diff command | Extra reading |
|---|---|---|
| RB-1 | `git diff -- web/src/lib/drill web/src/lib/screenFlow.ts web/src/lib/tracing web/src/lib/types.ts web/tests/slowcall.*.test.cjs web/tests/drill.copy.test.cjs` plus untracked new files in those folders (`git status --short web/`) | wireframe `prototype/slow-call-popup-WIREFRAME-v3.html` for copy and behaviour |
| RB-2 | `git diff -- internal/server/tracing*.go internal/casestore/buckets*.go internal/dbhealth/lane*.go cmd/audit-explorer/demo_cases*.go` plus new files | none |
| RB-3 | `git diff -- internal/audit internal/store internal/server/activity*.go internal/server/tracing_apiseen*.go` plus new files | `screen/probe/fields.md` |
| RB-4 | `git diff -- web/src/lib/appState.svelte.ts web/src/components/TracingPaper.svelte web/tests/slowcall.appstate.test.cjs web/tests/drill.paper.test.cjs` | `web/src/lib/screenFlow.ts`, `web/src/lib/drill/callTarget.ts`, `web/tests/tracingPaper.test.cjs` |
| RB-5 | new files `web/src/components/Call*.svelte`, `SlowCallScreen.svelte`, `web/src/lib/drill/callController.ts`, their tests | the wireframe (layout W1); the models' exported names |
| RB-6 | see P-43 | see P-43 |
| RB-7 | `git diff -- tools/e2e` | `tools/e2e/COVERAGE.md` |

Note: the branch already holds many unrelated uncommitted changes. A reviewer reviews ONLY the paths named for its batch and only the lines the packets describe; unrelated changes in the same file are out of scope (say "out of scope" once, do not review them).

## What each reviewer checks

All batches: the packet's Outcome is met; "Must not change" was respected; null stays null (never a stand-in 0); tests test behaviour and would fail without the change; no banned copy word; nothing demo-only passed off as real.
- RB-1: maths (running at once = busyMs / (bucketSeconds × 1000)); time zones; clamps; every copy key used by a later packet exists; FK grade and banned words; beginner wording.
- RB-2: JSON tags and `omitempty`; merging across sources; lane precedence (held > qs-file > api-seen); strict decoding kept; demo fills the new fields.
- RB-3: filters validated; pipelines bounded and 3.4-safe; paging unchanged; probe paths match the wired paths exactly.
- RB-4: Esc order; history marks (Back, Forward, reload); focus return; no extra timeline read on return; every pinned test still means what it meant; lazy chunk still split.
- RB-5: props match between parent and children; preview does no read, commit does one; stale answers dropped; aborts on dispose; keyboard and screen-reader use; no inner scroll; lane-fit rule; demo switch unreachable outside the demo.
- RB-7: scenarios assert behaviour, not markup trivia; no retries hiding a flake.

## Findings file format

File: `screen/reviews/RB-n-findings-round-k.md` (RB-6: `RB-6-security.md`). At most 60 lines.

```
# RB-n round k: <batch name>
Reviewed: <packet list>   Diff: <command used>

F-01 | P-nn | blocker|major|minor|nit | confidence high|medium|low | path:line | evidence (one quoted line or one sentence of what happens) | fix in one line
F-02 | ...

UNCLEAR: <anything the reviewer could not decide, with the question>
VERDICT: SHIP | FIX-FIRST
```

Rules for the reviewer prompt: severity AND confidence on every finding; file:line; evidence; one-line fix; verdict; at most 60 lines; "do not spawn subagents"; "do not edit files other than your findings file"; "do not review paths outside the list"; "if you find nothing, say so; do not invent findings".

## How findings go back

1. The orchestrator reads the findings file and groups findings by packet (the `P-nn` column).
2. For each packet with a blocker or major (and minors with confidence high): the orchestrator sends the findings to THE SAME implementer subagent that built the packet, with `SendMessage` to that agent's id (it still has the context). Message = the finding lines verbatim + "fix only these; re-run the packet's Proof; report evidence; do not commit". If that agent no longer answers, start a fresh one with the packet file plus the findings, same model.
3. Minor (low / medium confidence) and nit findings are collected in `screen/reviews/deferred.md`; they are not fixed in this plan unless the fix is one line in a file the fixer already has open.
4. A finding the implementer disagrees with comes back as "DISPUTED: reason + evidence". The orchestrator decides; for RB-4 and RB-6 a disputed blocker goes back to the same Opus reviewer once.

## Rounds and cost limits

- At most 5 rounds per batch. One fixer pass per round (each implementer fixes all its findings in one go).
- Round k+1 re-runs a reviewer ONLY if round k was FIX-FIRST with a blocker or major, or had an `UNCLEAR:` line. Otherwise the orchestrator checks the fix itself against the Proof evidence and closes the batch.
- Re-review reads only the files touched by the fix (`git diff` on those paths), not the whole batch.
- Re-reviews of Sonnet batches stay on Sonnet at effort medium. RB-4 and RB-6 re-reviews stay on Opus but read only the fixed lines.
- After round 5 with a blocker still open: stop, write it in the owner report, do not go on to P-45.
- Watch weekly usage: the two Opus reviews are run once each on the complete batch, never per packet.

## Separate acceptance packets (not reviews)

- P-46 browser acceptance on live data: Sonnet, its own packet.
- P-47 clumsy-user pass: Sonnet, its own packet, after P-46.
Their findings follow the same route back (same implementer by `SendMessage`); after any fix, re-run P-44 (gates), rebuild and reinstall (P-45) and repeat only the failed acceptance lines.
