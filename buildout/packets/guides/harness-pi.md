Sources (fetched 2026-10-29):
- https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
- https://raw.githubusercontent.com/earendil-works/pi/main/packages/coding-agent/README.md
- https://raw.githubusercontent.com/earendil-works/pi/main/packages/coding-agent/docs/cli.md
- https://raw.githubusercontent.com/earendil-works/pi/main/packages/coding-agent/docs/rpc.md

## What the harness does
Pi is Mario Zechner's minimal terminal coding agent, split into pi-ai (unified multi-provider LLM API), pi-agent-core (tool loop), pi-tui, and the pi-coding-agent CLI. Headless run modes: `pi --print "..."` runs the prompt once, writes the final assistant text to stdout, and exits; `--mode json` streams JSONL events; `--mode rpc` reads JSONL commands on stdin and writes responses/events on stdout until shutdown. When stdin or stdout is redirected and neither JSON nor RPC is selected, Pi uses print mode. Default tools are read, bash, edit, and write; built-in grep, find, and ls exist too, and extension tools codemode and tool_search are off by default. `--tools` replaces the selection (allowlist) or adjusts it with +name/-name entries; `--exclude-tools` and `--no-tools` narrow further. Permissions: Pi runs YOLO by default, with full filesystem access and any command at your user privileges, no prompts or safety rails; pass `--tools read,grep,find,ls` for read-only exploration. The system prompt is deliberately tiny (under 1000 tokens with tool definitions) and replaceable via `--system-prompt` or `--append-system-prompt`; AGENTS.md is injected. There is no built-in to-do list, plan mode, background bash, or sub-agent tool.

## How to prompt it for small blind-implementable work packets
Use `pi --print` with the packet as the argument, or pipe the packet on stdin. State that no exploration is wanted; the packet carries exact strings, and the model returns SEARCH/REPLACE blocks plus new files in one reply, output capped near 16K tokens.

Skeleton (10 lines):
1. Task: emit code edits only; do not run tools or ask questions.
2. Everything needed is in the packet below; do not read other files.
3. Return SEARCH/REPLACE blocks and new files in a single reply.
4. Copy old text verbatim, including whitespace, into the SEARCH section.
5. One block per edit; express new files as full content.
6. No prose, no summary, no bash output.
7. If an anchor is absent, output nothing rather than guessing.
8. Keep the entire reply under 16000 tokens.
9. Packet: <paste markdown packet>.
10. Reply with only the blocks.

Known failure modes: empty stdout when the model returns no assistant text; a `prompt` RPC response only means accepted, so wait for agent_settled before assuming completion; RPC mode rejects @file arguments; JSON and RPC reserve stdout, so logs go to stderr; token and cost tracking is best-effort, and aborts on providers that report usage only at stream end lose accounting; unconstrained runs call bash to explore instead of staying blind.

### guides/harness-kilo.md
```
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
