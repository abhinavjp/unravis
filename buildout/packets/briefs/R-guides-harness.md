# Research brief R2: official docs on three agent harnesses

Do not explore the repo. Use web search and fetch only. Primary sources only (project docs, README, official site). Paraphrase; at most one quote under 15 words per source. If a source cannot be fetched, say so in the file and do not invent.

Harnesses: (1) Manus (agent product and its API or CLI, how tasks are prompted), (2) pi, the minimal terminal coding agent by Mario Zechner (pi-mono / pi coding agent; its system prompt, tools, print or RPC mode, how to prompt it), (3) Kilo Code (CLI and agent modes, how tasks are prompted headless).

Each guide file starts with source URLs and fetch date 2026-10-29, then `## What the harness does` (headless run mode, tools it uses, permissions) and `## How to prompt it for small blind-implementable work packets` (packets are markdown with exact strings; the model returns SEARCH/REPLACE edit blocks and new files in one reply capped near 16K output tokens; no exploration wanted; include a 10-line prompt skeleton and known failure modes such as empty replies).

## Output
Reply with one block per guide, exactly this format and nothing else besides the final line:
### guides/harness-manus.md
```
<file content>
```
(then `### guides/harness-pi.md`, `### guides/harness-kilo.md`). Each file at most 800 words. Last line: `Status: PASS|FAIL`.
