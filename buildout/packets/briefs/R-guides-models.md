# Research brief R1: official prompting guides for four model families

Do not explore the repo. Use web search and fetch only. Primary sources only (vendor docs, model cards, official blogs or cookbooks). For each family below find the vendor's official guidance on prompting and agentic or coding use, then write one condensed guide. Paraphrase; at most one quote under 15 words per source. If a source cannot be fetched, say so in the file and do not invent guidance.

Families: (1) Moonshot Kimi K3, (2) Zhipu GLM-5.3, (3) DeepSeek V4.1 Flash (also cover V4 guidance if V4.1 has none), (4) xAI Grok 4.x (4.6 and 4.7 if documented).

Each guide file starts with the source URLs and fetch date 2026-10-29, then the section `## How to prompt this model for small blind-implementable work packets`. Packets are markdown with exact strings; the model returns SEARCH/REPLACE edit blocks and new files in one reply capped near 16K output tokens, ES5 JavaScript, no tool exploration wanted. Cover: prompt structure that works, instruction placement, whether to give examples, reasoning or thinking settings and their effect on obedience, verbosity control, output-format tips, known failure modes (empty replies, truncation, ignoring constraints, over-exploration), and a 10-line prompt skeleton.

## Output
Reply with one block per guide, exactly this format and nothing else besides the final line:
### guides/kimi-k3.md
```
<file content>
```
(then `### guides/glm-5-3.md`, `### guides/deepseek-v41-flash.md`, `### guides/grok-4.md`, same format). Each file at most 900 words. Last line: `Status: PASS|FAIL`.
