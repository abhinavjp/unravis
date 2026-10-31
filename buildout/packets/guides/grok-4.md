Sources: https://docs.x.ai/overview ; https://docs.x.ai/developers/grok-4-7 ; https://docs.x.ai/developers/models/grok-4.7 ; https://docs.x.ai/developers/models/grok-4.6 ; https://docs.x.ai/developers/model-capabilities/text/reasoning ; https://docs.x.ai/developers/model-capabilities/text/structured-outputs ; https://docs.x.ai/developers/tools/function-calling ; https://docs.x.ai/developers/model-capabilities/text/generate-text ; https://docs.x.ai/developers/models ; https://docs.x.ai/build/overview ; https://docs.x.ai/developers/advanced-api-usage/regions
Fetch date: 2026-10-29

## How to prompt this model for small blind-implementable work packets

Cover Grok 4.6 and 4.7 together: both are documented as 500k-context text/image-input models for coding and agentic tasks, with function calling, structured outputs, and reasoning. Prefer the Responses API. Keep one system message for invariant rules and one user message for the packet; xAI notes chat models have no role-order limitation, so placement is flexible, but front-loading rules is still safest. For blind work, be explicit that there is no repository access: “use only the text below; do not call web, X, code execution, files, or custom tools.” If tools are enabled by the client, the model may use them; for a one-shot blind packet, omit all server-side tools.

Reasoning cannot be disabled on Grok 4.5/4.6/4.7. `reasoning_effort` defaults to `high`; `low` is for latency-sensitive agentic/simple tool use, `medium` for long-context/data analysis, `high` for very hard multi-step problems, and `xhigh` for maximum depth on 4.6+. For small blind packets, `low` or `medium` usually leaves more output budget and reduces overthinking; use `high` only when exact-string patching requires subtle control flow. Do not send `presencePenalty`, `frequencyPenalty`, or `stop` with reasoning models; xAI says those requests error. Grok 4.7 returns encrypted reasoning content on Responses API responses; do not treat it as final text.

Examples: include one compact exemplar of the SEARCH/REPLACE grammar. Grok docs emphasize structured outputs and tool schemas more than few-shot prompting; for mechanical packets, a strict output contract matters more than many examples. If the consumer is a parser, use `response_format` JSON schema where possible; xAI says supported schema features are guaranteed to match, with Draft 2020-12 working best and Draft-07 accepted. Otherwise demand fenced blocks and validate. Tool arguments are implicitly strict to the tool’s JSON schema; define name, description, and parameters precisely, execute locally, and return results.

Known failure modes: using forbidden sampling/stop fields with reasoning models causes request errors; `logprobs`/`top_logprobs` are unsupported on grok-4.20+ and silently ignored; Grok has no realtime knowledge unless search tools are enabled; wrong regional endpoint or unavailable alias can 404, and US endpoint currently lists only 4.6/4.7. No text output limit is stated for 4.7, but still cap near 16K in the prompt and client. Empty replies/truncation/over-exploration are not cataloged in fetched pages; mitigate by requiring the first bytes to be `### ` or `MISSING:`, disabling tools, lowering effort, and retrying with a smaller packet.

10-line prompt skeleton:
System: Blind coding engine. No tools/search/web/X/code execution. Answer only patches. ES5 JS.
System: reasoning_effort=low unless this packet is marked HARD; never output reasoning or prose.
User: GOAL: <one sentence>. ACCEPT: <one check>.
User: SCOPE: only files listed; no deps; no refactors; preserve APIs.
User: OUTPUT: ### path fences with SEARCH/REPLACE blocks or complete new files; <=16K tokens; one reply.
User: MARKERS: <<<<<<< SEARCH, =======, >>>>>>> REPLACE exactly.
User: EXAMPLE: one tiny valid block.
User: PACKET: exact anchors, old strings, new strings, new-file bodies.
User: If anchor missing: MISSING: <exact string> only.
User: Start final answer with `### ` and end after last fence.
