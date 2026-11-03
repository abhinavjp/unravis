# TSK-28 human-comprehension-test

risk: low — a manual check by a person, not by a model.

## Outcome

A short script exists for a non-expert to follow, so the owner can confirm the greyed-out states are understood. The result is recorded by the owner.

## Write scope

- `.forge/charts-engine-hardening/phases/05-verification-and-docs/comprehension-test.md` (create)
- `.forge/charts-engine-hardening/phases/05-verification-and-docs/progress.md`

## Must not change

- Any other file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Create `comprehension-test.md` with exactly three prompts for the tester, each with a blank "answer" line:
   - "Open the wireframe's reference card with a quiet reference window. What do you think the grey state means?"
   - "Why is there no comparison number here? What would you do next?"
   - "Which of these two charts would you trust more, and why?"
   Add one line at the top: "Do not explain anything to the tester first. Write their words, not yours."
2. In `progress.md` under UNMEASURED write: `Human comprehension test: not run — needs a person; see comprehension-test.md.`

## Proof

The file exists with three prompts; `progress.md` has the UNMEASURED line.

## Depends on

TSK-27.
