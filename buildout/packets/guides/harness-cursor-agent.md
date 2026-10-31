# Guide: Cursor agent CLI and Composer 2.5 (lanes: cursor-composer-2-5, cursor-composer-25-fast, cursor-default, cursor-grok-47-high)

Fetched 2026-10-29:
- https://cursor.com/docs/cli/headless (vendor; docs.cursor.com redirects here)
- https://cursor.com/blog/agent-best-practices (vendor)
- Composer 2.5 announcement: cursor.com blog NOT retrieved. WebSearch found only secondary coverage (The New Stack, The Decoder, Handy AI): released 2026-05-18, built on a Moonshot Kimi K2.5 base with Cursor's own RL, available only inside Cursor IDE/CLI (no public API), no system card. Treat model facts as secondary.
- No official Composer-specific prompting guide found. Rely on the Cursor agent best-practices post.

## What the harness does
- `agent -p` (print) runs non-interactively. Without `--force` (alias `--yolo`) it only proposes changes and does not edit. `--output-format text|json|stream-json` (json puts the answer in `.result`). Auth via `CURSOR_API_KEY`. Forum reports (unofficial): headless cannot disable tools, no JSON-schema responses, occasional hangs on exit.

## How to prompt this lane for small blind-implementable work packets

- Be precise: name files, patterns and constraints (the vendor's own top rule). Example: which test pattern to follow, what to avoid.
- Do not tag or dump irrelevant files; extra context distracts the agent. Give only the files the packet touches.
- Give a verifiable target (tests, typecheck, linter) and tell it to iterate until green; for test-first work, write tests, confirm they fail, then implement without editing the tests.
- Long threads accumulate noise; one packet per fresh run. If the agent repeats a mistake, restart with a refined packet instead of patching with follow-ups.
- Plan-then-build helps for open work; for blind packets the plan IS the packet, so skip plan mode.
- Composer is fast and agentic, tuned inside Cursor's own tool harness; expect it to start with search/read tool calls. State "return SEARCH/REPLACE blocks in the reply; do not edit files" if the contract is text output, and omit `--force`.
- Composer-2.5-fast trades quality for speed: observed 24h 265 ok / 149 failed on cloud (see README). Use it for small, low-risk packets; use the standard composer for anything with logic.
- cursor-grok-47-high is Grok 4.7 inside Cursor: see grok-4-x.md; observed 23 ok / 231 failed, so it is a poor implementer lane right now.
- Keep rules short: key commands, conventions, pointers to canonical example files; do not paste long style guides.

Known failure modes
- Without `--force` nothing is written; with `--force` it edits directly, which breaks a text-only block-return contract.
- Forum-reported hangs and tool-use detours; set a timeout in the hub.
- No public documentation of output caps for Composer; assume the same 16K ceiling and keep packets small.
