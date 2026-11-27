---
name: prototype-pipeline
description: Mandatory process for every Unravis or Homestretch UI prototype. Research, build, self-check, fresh first-time-user (goldfish) review, fix, then show nachi. Use before showing any prototype.
---

# Prototype pipeline

Read `docs/prototypes/PIPELINE.md` and follow it. In short:

1. Research once (web, similar apps and screens; include ADHD-friendly low-cognitive-load principles and add a "Visual style" line). Write `docs/prototypes/research/<slug>.md`, max one page.
2. Build with the Prototype skill. Design with Impeccable and AI Slop Detector. Use real data.
3. Self-check with `docs/prototypes/UX-CHECKLIST.md`. Fix first. Check the text budget (first screen at most 120 words) and the visual style.
4. Goldfish review: spawn a separate fresh Sonnet agent (Agent tool, not a fork) with the prompt from `docs/prototypes/GOLDFISH-REVIEW-PROMPT.md`, filled with only the file path, three plain-words task questions and the round. Give it nothing else. The reviewer also judges balance (scan test: "scan or wall of text?"); fixes follow one in, one out. Save the reply unchanged to `docs/prototypes/reviews/<slug>-r1.md`.
5. Triage every finding: fixed, or rejected with a one-line reason.
6. Round 2 with a NEW fresh agent. At most two rounds.
7. Deliver: open in Chrome on nachi's main screen yourself, keep a Desktop copy, copy to `/mnt/project-files/prototypes/<slug>/`, give the full path, summary under 100 words.

Rules: Sonnet only, no Opus. Build and fix on the PC (delegate and hub-delegate); reviews are read-only. A prototype never reaches nachi before step 6 is done. When nachi finds a confusion anyway, add a line to `docs/prototypes/KNOWN-CONFUSIONS.md`.
