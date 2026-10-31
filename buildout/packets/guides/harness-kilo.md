Sources (fetched 2026-10-29):
- https://kilo.ai/docs/code-with-ai/platforms/cli
- https://kilo.ai/docs/code-with-ai/platforms/cli-reference
- https://kilo.ai/docs/customize/custom-modes
- https://github.com/Kilo-Org/kilocode

## What the harness does
Kilo Code is an open-source coding agent (a fork of OpenCode) shipped as VS Code and JetBrains extensions, a CLI (`npm install -g @kilocode/cli`), and a cloud service. Headless options: `kilo run --auto "message"` for non-interactive autonomous runs, `kilo run --format json` to emit raw JSON events, `kilo serve` for a headless server, `kilo acp` for an Agent Client Protocol server, and `kilo cloud start`. Modes are called agents: built-ins code, plan, debug, ask, orchestrator, explore, and general; custom agents are Markdown files with YAML frontmatter under `.kilo/agents/` or entries under the config `agent` key, with mode (primary/subagent/all), model, prompt, permission, steps, and temperature. Tools include read, edit, bash, glob, grep, task, webfetch, websearch, todowrite, todoread, lsp, and skill, plus MCP servers. Permissions resolve per tool to allow, ask, or deny with glob rules evaluated last-match-wins; `--auto` auto-approves everything not explicitly denied, while a non-interactive run without `--auto` auto-rejects permission requests and exits 1 if any were rejected. Exit codes are 0 success, 124 timeout, 1 error.

## How to prompt it for small blind-implementable work packets
Run `kilo run --auto --agent code --format json "<packet>"`, or define a throwaway agent whose prompt states the blind-edit contract and whose permissions deny bash and webfetch. Give the packet as the message and demand SEARCH/REPLACE blocks plus new files, no exploration, output under 16K tokens.

Skeleton (10 lines):
1. Task: return code edits only; do not run tools, browse, or ask questions.
2. Do not explore the repository; the packet below contains every needed string.
3. Emit SEARCH/REPLACE blocks and any new files in one reply.
4. Copy old text exactly, including whitespace, into the SEARCH part.
5. One block per edit; write new files as full content.
6. No summary, no commentary, no command output.
7. If an anchor is missing, output nothing instead of guessing.
8. Keep the whole reply under 16000 tokens.
9. Packet: <paste markdown packet>.
10. Reply with only the blocks.

Known failure modes: a run that ends without an assistant message exits 1 with "run ended without an assistant message; the model returned no output"; omitting `--auto` makes a headless run auto-reject approvals and fail; a timeout exits 124; in autonomous mode a follow-up question is answered by a canned "make the decision autonomously" message; `steps` caps tool rounds; overly broad permissions let the agent edit outside the packet.

Status: PASS
