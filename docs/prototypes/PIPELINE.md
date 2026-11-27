# Prototype pipeline

Owner rule (nachi, 27 Nov 2026): nachi must never be the one who finds the confusing parts of a prototype. A prototype reaches him only after research, a self-check and a first-time-user review.

Docs and process only. This file changes no app UI. UI rule from the project still holds: prototype first, nachi approves, then real UI.

## Steps (all mandatory, in order)

| # | Step | Who / model | Output |
|---|------|-------------|--------|
| 0 | Brief: one line goal, who uses it, which real data feeds it | author | top of the research note |
| 1 | Research, ONE pass | author, Sonnet, medium effort | `docs/prototypes/research/<slug>.md` (max 1 page) |
| 2 | Build the prototype | author, Sonnet. Prototype skill; design with Impeccable and AI Slop Detector (on the PC; install if missing) | the HTML file, real data snapshot |
| 3 | Self-check with [UX-CHECKLIST.md](UX-CHECKLIST.md) | author | fixed prototype, checklist ticked in the review file |
| 4 | Goldfish review, round 1 | a separate fresh agent, Sonnet, no context | `docs/prototypes/reviews/<slug>-r1.md` |
| 5 | Triage and fix | author | every finding fixed, or rejected with a one-line reason |
| 6 | Goldfish review, round 2 | a NEW fresh agent (a reviewer who saw round 1 is no longer a goldfish) | `docs/prototypes/reviews/<slug>-r2.md` |
| 7 | Fix round 2, then deliver | author | final prototype shown to nachi |

Stop rule: at most two review rounds. After round 2 fix what is left. If a blocker or major finding cannot be fixed, say so in the delivery message; do not run a third round without asking.

Cost rules: Sonnet everywhere, no Opus. One research pass. One reviewer per round. Max 8 screenshots per reviewer run. Skip no step because the prototype is small; a small prototype just makes each step short.

## Step 1: research

Search the web for 5 to 8 similar apps, screens or published redesigns for the same job. For a release manager or CI screen: Buildkite, GitHub Actions, GitLab, CircleCI, Vercel, Octopus, Azure DevOps, Argo CD, TeamCity. For other screens pick the closest products.

The same pass also covers low-cognitive-load and ADHD-friendly design principles (see [Default visual style](#default-visual-style-adhd-friendly)). Do not run a second search for this.

The note has five short parts:

1. Goal and real data (from step 0).
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

## Step 3: self-check

Run [UX-CHECKLIST.md](UX-CHECKLIST.md) yourself, including its "Text budget" and "Visual style" sections. Fix everything it catches. The goldfish should spend its time on what you could not see.

## Steps 4 and 6: the goldfish review

Use [GOLDFISH-REVIEW-PROMPT.md](GOLDFISH-REVIEW-PROMPT.md). Rules:

- Spawn a separate agent (Agent tool, Sonnet, not a fork). Never review in your own context.
- Give it ONLY the filled prompt: the file path, and the three user questions for this screen. Do not give it the research note, the brief, earlier reviews, the conversation, or an explanation of the product. Any extra sentence makes it less of a goldfish.
- The reviewer judges balance, not only clarity: "does this scan visually, or read as a wall of text?" (the scan test in the prompt).
- Reviewing is read-only and may run in the cloud or on the PC. Building and fixing run on the PC (project rule: delegate and hub-delegate).
- Save the reviewer's reply unchanged as the review file. Do not edit findings you dislike. Reject them in the triage table instead.
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
- **Count after each fix round.** The author counts the visible words of the first screen and writes "words on first screen: N" in the triage of the review file. It must not grow versus the previous round without a reason.

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

## Step 7: delivery

1. Open the final HTML in Chrome on nachi's main screen (open it yourself; never tell him to run a command).
2. Keep a copy on his Desktop (clickable).
3. Copy to `/mnt/project-files/prototypes/<slug>/` and attach it to the reply.
4. In the reply give the full file path, then a short summary: what research changed, what the reviews found and what was fixed, what is still open. No more than 100 words, then the link.
5. Keep the research note and review files in the repo under `docs/prototypes/`.

## Using it for the real UI

After nachi approves a prototype and the real UI is built, run the same goldfish prompt on real screenshots (see `tools/uiaudit`) before the PR is called done. Same rules.

## Skill

The skill form is [skill/SKILL.md](skill/SKILL.md). Install it by copying it to `.claude/skills/prototype-pipeline/SKILL.md` (that folder is gitignored, so it is not tracked).
