---
name: prototype-pipeline
description: Mandatory process for every Unravis or Homestretch UI prototype. Research, build, auto gate, self-check, fresh first-time-user (goldfish) review, fix, then show nachi. Use before showing any prototype.
---

# Prototype pipeline

Read `docs/prototypes/PIPELINE.md` and follow it. In short:

0. Brief: one line goal. Read `docs/prototypes/KNOWN-CONFUSIONS.md` (every line is a must-check). Look for a saved research note for this screen type.
1. Research once per SCREEN TYPE (web, similar apps and screens; include ADHD-friendly low-cognitive-load principles). Save `docs/prototypes/research/<screen-type>.md`, max one page; reuse it if under 30 days old. The prototype gets a one-line "Visual style" note.
2. Build with the Prototype skill and real data. Design with Impeccable and AI Slop Detector.
3. Auto gate, before any model reviewer: `node tools/prototypes/precheck.mjs <file>`, then Impeccable (critique/audit read-through; its `detect` launcher is not installed, do not `npx` it), then the AI Slop Detector = the `audit-ai-design-slop` skill (text only, read-only; run it on the rendered page at 1440 and 390, keep P0/P1 findings). To swap the detector, change this line and PIPELINE.md step 3 together. Install a missing skill only after reading every file and finding no scripts or network use. Loop until nothing is open. Log findings and actions in ONE file, `docs/prototypes/reviews/<slug>-design-gate.md`, one section per run. Run the gate again after every fix round and before delivery.
4. Self-check with `docs/prototypes/UX-CHECKLIST.md`, only the boxes not marked [auto]. Check the text budget (first screen at most 120 words) and the visual style.
5. Goldfish round 1: ONE combined reviewer, a separate fresh Sonnet agent at medium effort (Agent tool, not a fork), with the prompt from `docs/prototypes/GOLDFISH-REVIEW-PROMPT.md`, filled with only the file path, three plain-words task questions and the round. Give it nothing else. It also judges balance (scan test: "scan or wall of text?"). Save the reply unchanged to `docs/prototypes/reviews/<slug>-r1.md` with a header line "Cost: N tokens, M min".
6. Triage every finding (fixed, or rejected with a one-line reason); fixes follow one in, one out. Rerun step 3 on what changed.
7. Round 2 with a NEW fresh agent, only if round 1 had a blocker, 3 or more majors, or the fixes changed about a third of the screen. Otherwise skip and say so. No third round without asking.
8. Final auto gate (step 3), then deliver: open in Chrome on nachi's main screen yourself, keep a Desktop copy, copy to `/mnt/project-files/prototypes/<slug>/`, give the full path, summary under 100 words.

Rules: Sonnet only, no Opus. Build and fix on the PC (delegate and hub-delegate); reviews are read-only. A prototype never reaches nachi before the gate and the reviews are done. When nachi finds a confusion anyway, add a line to `docs/prototypes/KNOWN-CONFUSIONS.md`.
