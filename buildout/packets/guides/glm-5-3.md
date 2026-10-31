Sources: https://docs.z.ai/guides/llm/glm-5.3 ; https://docs.z.ai/guides/overview/migrate-to-glm-new ; https://docs.z.ai/guides/capabilities/thinking-mode ; https://docs.z.ai/guides/capabilities/thinking ; https://docs.z.ai/guides/capabilities/function-calling ; https://docs.z.ai/guides/capabilities/struct-output ; https://docs.z.ai/devpack/resources/best-practice ; https://docs.z.ai/guides/overview/concept-param
Fetch date: 2026-10-29

## How to prompt this model for small blind-implementable work packets

Make the packet self-contained: system message carries persistent rules; user message carries task, exact anchors, file excerpts, and output schema. Z.ai’s coding-agent best-practice frame separates task context, project context, and environment context; for blind packets, collapse these into the prompt because the model cannot inspect the repo. State the working directory assumptions, allowed files, forbidden commands, and acceptance check. Keep the execution boundary explicit: no shell, no browsing, no dependency install, no formatting beyond requested hunks.

GLM-5.3 is text-only, with 1M context and 128K max output. Reasoning is forced: `thinking.type` must stay `enabled`; disabling is unsupported and requests can fail. Use `reasoning_effort` `low`, `high`, or `max`; default is `max`, and Z.ai recommends `max` for complex coding. For small blind edits, choose `high` when there are multiple exact-string edits and `low` only for trivial renames; forced thinking means you cannot save tokens by turning reasoning off. Migration guidance says invalid effort values error for GLM-5.3, old `disabled` configs must become `enabled` plus usually `low`, and regression tests should watch randomness, latency, and tool-stream completeness.

Use examples sparingly: one minimal SEARCH/REPLACE block is enough because the packet format is mechanical. Put output rules before the payload and repeat the stop condition at the end. For verbosity, say “return edits only; no rationale; no summary; no line numbers unless inside file content.” For structured integration, GLM structured output is JSON-mode oriented (`response_format` as JSON object); still validate in code. For tools, function calling uses `tools`, `tool_choice`, and JSON string arguments; with streaming enable `stream=true` and `tool_stream=true`, then concatenate `delta.tool_calls[*].function.arguments`. Interleaved thinking is default with tools; preserve thinking blocks and return them with tool results.

Sampling: migration notes list `temperature` default 1.0 and `top_p` default 0.95, and recommend tuning only one. For blind packets prefer lower randomness if your deployment allows it, but do not rely on sampling to enforce format; enforce with prompt contract and parser. Set `max_tokens` deliberately: 128K output exists, but your packet cap is near 16K, so say so and keep inputs small enough that reasoning does not crowd out edits.

Known failure modes: request failure from `thinking.type: "disabled"` or bad `reasoning_effort`; truncated JSON/edits when `max_tokens` is too small; incomplete streamed tool arguments if not concatenated; instability if both `temperature` and `top_p` are tuned; agent inconsistency when task/project/environment context is vague. Empty replies and systematic over-exploration are not cataloged in the fetched GLM-5.3 pages; mitigate by demanding a non-empty first marker and by omitting tools entirely for blind packets.

10-line prompt skeleton:
System: You are GLM-5.3 acting as a blind patch generator. No exploration, no tools, no prose.
System: thinking stays enabled; use only the supplied packet; ES5 JavaScript only.
User: GOAL: one sentence; ACCEPT: one observable check.
User: OUTPUT: only ### path fences containing SEARCH/REPLACE blocks or full new files.
User: LIMITS: one reply, <=16K output tokens, modify listed files only, no new deps.
User: FORMAT: <<<<<<< SEARCH / ======= / >>>>>>> REPLACE exactly.
User: EXAMPLE: one 3-line dummy block with markers only.
User: CONTEXT: exact strings, imports, and neighboring lines needed to match.
User: EDITS: numbered atomic changes with exact old and new text.
User: If any anchor is absent, emit MISSING_ANCHOR: <exact> and stop.
