Sources: https://platform.kimi.ai/docs/guide/kimi-k3-quickstart ; https://platform.kimi.ai/docs/guide/prompt-best-practice ; https://platform.kimi.ai/docs/guide/use-thinking-models ; https://platform.kimi.ai/docs/guide/use-reasoning-effort ; https://platform.kimi.ai/docs/guide/kimi-k3-tool-calling-best-practice ; https://platform.kimi.ai/docs/guide/response_format ; https://platform.kimi.ai/docs/guide/troubleshooting
Fetch date: 2026-10-29

## How to prompt this model for small blind-implementable work packets

Use one system message for durable rules and one user message containing the whole packet. Put role, output contract, and constraints first; put the exact strings to find and the replacement text after a clear delimiter such as XML tags or triple quotes. Kimi’s prompt guidance explicitly recommends clear instructions, delimiters to separate input parts, examples of the desired output when style is hard to describe, and explicit length limits. For edit packets, state: implement only the described change; do not explore; return only SEARCH/REPLACE blocks and full new files; ES5 JavaScript only; one reply; about 16K output tokens max.

Give one tiny example of the edit-block grammar, not a large solved task. Kimi says examples are efficient for style replication, but exact word counts are unreliable; constrain by structure: “at most N blocks,” “each block has SEARCH then REPLACE,” “no prose.” Ask for bullet/paragraph counts rather than exact words when length matters.

Kimi K3 is documented with long context, vision, tool calling, structured output, partial mode, tool choice, dynamic tool loading, and reasoning-effort controls. Treat reasoning as always available; configure top-level `reasoning_effort` as `low`, `high`, or `max` rather than old K2 `thinking` flags. For a blind packet, use `low` or `high` when the change is mechanical; reserve `max` for cross-file reasoning. Higher effort improves complex reasoning/code/tool use but costs latency and tokens; lower effort reduces drift and leaves more room for the answer. In multi-turn or tool flows, pass assistant messages back as-is, including `reasoning_content` and `tool_calls`.

Control verbosity in the prompt, not by hoping sampling will do it: “No explanation. No markdown outside edit blocks. If a SEARCH string is absent, emit `MISSING: <exact string>` and stop.” For strict machine parsing, prefer `response_format` JSON schema only when the consumer can parse `choices[0].message.content`; do not parse the whole response because thinking models may also return `reasoning_content`. Declare nullable unions for possibly missing data; Kimi warns that required single-type fields can invite fabrication or empty strings when information is absent.

Known failure modes and mitigations: truncation appears as `finish_reason=length`; estimate tokens, raise `max_completion_tokens`, or continue with Partial Mode. Empty replies are not cataloged for K3; still validate non-empty output and retry with a shorter packet. Ignored constraints are reduced by front-loading rules, using delimiters, and keeping one task per packet. Over-exploration/answering from memory in tool setups is documented; force retrieval on the first turn with `tool_choice: "required"`, then return to `"auto"`. Keep stable prefixes for cache hits; changing early tools can reduce cache hits, while changing `tool_choice` does not invalidate the prefix cache.

10-line prompt skeleton:
System: You are a blind implementation engine. No exploration, no questions, no prose.
System: Output only SEARCH/REPLACE edit blocks and full new files. ES5 JavaScript. One reply <= 16K tokens.
User: <task>One sentence outcome and acceptance check.</task>
User: <constraints>Modify only listed files; preserve public APIs; no dependencies; no tests unless asked.</constraints>
User: <format>### path\n```\n<<<<<<< SEARCH\nexact old text\n=======\nnew text\n>>>>>>> REPLACE\n```</format>
User: <example>One minimal block showing exact marker strings.</example>
User: <files>Relevant exact file contents or exact SEARCH anchors.</files>
User: <packet>Exact strings, edits, new-file bodies.</packet>
User: If exact SEARCH text is missing, output MISSING: <string> and nothing else.
User: Begin now; final answer must contain only the blocks/files.
