# Prototype pipeline

Owner rule (nachi, 27 Nov 2026): nachi must never be the one who finds the confusing parts of a prototype. A prototype reaches him only after research, the auto gate, a self-check and a first-time-user review.

Docs and process only. This file changes no app UI. UI rule from the project still holds: prototype first, nachi approves, then real UI.

## Steps (all mandatory, in order)

| # | Step | Who / model | Output |
|---|------|-------------|--------|
| 0 | Brief: one line goal. Read [KNOWN-CONFUSIONS.md](KNOWN-CONFUSIONS.md) and treat every line as a must-check. Look for a saved research note for this screen type. | author | the goal line |
| 1 | Research, ONE pass per SCREEN TYPE | author, Sonnet, medium effort | `docs/prototypes/research/<screen-type>.md` (max 1 page), e.g. `ci-run-page.md`; the prototype gets a one-line "Visual style" note |
| 2 | Build the prototype (Prototype skill, real data; design with Impeccable and AI Slop Detector) | author, Sonnet | the HTML file |
| 3 | Auto gate (cheap, deterministic, before any model reviewer): (a) `node tools/prototypes/precheck.mjs <file>`, (b) Impeccable, (c) AI Slop Detector. Loop until nothing is open. | author, on the PC | `docs/prototypes/reviews/<slug>-design-gate.md`, one section per run |
| 4 | Self-check: the [UX-CHECKLIST.md](UX-CHECKLIST.md) boxes not marked [auto] | author | fixed prototype, boxes ticked in the review file |
| 5 | Goldfish round 1: ONE combined reviewer (newbie story, text budget, scan test, ADHD scan, balance) | a fresh agent, Sonnet, medium effort, no context | `docs/prototypes/reviews/<slug>-r1.md`, header line "Cost: N tokens, M min" |
| 6 | Triage and fix (fixed, or rejected with a one-line reason; one in, one out), then rerun step 3 on what changed | author | triage table, updated design-gate file |
| 7 | Goldfish round 2, ONLY IF round 1 had at least one blocker or 3 or more majors, or the fixes changed more than about a third of the screen. Otherwise skip it and say so in the delivery note. | a NEW fresh agent, medium effort | `docs/prototypes/reviews/<slug>-r2.md` |
| 8 | Final auto gate (step 3 once more, so late edits cannot break the budget or add dead buttons), then deliver (see "Step 8: delivery") | author | final prototype shown to nachi |

Stop rule: no third review round without asking. After round 2 fix what is left. If a blocker or major finding cannot be fixed, say so in the delivery message.

Cost rules: Sonnet everywhere, no Opus. One research pass per screen type. One reviewer per round. Max 8 screenshots per reviewer run. Skip no step because the prototype is small; a small prototype just makes each step short.

## Step 1: research

Save the note per screen type, not per prototype. If `docs/prototypes/research/<screen-type>.md` exists and is under 30 days old, reuse it and research only what is new. Otherwise do the one pass.

Search the web for 5 to 8 similar apps, screens or published redesigns for the same job. For a release manager or CI screen: Buildkite, GitHub Actions, GitLab, CircleCI, Vercel, Octopus, Azure DevOps, Argo CD, TeamCity. For other screens pick the closest products.

The same pass also covers low-cognitive-load and ADHD-friendly design principles (see [Default visual style](#default-visual-style-adhd-friendly)). Do not run a second search for this.

The note has five short parts:

1. Goal and real data.
2. What similar screens do, with links. One line each.
3. Patterns to copy, and why they fit our user.
4. Patterns to avoid, and why.
5. Visual style: one line saying how the prototype applies the ADHD-friendly defaults below.

Keep the note under one page. It guides the build; it is not shown to the reviewer.

Research basis for this pipeline itself (why the steps look like this):

- [Krug's trunk test](https://concepts.dsebastien.net/concept/trunk-test/): a person dropped on any page should answer: what site is this, what page, what are the main sections, where am I, what can I do here. This is where our six story questions come from.
- [Cognitive walkthrough](https://ixdf.org/literature/article/how-to-conduct-a-cognitive-walkthrough) ([four questions](https://medium.com/@userfocus/the-4-questions-to-ask-in-a-cognitive-walkthrough-55d547a0644a)): will the user try the right thing, notice it, connect it to their goal, and see progress after. This is where "what do I do next" comes from.
- [UXAgent](https://arxiv.org/html/2504.09407v3) and [Synthetic Cognitive Walkthrough](https://arxiv.org/html/2512.03568): LLM agents acting as a persona can find real usability problems cheaply, but they miss some and invent some. So the reviewer is a filter before nachi, not a replacement for him.
- [Buildkite build page redesign notes](https://buildkite.com/resources/changelog/290-the-next-phase-of-the-build-page-experience): CI run pages work best when the failing step and its first error come first.

## Step 3: auto gate

Run, in this order, and fix until nothing is open:

1. `node tools/prototypes/precheck.mjs <file.html> [--out <dir>]`. Plain node and Playwright, no network. It prints PASS, WARN or FAIL with the exact element text and exits 1 on any FAIL. It checks: visible words on the first screen at 1440x900, label length, sentence length, console and page errors, horizontal overflow at 390x844 and 1440x900, every button and link (dead ones fail; changes outside the viewport warn "hidden-state"), and contrast when axe-core is installed. Mark a deliberately inert element with `data-proto-inert`. It also writes first-screen screenshots and `visible-text.txt` for the author.
2. Impeccable (`~/.claude/skills/impeccable`, on the PC): design critique and audit of the rendered page (its `critique` and `audit` references). Its `impeccable detect` command is a bundled rule checker; the installed copy has no launcher (`scripts/impeccable`), so `detect` cannot run offline here. Do not fetch one with `npx` unreviewed. Use the critique and audit read-through instead, and run `detect` only if nachi installs the launcher.
3. AI Slop Detector = the `audit-ai-design-slop` skill (`~/.claude/skills/audit-ai-design-slop`, MengTo/Skills, text only: no scripts, no downloads, read-only). Run it on the rendered page at 1440 and 390 and keep its P0 and P1 findings in the design-gate file. This is the named detector; to swap it for another, change this line and the skill file together. The Impeccable `detect` rules are NOT this detector. The `the-antislop` skill checks writing, not UI, so it is not used here.

If a skill is not installed, search for it, read every file in it, install it only if it is text only (no scripts, no network), then use it. Do not skip a skill because it is missing.

Write the findings and what was done about each (fixed, or rejected with one line) in ONE file per prototype, `docs/prototypes/reviews/<slug>-design-gate.md`, with one section per run of the gate. Run the gate after the build, again after every fix round, and once more before delivery (step 8). A goldfish review does not start while the gate has open findings.

## Step 4: self-check

Run [UX-CHECKLIST.md](UX-CHECKLIST.md), including its "Text budget" and "Visual style" sections. Boxes marked [auto] are covered by the precheck; do not re-check them by hand. Fix everything the rest catches. The goldfish should spend its time on what you could not see.

## Steps 5 and 7: the goldfish review

Use [GOLDFISH-REVIEW-PROMPT.md](GOLDFISH-REVIEW-PROMPT.md). One combined reviewer covers the newbie story, text budget, scan test, ADHD scan and balance. Rules:

- Spawn a separate agent (Agent tool, Sonnet, medium effort, not a fork). Never review in your own context.
- Give it ONLY the filled prompt: the file path, and the three user questions for this screen. Do not give it the research note, the brief, earlier reviews, the conversation, or an explanation of the product. Any extra sentence makes it less of a goldfish. In round 2 the author may add only the NAMES of the states that changed, so the reviewer spends its effort there; it must still follow the whole story.
- The reviewer reads the rendered text (innerText) and uses screenshots sparingly: the first screen at 1440 and 390, plus failure states, 8 at most.
- The reviewer judges balance, not only clarity: "does this scan visually, or read as a wall of text?" (the scan test in the prompt).
- Reviewing is read-only and may run in the cloud or on the PC. Building and fixing run on the PC (project rule: delegate and hub-delegate).
- Save the reviewer's reply unchanged as the review file. Add one header line above it: "Cost: N tokens, M min" (from the agent's completion note) so we can calibrate. Do not edit findings you dislike. Reject them in the triage table instead.
- Triage table at the bottom of each review file: finding id, fixed / rejected, one line what changed or why not.

Why a new agent in round 2: a reviewer that already saw the screen remembers it. Round 2 checks that the fixes work for someone seeing it fresh.

## Balance: clear, not wordy

Reviews that only ask for clarity push authors to explain everything inline, and the screen turns into a wall of text (this happened on the Homestretch run page). Clear and short must hold together.

- **One in, one out.** A fix may not add text without removing or shortening text elsewhere.
- **Fix order for a confusing item:** (1) remove or rename it; (2) show it as an icon, a number or a short chip; (3) put the explanation behind a "?" (tooltip or small popover) or in a collapsed section; (4) add inline text only as the last resort.
- **Jargon help always goes behind a "?"**, never inline.
- **Text budget** (starting values; adjust with experience and record each change here):
  - First screen at 1440x900: at most 120 visible words.
  - A card or row label: at most 8 words.
  - A banner: one sentence, at most 20 words.
  - Every sentence over 20 words is cut or collapsed.
- **Count after each fix round.** The precheck prints the visible words of the first screen; the author writes "words on first screen: N" in the triage of the review file. It must not grow versus the previous round without a reason.

## Default visual style (ADHD-friendly)

Every prototype uses this style unless nachi says otherwise (nachi, 27 Nov 2026: not a monotonous wall of text).

- One focal point per screen.
- At most 1 to 3 attention items at once.
- Icons and shapes plus colour, never colour alone.
- Progress bars, step flows and diagrams over sentences.
- Numbers over words.
- Chunked cards with generous space.
- Progressive disclosure: detail collapsed, one click away.
- Consistent patterns: the same thing looks and sits the same everywhere.
- Calm: no monotone block of same-weight text.

The reviewer judges "does this scan visually, or read as a wall of text?". The checklist section "Visual style" is the self-check.

## Honest limits

A model is not a real newbie. It knows what CI and release managers are, even when told to forget. Two guards:

- The prompt makes it flag every time it understood something only because of outside knowledge.
- nachi stays the final judge. When he finds a confusion the reviewer missed, add one line to [KNOWN-CONFUSIONS.md](KNOWN-CONFUSIONS.md) and, if it was a missing check, add it to the checklist or the prompt. The pipeline improves from his corrections.

## Step 8: delivery

1. Run the final auto gate (step 3) and record it in the design-gate file.
2. Open the final HTML in Chrome on nachi's main screen (open it yourself; never tell him to run a command).
3. Keep a copy on his Desktop (clickable).
4. Copy to `/mnt/project-files/prototypes/<slug>/` and attach it to the reply.
5. In the reply give the full file path, then a short summary: what research changed, what the reviews found and what was fixed, what is still open, and whether round 2 was skipped and why. No more than 100 words, then the link.
6. Keep the research note and review files in the repo under `docs/prototypes/`.

## Definition of done

- The precheck is clean.
- Impeccable and AI Slop Detector have no open findings.
- The last review's findings are all triaged.
- No blocker is open.
- Text-budget numbers are recorded ("words on first screen: N" per round).
- Delivered per step 8.
- KNOWN-CONFUSIONS.md was read at step 0 and is updated if nachi finds something.

## What it costs

| Part | Cost per prototype |
|------|--------------------|
| Research | once per screen type; reused for 30 days |
| Precheck, Impeccable, AI Slop Detector | about free (a script plus two skills) |
| Goldfish round 1 | one reviewer run: measured about 80k subagent tokens and 3 minutes on the run page (effort low then) |
| Goldfish round 2 | only when needed (see step 7) |

All other numbers are unmeasured until the "Cost:" header lines in the review files fill in.

## What we cut (and why)

- Separate agents for story, text and ADHD checks: one combined reviewer does it.
- A fixed second round: now conditional.
- A research note per prototype: now per screen type.
- Handing the reviewer the whole HTML: it renders the page instead.
- Per-round design-gate files: one file per prototype.

Kept: a fresh agent each round, the goldfish isolation rules, Sonnet only.

## Using it for the real UI

After nachi approves a prototype and the real UI is built, run the same goldfish prompt on real screenshots (see `tools/uiaudit`) before the PR is called done. Same rules.

## Skill

The skill form is [skill/SKILL.md](skill/SKILL.md). Install it by copying it to `.claude/skills/prototype-pipeline/SKILL.md` (that folder is gitignored, so it is not tracked).
