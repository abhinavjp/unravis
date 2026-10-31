# Guide: Claude Opus 5 / 5.5 (lanes: droid-opus-5-5; cursor-opus-5-5-high is rate_limited)

Fetched 2026-10-29 (vendor primary sources):
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5
- https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/claude-prompting-best-practices

## How to prompt this lane for small blind-implementable work packets

Best use here: authoring packets, hard design review, final security review. Cost is high; do not burn it on mechanical edits.

Structure
- Opus performs best when handed the complete specification up front and left to run. One packet, all constraints, no progressive drip.
- Use tags (`<task>`, `<context>`, `<constraints>`, `<output_format>`); state the "why" for key rules.
- It completes full tasks instead of leaving stubs, so list exactly which files and edits are in scope.

Scope and verbosity
- It can widen scope and add steps. Constrain it: "Deliver what was asked at the scope intended. Make routine judgment calls yourself; ask only if readings would lead to materially different work. If the request looks mistaken, say so in one sentence and continue as asked."
- Default replies and written documents run long. Say "Keep responses brief" explicitly and, for files it writes, "match document length to what the task needs; no filler sections or redundant summaries". Lowering effort shortens thinking, not visible output.
- It narrates agentic work. For headless runs add: "No progress narration; lead the final message with the outcome."

Things to REMOVE from prompts for Opus
- Explicit verification steps ("add a final verification step", "use a subagent to verify") and "double-check your answer" cause over-verification and waste tokens; it self-verifies. Instead ask for a concrete check command run once.
- Do not ask it to write out its reasoning verbatim: may trigger a `reasoning_extraction` refusal. Ask for a short explanation or action summary.

Subagents
- Delegates readily. In headless packets say: "Do not delegate work you can finish in a few tool calls; do not use subagents to verify your work."

Effort
- Opus 5.5 default is `medium` and often matches Opus 5 at `high`; thinking is always on. `xhigh`/`max` only where measured gain; they lengthen turns. To get less thinking, lower effort, not prompt text.
- Unattended long runs: 5.5 can end a turn with a progress report and no tool call. Packets should state a completion condition and checklist so the harness can tell done from stopped.

Examples and format
- Examples still help format control: give one exact SEARCH/REPLACE example. Positive examples beat prohibitions.

Review use
- As reviewer: ask for all findings with confidence and severity and filter later; "only report high severity" makes it silently report less.
- Great different-family reviewer for a Sonnet/other-family implementer, but cost-limited; prefer for final review only.

Known failure modes
- Over-expansion of scope; over-verification; long prose; refusal on reasoning-extraction wording; thinking-disabled artifacts (tool call written as text, stray XML tags) on Opus 5 when thinking is off, so keep thinking on and lower effort instead.
