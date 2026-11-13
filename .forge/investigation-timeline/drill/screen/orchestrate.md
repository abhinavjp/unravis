# Orchestrator runbook: Slow call screen

For the ORCHESTRATOR (a Fable agent). You plan nothing new and write no product code: you start implementers and reviewers, collect proof, decide go / no-go per wave, and report to the owner. Read first: `plan.md`, `graph.md`, `reviews.md`, then `~/.claude/model-allocation.md`, `~/.claude/hub-model-prompting.md`, `~/.claude/runner-hub.md`.

## 0. Hard rules (apply to you and go into every prompt)

- No commit, stash, reset, checkout, revert, tag, release, push or merge. The tree has many unrelated uncommitted changes: they are not yours and not the implementers'.
- No credentials, tokens, user names, host names or record text in logs, fixtures, reports or prompts. Never read `.env` or `sql/`.
- No write to the customer database. Never switch Query Store on.
- An implementer edits only the files in its packet's Write scope.
- Always set `model` explicitly when starting a subagent. Opus only for RB-4 and P-43.
- "Done" means the real data path works. Never report a demo-only result as done; say "live-verified" or "demo-only-verified".
- Ask the owner only the questions in section 9; do not stop the run for them unless marked BLOCKING.

## 1. Before wave W0

1. `git status --short | wc -l` and `git rev-parse HEAD` → write both to `screen/reviews/run-log.md` (the baseline; you will compare at the end).
2. Baseline gates (one Haiku agent, read-only): `cd web && npm test` (expect 2779 pass, 0 fail) and `go build ./...`. If the baseline is red, STOP and tell the owner: the plan assumes a green start.
3. Check the installed app is running with a live source (needed by P-01, P-40, P-46). If not: run everything else, mark P-01 / P-34 / P-36 / P-38 as "skipped: no live source" and tell the owner at the end.
4. Usage: check the weekly and 5-hour limits. Plan about 50 implementer runs + about 9 reviewer runs.

## 2. Where implementers run

Per the owner's standing preference: Runner Hub first (remote hub first, local hub at http://127.0.0.1:8080 as fallback; lanes and limits in `~/.claude/runner-hub.md`), local subagents when a packet needs this session's tools: the built-in browser (P-32 measuring, P-33 browser check, P-46, P-47), the live app (P-01, P-40) or `SendMessage` continuity for review fixes. For a hub lane, translate the model line of the packet with the lane table in `~/.claude/hub-model-prompting.md` and keep the prompt template below unchanged.

## 3. Wave order

Run the waves in order. Inside a wave start at most 6 agents at a time (5-hour limit pacing); start the next as one finishes. A wave is closed when every packet in it reported all Proof items ticked (or "skipped" with the allowed reason) and the wave gate passed.

| Wave | Packets | Wave gate (you run it, or one Haiku agent) |
|---|---|---|
| W0 | P-01, P-02, P-03, P-04, P-05, P-06, P-08, P-09, P-10, P-11, P-12, P-13, P-14, P-15, P-16, P-40 | `go build ./... && go vet ./...`; `cd web && npx svelte-check --threshold error && npm test` |
| W1 | P-07, P-17, P-22, P-23, P-24, P-26, P-27, P-28, P-30, P-31, P-35 | same; then start RB-1 |
| W2 | P-18, P-25, P-29, P-34, P-37 | same |
| W3 | P-19, P-32, P-36 | same; then start RB-5 |
| W4 | P-20, P-33, P-38 | same + `cd web && npm run build`; then start RB-4 (Opus) |
| W5 | P-21, P-41 | Go gate; DRILL-01..03 pass; then start RB-2 |
| W6 | P-39, P-42 | Go gate + `go test ./internal/server/ -count=1`; SCALL-01..06 pass; then start RB-3 |
| W7 | P-43 (Opus, RB-6), P-44 | all review batches closed; `gates.md` all PASS; `RB-6-security.md` VERDICT: SHIP |
| W8 | P-45 | installed version is rc.35 |
| W9 | P-46 | `acceptance.md` complete |
| W10 | P-47 | `clumsy.md` complete; no open blocker |
| W11 | P-48 | backlog diff checked by you |

Parallel-run rule: implementers share one working tree. Inside a wave each implementer runs ONLY its own test file / its own package tests (the commands in its Proof). The full `npm test` and full Go tests are the wave gate, run after the wave. Exceptions that run the full web suite themselves: P-02, P-23, P-32, P-33 (their packets say so); run P-23's full suite after the other W1 web packets have finished, or accept a red caused by a half-written neighbour and re-run.
Go: packages compile together. If `go build ./...` fails for an implementer because another package is mid-edit, it waits 2 minutes and retries twice before reporting.

One-writer chains (never start the next before the previous is closed): server P-14 → P-18 → P-19 → P-20 → P-21 → P-39; store P-17 → P-34 → P-36 → P-38; audit P-13 → P-35 → P-37; `SlowCallScreen.svelte` P-25 → P-32.

## 4. Implementer prompt template

Fill the `<…>` parts from the packet file. Paste the packet's sections verbatim; do not summarise them. For a Haiku packet (P-03, P-11, P-44) keep the two HARD LIMITS blocks; for Sonnet drop the second one.

```
HARD LIMITS (read first)
- Edit only the files listed under FILES YOU MAY EDIT. Touch nothing else.
- Do not commit, stash, reset, checkout, revert, tag or push. Other uncommitted changes in the tree are not yours: leave them.
- Do not spawn subagents. Do not read .env or the sql/ folder. Print no token, password, user name or record text.
- If something does not match what this prompt says (a symbol, a line, a test), STOP and report; do not improvise.

ROLE
You are the implementer of packet <P-NN title> in the repo D:\Projects\api-audit-explorer (Unravis: Go backend, Svelte 5 web UI).

GOAL
<the packet's Outcome, verbatim>

CONTEXT
- This is one small packet of a larger plan (a full-page "Slow call" screen). Other packets build the other parts; you build only this one.
- Packet file: D:\Projects\api-audit-explorer\.forge\investigation-timeline\drill\screen\packets\<file>. Read it fully before you start; it is the source of truth.
- Go toolchain: export PATH="$PWD/.dev/toolchains/go/bin:$PATH". Web tests: cd web && node --test tests/<file>.test.cjs. New .cjs tests copy the loader preamble from web/tests/drill.copy.test.cjs lines 1-24. .svelte files are tested as source text.
- Copy rules: plain words for a first-time investigator; never use: because, due to, caused, waiting on, blocked by, led to, results in, responsible for, leak, starve, saturate, proven. A missing number is shown as "not recorded", never 0.
- Other agents are editing other files in the same tree right now. Run only the tests named below.

FILES YOU MAY EDIT
<the packet's Write scope, verbatim>

FILES TO READ ONLY
<files named in the packet's Changes that are not in the write scope; plus for web packets web/src/lib/drill/drillCopy.ts>

DO NOT
<the packet's Must not change, verbatim>

REQUIREMENTS
<the packet's Changes, verbatim, numbered>

TESTS
<the packet's Proof commands, verbatim>

OUTPUT CONTRACT
Reply with exactly these parts, at most 40 lines:
1. STATUS: DONE | BLOCKED | PARTIAL
2. PROOF: each Proof item copied as "- [x] <item> → <the evidence: last line of the command output, or the number>" or "- [ ] <item> → <why not>"
3. FILES CHANGED: paths (must be inside the write scope)
4. VERIFIED ON: live data | demo only | unit tests only
5. NOTES: anything that did not match the packet; anything you could not verify; follow-ups for the orchestrator
Tick every Proof item and report its evidence. Do not commit.

STOP CONDITIONS
- A symbol, line or test named in the packet is not there → STOP, report what you found instead.
- The change needs a file outside FILES YOU MAY EDIT → STOP, report which file and why.
- A test outside your packet fails and you did not touch its subject → report it, do not fix it.
- You are about to print or store a secret → STOP.

<Haiku only> HARD LIMITS (again): only the listed files; no commit; no subagents; when unsure, stop and report.
```

Model and effort: from the packet's first line. Local subagent type: `general-purpose` with `model` set (haiku / sonnet). Reviewers: `general-purpose` with `model: opus` or `sonnet` (or `voltagent-qa-sec:security-auditor` for P-43, which is read-only by its tools; then you write the findings file from its reply).
Keep each implementer's agent id in `run-log.md` (`P-NN | agent id | model | start | end | status`): review fixes go back to the same agent.

## 5. Collecting results

For each reply:
1. STATUS DONE and every Proof item ticked with evidence → check `git status --short` for files outside the packet's write scope that changed since the wave started (compare with the list you took before the wave). An out-of-scope change → send it back: "revert only your own out-of-scope edit by editing the file back; do not use git".
2. Copy the STATUS / VERIFIED ON lines into `run-log.md`.
3. Collect NOTES: a "did not match the packet" note may change a later packet. Update that later packet's prompt (not the packet file) and write the change in `run-log.md`.
Specific hand-overs to watch: P-14 note on the demo rollup `Sum` (if the demo does not fill it, add a step to P-35's prompt: fill `Sum` in the demo rollup, since P-35 is the next audit writer); P-01 result decides P-34 / P-36 / P-38; P-25 / P-32 "props do not fit" reports go to the child's implementer; P-32's measured lane-fit numbers go into P-42's and P-46's prompts; P-41's demo-exe build command goes into P-42, P-44 and P-47.

## 6. Failures

- BLOCKED by a stop condition: read the note; if the packet's anchor was wrong, correct the prompt with what the implementer found and send it back to the SAME agent (`SendMessage`). Two blocks on the same packet → move it up one effort level (sonnet medium → high); a Haiku packet that blocks once goes to Sonnet. Never move a build packet to Opus.
- PARTIAL / a red Proof item: same agent, one message: "finish item N; evidence required". At most 3 attempts per packet, then stop that chain, go on with packets that do not depend on it, and put it in the owner report.
- Wave gate red: find the failing test's owner by file (graph.md write scopes); send the failing output (last 30 lines) to that implementer. Do not let a different agent fix it.
- A packet turns out to need a file owned by another packet in the same wave: finish the other first, then run this one alone (it moves to the next wave; write that in `run-log.md`).
- P-01 blocked (no live source): mark P-34, P-36, P-38 "skipped"; P-39 still runs. Tell the owner: the Database / Redis / Web calls columns will say "not recorded" on live data until the probe can run.
- Security verdict FIX-FIRST: nothing goes to P-45 until fixed and re-reviewed (reviews.md).
- Anything that would need a destructive git command: do not do it; ask the owner.

## 7. Reviews

Start each batch at the point given in section 3, as described in `reviews.md` (prompt contents, findings format, 5-round cap, one fixer pass per round, re-run only unclear or failing reviewers). Reviewer prompt skeleton:

```
ROLE: reviewer for batch <RB-n name>. You review; you change no file except your findings file. Do not spawn subagents.
READ: <packet files>; plan.md sections "Rulings" and "Ground rules"; <extra reading from reviews.md>.
DIFF: <command from reviews.md>. The tree has unrelated uncommitted changes: review only what these packets describe.
CHECK: <the checklist lines for this batch from reviews.md>.
OUTPUT: write <findings path> in the format below, at most 60 lines. Each finding: id | packet | severity (blocker/major/minor/nit) | confidence (high/medium/low) | file:line | evidence | one-line fix. Then "UNCLEAR:" lines if any. Last line "VERDICT: SHIP" or "VERDICT: FIX-FIRST". If you find nothing, say so; do not invent findings.
```

Review work can overlap the next wave as long as the fixer and the running implementer do not write the same file (check graph.md); otherwise hold the fix until that packet reports.

## 8. Usage pacing

- 5-hour limit: at most 6 agents at once; start W0 with the six high-effort packets (P-06, P-14, P-15, P-16, P-13, P-02), then fill with the small ones.
- If the limit is close (your own check), finish the running wave, write `run-log.md`, and stop at the wave boundary; never stop inside a one-writer chain with a half-edited file.
- Weekly limit: Opus runs are exactly two full reviews (RB-4, P-43) plus at most one narrow re-review each. Re-reviews read only fixed lines.
- Good overnight stopping points: after W4 (the screen is mounted and demo-testable) and after W7 (gates + security done; P-45..P-48 need the owner's PC and live data anyway).

## 9. Owner report

After W4 (short) and at the end (full), write `screen/reviews/owner-report.md` and give the owner the same text. Plain words, at most 30 lines:
1. What works on LIVE data now (from `acceptance.md`, "Verified on live data").
2. What works on the demo only, and what is still owed for a live check.
3. What is not done, with the reason (blocked packet, skipped for no live field, review blocker).
4. Numbers: packets done / skipped / failed; tests (web pass count vs 2779 baseline; Go; e2e ids); security verdict; installed version.
5. What changed in the working tree (file count before / after from `run-log.md`); nothing committed, nothing tagged, nothing pushed.
6. Questions only the owner can answer:
   - Confirm layout W1 on the real screen (after seeing it), or pick another.
   - Is the Nearby calls / All sources split right as built?
   - Back uses the browser history without a visible address change: acceptable?
   - Overview strip covers one hour each side with N-minute columns: enough, or wider?
   - "Started late" and "history no longer kept" share one sentence, and the screen cannot say since when Query Store was read-only: acceptable?
   - BL-016: a live answer needs a database whose Query Store is off. Is one available? (Not BLOCKING.)
   - The screen never starts a database read by itself (it shows saved days, files, or the API's view): keep it so?
   - May the old pop-up be deleted (P-49)?
   - Go-ahead for the later items L-20..L-25, in which order?
7. Next step you recommend, one line.

## 10. End-of-run checks (you)

- `git rev-parse HEAD` equals the baseline; `git tag --list` unchanged; `git stash list` unchanged.
- Every file changed since the baseline is in some packet's write scope (graph.md table) or under `screen/`.
- `docs/BACKLOG.md`: every new tick has a "Verified on live data" line behind it.
- No file under `screen/` holds a token-like string: `Select-String -Path screen\**\*.md -Pattern '[A-Za-z0-9_\-]{40,}'` → nothing.
