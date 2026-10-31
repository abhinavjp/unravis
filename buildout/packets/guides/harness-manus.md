Sources (fetched 2026-10-29):
- https://manus.im/docs/integrations/manus-api
- https://open.manus.ai/docs/v2/introduction
- https://open.manus.ai/docs/v2/agents-overview
- https://open.manus.ai/docs/v2/task.create

## What the harness does
Manus is a hosted autonomous agent (web and mobile app) plus a REST API at https://api.manus.ai. Headless runs are tasks created programmatically; these pages document no local CLI. Flow: POST /v2/task.create with a message, poll GET /v2/task.listMessages, continue with POST /v2/task.sendMessage, end with POST /v2/task.stop. Every account has a default IM agent whose main task is reachable through the shortcut agent-default-main_task; agent.list/detail/update manage named agents. The agent plans, executes multi-step workflows, uses tools, browses the web, and returns reports, presentations, websites, or data analysis. Per-task configuration covers connectors (connector IDs), skills (enable_skills/force_skills), task_references, project_id, locale, and a structured_output_schema. Tools and permissions: connectors and skills are the explicit tool surface; interactive_mode defaults to false so the agent proceeds best-effort without asking, and task.confirmAction exists when a run does need confirmation. Auth is x-manus-api-key or an OAuth bearer token with create_task/manage_all_tasks scopes. Responses wrap results as ok/request_id/error.code (invalid_argument, permission_denied, rate_limited, and so on).

## How to prompt it for small blind-implementable work packets
Manus is a general autonomous agent, not a diff emitter, and the docs describe no SEARCH/REPLACE wire format. Send the packet text as message.content, then either cap output with a structured_output_schema whose required fields carry the edit blocks, or instruct it to return the blocks verbatim as its final message. Because the agent plans and can browse, forbid exploration explicitly or it will explore.

Skeleton (10 lines):
1. Task: produce code edits only; do not browse, run tools, or ask questions.
2. Do not explore any repository. Everything needed is in the packet below.
3. Return SEARCH/REPLACE blocks and any new files in a single reply.
4. Use the exact strings from the packet; copy old text verbatim, including whitespace.
5. One block per edit; write new files as full content.
6. Do not summarise and do not add commentary.
7. If an anchor is missing, return an empty reply rather than a guess.
8. Keep total output under 16000 tokens.
9. Packet: <paste markdown packet>.
10. Reply now with only the blocks.

Known failure modes: empty or summary-only replies (the task ends in narrative, not blocks); input over roughly 5,000 estimated tokens per request returns InvalidArgument (HTTP 400); unwanted exploration, connector calls, or sub-tasks; polling before completion returns no final assistant text; interactive_mode true causes clarifying questions; async title leaves the placeholder "generating title..."; OAuth clients silently drop non-App skill IDs.

### guides/harness-pi.md
