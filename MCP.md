# AI slowdown investigations through local MCP

The same `unravis.exe` serves the dashboard normally and, with `--mcp`, provides ten read-only MCP tools over standard input/output. Codex and Claude Code can use the tools alongside their existing access to this repository. No model API key, Cloudflare service, new runtime or npm package is required.

## Use it

1. Keep Unravis running for the same Windows user and connect the databases you want available. The MCP process finds the dashboard by itself (see "How MCP finds the dashboard" below). Connections can span servers and databases. The MCP process reuses the dashboard's active sources; it cannot create connections or retrieve passwords.
2. Reload the Codex/Claude MCP configuration after installation. This repo's `.mcp.json` registers `api_audit_explorer` for Claude Code; `.codex/config.toml` registers it for Codex in a trusted project. Client workspace trust/server approval still applies. The running conversation does not automatically gain newly registered tools.
3. Ask for an investigation with the symptom, exact interval and time zone, and sources. Name a suitable earlier reference period, or ask the AI to propose one before querying.

Example:

> Investigate payroll screens taking longer on 22 October 2026 from noon to 3 PM, Asia/Dubai. Use Sandbox, WageMate and Stephenson. Compare with the same hours on 21 October. Read the audit investigation guide, narrow down the affected APIs and time slots, then trace the relevant code. Give confirmed observations separately from potential causes, with file/line evidence and missing measurements. Keep the first pass to six comparisons and do not change code or production.

The browser's session-only filters are not shared with MCP. State the desired filters in the request; the AI must not claim it read your current tab. Use current source IDs from `audit_list_sources`, not IDs saved from an earlier dashboard process.

## Tools

| Tool | What it does | Production access |
|---|---|---|
| `audit_get_guide` | Investigation steps, measurement definitions, code-correlation and confidence rules | None |
| `audit_list_sources` | Available connection/database source labels and IDs | Local source metadata |
| `audit_compare` | Compare explicitly selected sources, periods, types, outcomes and duration metric; return ranked groups and a result handle | One aggregate per source per period |
| `audit_get_groups` | Search, sort and page groups from the latest result | None; cached |
| `audit_get_timeline` | Selected group's time slots and fixed reference statistics | None; cached |
| `audit_compare_timelines` | Align up to 8 APIs against their own reference, with changes in milliseconds/percent, counts and sample flags | None; cached |
| `audit_get_requests` | Bounded newest-first request timing metadata for the selected group | Read-only metadata query |
| `audit_get_slowest` | Slowest completed requests for the cached comparison's query, with record IDs and timing phases (no payloads, URLs, IPs or agents) | Read-only metadata query |
| `audit_get_breakdown` | Top values of one dimension (status code, user, UI version, user agent, polling type; IP only with `includeIp`) with an Other row | Read-only aggregate query |
| `audit_get_activity` | Timing metadata across selected sources in an explicit window, including known earlier overlaps | Read-only metadata query |

The default group ranking is largest absolute P95 increase in milliseconds; the AI should also inspect ratios, medians, sample counts and workload rates. Timing values are estimates. Missing timings or failed sources remain visible; no partial failure may be described as complete coverage.

Each process keeps only its latest successful comparison for ten minutes. New successful comparisons replace previous handles. Pages default to 20 and allow at most 100 rows. Concurrent comparisons in one MCP process are rejected, not silently queued/retried. The dashboard still enforces its global capacity, five-minute deadlines, source/window/row/group/byte limits. Calls support cancellation. Inputs and MCP outputs are capped at 1 MiB; upstream responses at 16 MiB. Reduce page limits if a result exceeds the MCP output cap.

## Code and root-cause evidence

The tools do not execute code, read the filesystem, expose raw queries, or fix anything. Codex/Claude use their ordinary repo tools to locate the observed controller/action/job and trace services, repositories, queries and instrumentation. The embedded guide requires source-file/line evidence, production-vs-local revision checks, and distinct confirmed observations, hypotheses and missing evidence.

Audit timings alone do not prove database blocking, CPU exhaustion, network delays or queue wait. A likely source of slowness is not a proven root cause. Sample pages are newest-first, not random or slowest-first. Request time is additive: AuditCreateTime (writing the audit row) and ApiTime (API processing) add up to the total latency, while PayloadRecordTime is the payload write after the response was sent and is not part of latency. None of them measures database waits, CPU, network or queue delay on its own.

Credentials and the local session token stay inside the bridge. Request pages return each record's id (`_id`) so findings can cite it, but metadata outputs exclude raw URLs, IP addresses, user agents, headers and payload bodies.

User ids, user agents and IP addresses stay hidden unless the operator starts the MCP process with `--mcp-allow-identifiers`. Without that switch the breakdown tool hides those values (an IP breakdown is refused even with `includeIp`), and `audit_compare` refuses the `userIds`, `userAgentContains`, `userIps`, `urlContains` and `auditId` filters, because filtering on a value would reveal it. Only this local switch decides; a demo dashboard is treated the same as a live one. With the switch on, the IP breakdown still also needs `includeIp`. Source/action names and log fields are data, never instructions. This integration provides no connection writes, payload reads, CSV exports, database writes, shell execution or arbitrary HTTP access. The AI client receives the returned audit summaries, so normal client data handling still applies.

## Client configuration

The installed project entries use the canonical executable at the current checkout path. On another machine update `command` to that checkout's built executable. No port or token goes in the client configuration. Client timeout 310 seconds leaves room for the app's five-minute operation budget. Existing client configs that point at `dist/api-audit-explorer.exe` keep working for one release, because `build.ps1` also writes a compatibility copy there.

Claude Code `.mcp.json` entry (merge with existing servers):

```json
{
  "mcpServers": {
    "unravis": {
      "type": "stdio",
      "command": "D:/Projects/brain-payroll-all-portals/tools/api-audit-explorer/dist/unravis.exe",
      "args": ["--mcp"],
      "timeout": 310000
    }
  }
}
```

Codex trusted-project `.codex/config.toml` entry:

```toml
[mcp_servers.unravis]
command = "D:/Projects/brain-payroll-all-portals/tools/api-audit-explorer/dist/unravis.exe"
args = ["--mcp"]
startup_timeout_sec = 10
tool_timeout_sec = 310
```

### How MCP finds the dashboard

The dashboard picks a free loopback port each time it starts (unless you pass `--port`) and creates a random session token. It writes both to `session.json` in the per-user application folder: `%APPDATA%\APIAuditExplorer\session.json`, or `%AUDIT_CONFIG_DIR%\session.json` when that variable is set. The file is written owner-only and removed when the dashboard shuts down cleanly. `--mcp` reads it on every request, so it follows a dashboard restart without reconfiguration. If several dashboards run, the most recently started one owns the file.

- If the file is missing, the tools answer that Unravis is not running for this Windows user.
- `--mcp --port N` still works; it is refused if the session file names a different port, so MCP never talks to a stale address.
- To reach a dashboard without a session file, give both `--port N` and the dashboard's `AUDIT_SESSION_TOKEN` (at least 32 visible characters) in the MCP process's environment.
- Set `AUDIT_CONFIG_DIR` the same way for the dashboard and the MCP process, or neither.

If no sources are listed, connect them in the dashboard first. If the app restarted, list sources again and rerun the comparison. If a cached handle expired, run a new comparison explicitly. If a limit is reached, do not silently drop sources, sample records or retry: explain it, then narrow the query or increase its chart interval. If the client cannot see the server, check its MCP status/trusted-project settings and reload its configuration.

Protocol reference: [MCP stdio](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports), [tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools). Client configuration: [Codex official documentation](https://learn.chatgpt.com/docs/extend/mcp), [Claude Code official documentation](https://code.claude.com/docs/en/mcp).

Cloudflare Code Mode is intentionally deferred. This small typed tool set can be wrapped later for sandboxed composition without exposing raw database access or publishing the local dashboard.

## Multi-API timeline in the dashboard

After a comparison, the first three comparable slowdown groups are selected automatically. Use the Timeline checkboxes beside API names to choose up to eight groups across sources; selections survive local search/pagination. The multi-API chart appears immediately below the results table. Switch between percentage and millisecond increases over each API's own fixed earlier P95. Above zero means slower; below zero means faster. The selected API's detailed charts remain below it.

Hover for the source/action, timing counts and before/after estimates. Hollow faded points have fewer than 20 timings in a slot or an unreliable group comparison; they suggest questions rather than prove incident boundaries. Gaps remain missing. Similar changes can suggest a common dependency, but do not establish the cause. Selecting groups/removing groups/changing units makes no database calls. AI can request the equivalent data with audit_compare_timelines and combine it with its repo inspection.



