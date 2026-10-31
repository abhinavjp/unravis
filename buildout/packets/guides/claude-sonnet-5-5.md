# Guide: Claude Sonnet 5 / 5.5 (lanes: droid-sonnet-5, battle-sonnet via opencode)

Fetched 2026-10-29 (all vendor primary sources):
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-sonnet-5
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-sonnet-5-5
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/claude-prompting-best-practices (general rules; 72 KB, read the sections on clarity, examples, XML, overeagerness, test-gaming, hallucination)

Notes: droid-sonnet-5 is "claude-sonnet-5"; battle-sonnet is "anthropic/claude-sonnet-5-5" through opencode. Effort and thinking are normally set by the harness or hub, not by the packet text.

## How to prompt this lane for small blind-implementable work packets

Structure
- Sonnet 5.x reads literally. It does not generalise an instruction from one item to the next and does not infer unstated requests. State scope explicitly ("apply to every file listed, not just the first").
- Put the whole task, intent and constraints in the first turn. Underspecified, drip-fed prompts cost both tokens and quality.
- Wrap content types in tags: `<task>`, `<context>`, `<files>`, `<constraints>`, `<output_format>`. Tags reduce misreading when exact strings and prose are mixed.
- Give the "why" next to a rule (one clause). The model generalises from the reason.
- Numbered steps when order matters; exact strings in fenced blocks.

Scope control (important for packets)
- 5.5 adds unrequested tests, docs and small helper files, more at higher effort. For SEARCH/REPLACE packets add: "Make only the edits listed. Do not add features, tests, files, docs or refactors that were not asked for; mention them at the end instead."
- At low/medium effort it may stop to ask or check in before finishing. Add: "Keep working until every listed edit is done. Only stop to ask if you cannot continue." Prefer raising effort one level over writing around it.
- Reviewer/subagent spawning at xhigh/max wastes cost; say "do not launch sub-agents or start extra review rounds".

Examples
- Examples are the most reliable format lever. For output format give 1 short, exact example of a SEARCH/REPLACE block (3-5 diverse examples only if the format is unusual). Positive examples beat "don't" lists.

Output format / length
- Length is calibrated to the task; add "Return only the blocks, no narration" to trim chatter. Lead with outcome if a summary is wanted.
- Max output is a hard cap that includes thinking at high/xhigh. Keep packets small enough that edits fit well under 16K tokens; if thinking is on, reduce scope further.

Effort and obedience
- Effort is respected strictly at the low end: low/medium narrow to what was asked (good for mechanical edits) but can under-think multi-step logic. Default `high` is the sane implementer setting; `xhigh` only for hard packets.
- Adaptive thinking is on by default. Telling it "think less" does not reliably work; change effort.

Review use
- For code review, do NOT say "only report high-severity" or "be conservative"; the model obeys literally and drops findings. Say: report every issue with confidence and severity; a later pass filters. If self-filtering, give a concrete threshold (e.g. "anything that could cause wrong behaviour or a failing check; skip pure style").

Verification
- Low effort sometimes reports done without running a check. Add: run the real check (tests/build/the changed command); a syntax-only check does not count; if it cannot run, say which and why.

Known failure modes
- `stop_reason: max_tokens` after long thinking gives a truncated answer; treat as failed and retry with more headroom or less scope.
- Tool name case slips (e.g. `bash` for `Bash`) are harness-handled.
- Safeguard classifier refusals (cyber, reasoning-extraction) return a refusal stop; do not ask it to write out its reasoning verbatim, ask for a short explanation instead.
- Not a first choice for hardest long-horizon work (Anthropic says use Opus there).
