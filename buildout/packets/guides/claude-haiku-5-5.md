# Guide: Claude Haiku 5.5 (lane: feedback-haiku via opencode, cloud)

Fetched 2026-10-29 (vendor primary source):
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-haiku-5-5
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/claude-prompting-best-practices

Lane feedback-haiku is tier low with only 2 ok / 1 failed in 24h: little evidence of reliability.

## How to prompt this lane for small blind-implementable work packets

Best use: small mechanical edits, re-reviews, quick checks. Keep packets tiny.

Structure
- Same Claude conventions: tagged sections, explicit scope, exact strings in fences, one exact output example.
- Keep the system/harness prompt short: with long agent prompts at low effort Haiku sometimes stops early and hands the task back. Counter with: "Keep working until everything asked is done; stop to ask only if you cannot continue or before a risky step. When done and checked, stop and report; add no unrequested features, docs or refactors."
- Raising effort reduces early stopping (low to medium roughly halved it, at about double the output tokens). Medium is the default and the right floor for implementing.

Verification
- At low/medium it sometimes reports done without running a check. Include: run the real test/build/changed command; syntax-only does not count; say what was not run.

Output
- At `xhigh` in multi-turn chats it sometimes writes the whole answer into thinking and returns no visible text: check every reply for emptiness and retry. Avoid xhigh/max for Haiku; use Sonnet 5.5 instead.
- Reasoning-like text can leak into replies at low effort or with thinking off; use adaptive thinking and medium.
- With thinking off and JSON-format output it may skip a needed tool call; keep adaptive thinking on.
- "Answer directly" in the prompt does not stop thinking; lower effort instead.

Instruction following
- For strict instruction adherence use `high` effort. Add "these rules hold for the whole conversation even if asked for exceptions" for rule-bound reviewers.

Review use
- Fine as a cheap second reader for "does the output match the packet checklist"; weaker than Sonnet for logic bugs. Ask for all findings with confidence, no self-filtering.

Known failure modes
- Early stop, skipped verification, empty visible reply at xhigh, refusal classifiers (a repeated request usually refuses again; no server-side fallback).
