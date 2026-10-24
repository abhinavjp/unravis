# T0.10 MCP output audit

**Date:** 2026-10-24
**Status:** audit only. It approves nothing, and R11 stays open.
**Roadmap refs:** T0.10 (§5.2), §2.1 MCP row, §6.7, §7.4, §8.9, R11 (§10.3)
**Method:** static reading of `internal/mcp`, `internal/audit`, `internal/server` and `internal/store` on branch `p14a/t010` (base `2f93be5`). No tool was run against live or demo data. A claim is marked *(unverified)* where the code alone does not settle it. Paths are relative to the repository root.

## 1. Path and common behaviour

- `--mcp` starts a stdio bridge to the dashboard that is already running at `127.0.0.1:<port>` (`cmd/audit-explorer/main.go:50-64`, `internal/mcp/bridge.go:37-44`). The bridge can call only 7 dashboard routes: `info`, `sources`, `analyze`, `requests`, `activity`, `slowest` and `breakdown` (`bridge.go:84-88`). It cannot call `/api/payload`, `/api/record` or `/api/export`.
- Each tool result is JSON text inside one MCP `content` item (`protocol.go:218-231`). **Everything below reaches the AI client and then its model provider.**
- Limits: each MCP message is at most 1 MiB, and a result that is too large is replaced by a fixed error (`protocol.go:15,63-65`). Upstream reads are capped at 16 MiB (`bridge.go:20,118`). At most 4 calls run at once (`protocol.go:205-209`). Each call has a 5-minute deadline (`protocol.go:210`). Only one comparison is cached, for 10 minutes (`bridge.go:21`, `tools.go:79-86`).
- **Error text.** Tool errors are returned word for word (`protocol.go:220-221`). Every error the bridge creates is fixed text. Validation messages name the field, never the value (`internal/audit/query.go:10-63`, `filters.go:122-176`, `ranges.go:150-180`). An argument decode failure returns a fixed message (`bridge.go:155-172`). A non-200 upstream answer is mapped to fixed text, and upstream bodies are never shown (`bridge.go:131-147`). Per-source `error` strings are curated messages chosen by code (`internal/server/server.go:561-572`, `internal/audit/source_errors.go`, `internal/store/query_errors.go:11-48`). The breakdown route does not call `normalizeSourceResult`, but its messages come from the same curated function (`store/breakdown.go:26-29,70-118`). **No path was found that puts data values into error text.**
- **Echoed query.** `audit_compare`, `audit_get_timeline` and `audit_compare_timelines` return the cached query unchanged (`tools.go:167,215`, `timelines.go:87`), including any `userIds`, `userIps`, `userAgentContains`, `urlContains` and `auditId` the caller sent. `audit_get_requests`, `audit_get_slowest` and `audit_get_breakdown` use `echoQuery`, which removes `userIps` only (`breakdown.go:103-106`). The model wrote these values itself, so echoing them reveals nothing new. They still count as identifiers if a proxy pseudonymises outputs.
- **Cursors** are base64 JSON holding {timestamp, sourceId, ObjectID, metric, SHA-256 query fingerprint, now} (`store/requests.go:20-30,296-302,467-476`). They contain no personal fields.

## 2. Per-tool inventory

Shared input object `query` (`schema.go:53-73`) has these required fields: `sourceIds` (1–8), `start`, `end`, `baselineStart`, `baselineEnd`, `kind`, `outcome`, `metric` and `bucketSeconds` (10–86400, at most 120 slots, each period at most 7 days; `query.go:11-41`). The optional filters are `controller`, `action`, `operation`, `userIds` (≤50), `uiVersions`, `userIps` and `statusCodes` (≤20 each), `userAgentContains`, `urlContains` (≤200 B), `auditId` (24 hex), `completion`, and ranges on `observedMs`, `apiTimeMs`, `auditCreateTimeMs`, `requestKb`, `responseKb` and `incompleteAgeMs`.

**T1 `audit_get_guide`**: no inputs (`tools.go:99-103`). Returns the fixed guide text (`schema.go:5-18`). No data.

**T2 `audit_list_sources`**: no inputs (`tools.go:104-115`). Returns `Source{selectionKey, connectionId, connectionLabel, id, label, database, collection}` (`models.go:83-91`). `selectionKey` is a SHA-256 of scheme, hosts, database and collection (`store/profiles.go:356-374`). **`connectionLabel` defaults to the MongoDB URI host list, including port, when the analyst leaves the label blank** (`store/profiles.go:112-116`). This is infrastructure data, not personal data. Credentials and the URI are not returned.

**T3 `audit_compare`**: inputs are `query` (required), `limit` (default 20, 1–100) and `sort` (default `p95_delta_ms`) (`tools.go:117-131`, `groupPage` `tools.go:24-40`). Returns:
- a page of `groups[]` of `Comparison` (`models.go:240-264`): opaque `key`, `sourceId`, `controller`, `action`, `kind`, `outcome`, `state`, event counts, `current` and `reference` `Stats` (count, mean/min/max, p50/p90/p95/p99, histogram, phase means, 4xx/5xx counts and rates, incomplete/pending/invalid; `models.go:191-223`), ratios and deltas
- `matchedGroups`, `totalGroups`, `nextOffset`, `resultId`
- `query` (unfiltered echo), `sources[]` (`SourceResult{sourceId, ok, error, errorCode, version, indexWarning}`, `models.go:145-152`; `version` is the MongoDB build version), `quality` counts, `recordedEvents` with rates, `approximate` and `expiresInSeconds` (`tools.go:166-175`)

It does not return `Analysis.Warnings`, `Timings`, `Rollups`, `Overview`, `LoadWindows` or `RequestVolume`. Its description mentions "the rollup" (`schema.go:85`), but rollups are not in the output. This is a doc mismatch, not a data risk. Controller and action come from the stored `ControllerName`/`ActionName` (`store/pipeline.go:198`) and can be up to 1024 runes each (`store/legacy_pipeline.go:38-39`).

**T4 `audit_get_groups`**: inputs are `resultId` (required), `offset` (≥0), `limit` (default 20, 1–100), `search` (≤200 B, matched against controller, action, sourceId, kind and outcome), `state` and `sort` (`tools.go:14-78`). Returns the same `Comparison` page plus counts. It reads the cache only.

**T5 `audit_get_timeline`**: inputs are `resultId` and `apiKey` (`tools.go:193-196`). Returns `query` (unfiltered), `group` (`Comparison`) and `points[]` of `Point{at, count, p50, p95, p95Ratio, count4xx, count5xx, incomplete, p90, p99}` (`models.go:266-285`, `tools.go:208-215`). It reads the cache only.

**T6 `audit_compare_timelines`**: inputs are `resultId` and `apiKeys` (1–8, unique) (`tools.go:217-228`, `timelines.go:10-20`). Returns `query` (unfiltered), `groups[]` (`Comparison`) and up to 120 `slots[]`. Each slot holds `{apiKey, count, sampleStatus, currentP95Ms, baselineP95Ms, increaseMs, increasePercent}` (`timelines.go:48-87`). It reads the cache only.

**T7 `audit_get_requests`**: inputs are `resultId`, `apiKey`, `cursor` (≤4096 B) and `limit` (default 20, 1–100) (`tools.go:230-244`). It runs a live query. Upstream rows include `url` (`RequestMainUrl`), `userId`, `userIp`, `userAgent`, `uiVersion` and more (`store/requests.go:399`, `models.go:355-380`). The bridge **allow-lists** these fields per row: `id` (Mongo `_id`), `state`, `initiatedAt`, `completedAt`, `durationMs`, `apiTimeMs`, `auditCreateTimeMs`, `payloadRecordTimeMs`, `requestKB`, `responseKB`, `outcome` and `status` (`breakdown.go:18-20`). The response also has group labels, `query` (without `userIps`), `nextCursor`, `sources[]` and `truncated` (`tools.go:257-261`). **The response contains no URL, IP, user agent, user id, header or payload.** A test covers this for slowest (`tools_test.go:255-`).

**T8 `audit_get_slowest`**: inputs are `resultId`, `cursor`, `limit` (default 20, 1–50) and `minMs` (≥0, finite) (`breakdown.go:22-44`, `load.go:15`). It runs a live query. Rows use the same allow-list plus `sourceId`, `controller`, `action` and `kind` (`breakdown.go:49-54`). The response also has `metric`, `query` (without `userIps`), `nextCursor`, `sources[]` and `truncated`.

**T9 `audit_get_breakdown`**: inputs are `resultId`, `dim` (required: `statusCode`, `userId`, `uiVersion`, `userAgent`, `pollingType` or `userIp`), `top` (0 means 20, max 50) and `includeIp` (bool) (`breakdown.go:58-79`, `audit/breakdown.go:24-36`). It runs a live aggregate query. Returns `rows[]` and `other` as `BreakdownRow{value, missing, count, complete, incomplete, pending, count4xx, count5xx, stats, filter, values}` (`audit/breakdown.go:76-88`), plus `total`, `distinct`, `pooledKinds`, `sources[]` and `query` (without `userIps`) (`breakdown.go:98`).
- `value` is the raw stored field: `UserId` as decimal digits, `UserAgent` up to 512 runes, `PayrollUIVersion`, `StatusCode`, `RequestPollingType`, or `UserIp` up to 64 runes (`audit/breakdown.go:138-157`, `store/breakdown.go:171-183`, `store/legacy_pipeline.go:38-43`).
- `filter` repeats the value as a drill-down fragment: `userIds`, `userAgentContains`, `uiVersions`, `statusCodes` or `userIps` (`audit/breakdown.go:166-204`).
- **`userId` and `userAgent` are available by default.** No flag gates them.
- `userIp` is refused unless `includeIp` is true. The refusal happens before any query, and the error text contains no address (`breakdown.go:70-73`, `tools_test.go:187-205`). For every other dimension, IPs are removed from `filter` (`breakdown.go:88-97`).

**T10 `audit_get_activity`**: inputs are `sourceIds` (1–8), `start` and `end` (window ≤24 h), `cursor` and `limit` (default 20, 1–100) (`activity.go:9-19`, `audit/activity.go:11-35`). It runs a live query over all kinds and outcomes. Rows are allow-listed to `sourceId`, `controller`, `action`, `kind`, `outcome`, `initiatedAt`, `completedAt`, `durationMs`, `apiTimeMs`, `auditCreateTimeMs` and `payloadRecordTimeMs` (`activity.go:24-27`). Rows have no record id, no user fields and no URL. The response echoes `start`, `end` and `sourceIds`, plus `sources[]`, `nextCursor` and `truncated`.

## 3. Findings that matter for R11

1. **`includeIp` is controlled by the model, not by the operator.** It is an ordinary tool argument. No CLI flag, config setting or dashboard setting can disable it (`main.go:47-50`, `breakdown.go:63-73`). Once a model sets it to true, up to 50 IP addresses plus counts reach the provider. "Opt-in" protects against accidents, not against the provider seeing IPs.
2. **Raw user ids and user-agent strings are returned by default** through `audit_get_breakdown` (`dim=userId` or `userAgent`), both as `value` and as `filter`. Rev 9 §2.1 is right that a user dimension exists. It should also name `userAgent`.
3. **A single request can be linked to its user.** `audit_compare` with `query.auditId=<_id>` followed by `audit_get_breakdown dim=userId` (or `userAgent`, or `userIp` with `includeIp`) returns that one record's user id, agent or IP (`schema.go:63`, `breakdown.go:85`). A `userIds` filter likewise gives a per-user latency profile. Inferred from the code; not run.
4. **Raw URLs are never returned, but `urlContains` works as a substring oracle.** Group and breakdown counts change with the substring the model supplies (`schema.go:62`). Repeated probing could reveal URL or query-string content. `userAgentContains` works the same way. Inferred from the design; not tested.
5. **Payload bodies cannot be returned.** There is no route to them (`bridge.go:84-88`). `requestKB` and `responseKB` (sizes only) are returned.
6. `_id` values are returned on purpose, to make findings citable. They are pseudonymous, but anyone with database or dashboard access can link them to the full record, including payload, URL, user and IP.
7. Infrastructure detail reaches the provider through `audit_list_sources` (the `connectionLabel` host list when no label is set; database and collection names) and through `sources[].version` (the MongoDB version).
8. Controller and action names, status strings, UI versions and user agents are data written by the audited app. Whether they ever contain personal data cannot be determined from Unravis code *(unverified, OPEN-6)*.

## 4. Risk table

| Tool | Sensitive field class | Default | Recommended working default (until R11 closes) |
|---|---|---|---|
| T9 breakdown | Client IP (`value`, `filter.userIps`) | Off; the model can turn it on | Treat as on. No live use until an operator-side IP switch exists (default off) or the client can deny `includeIp`. Prefer demo data (§7.4) |
| T9 breakdown | User id (`value`, `filter.userIds`) | **On** | Pseudonymise with a keyed per-case HMAC before any client sends it, or report aggregate counts only |
| T9 breakdown | User agent (≤512 runes) | **On** | Reduce to browser/OS family, or omit the dimension on live data |
| T9 breakdown | UI version, status, polling type | On | Acceptable if the client approves operational metadata |
| T3/T5/T6 | Echoed filters (`userIds`, `userIps`, `urlContains`, `userAgentContains`, `auditId`) | On (only what the model sent) | Apply `echoQuery`-style stripping in all three, and include them in any pseudonymisation |
| T3 + T9 | Record-to-user linkage via `auditId` | On | Forbid the `auditId` + user/agent/IP breakdown combination on live data, or pseudonymise as above |
| T3 (T4/T9 reuse its cache) | URL and agent substring oracle (`urlContains`, `userAgentContains`) | On | Do not allow these filters on live data unless approved |
| T7/T8 | `_id`, timestamps, timings, KB sizes, status | On | Acceptable if approved. `_id` stays a local citation key |
| T10 | Cross-source activity timings, controller/action | On | Acceptable if approved |
| T2 + `sources[]` | Host list, database/collection names, MongoDB version | On (host only when the label is blank) | Always set a neutral connection label for MCP sessions |
| All | Payload bodies, raw URLs, headers, credentials | Not reachable | No change. Keep the route allow-list (`bridge.go:84-88`) |

The §8.9 R8-B3 test (a fixture with a known user, IP and URL; call all 10 tools) should cover findings 1–4. In particular it should assert the absence of URL text under `urlContains` probing, and the absence of the user id in T3, T5 and T6 query echoes once stripping is approved.

## 5. Could not be determined from code (OPEN; for the client's security owner)

- **OPEN-1** Which AI client(s) will be used with live data. `MCP.md:3,8` names Codex and Claude Code as configured clients; whether either is approved is not stated.
- **OPEN-2** The model provider behind that client, including region, account type and any enterprise or zero-retention agreement.
- **OPEN-3** The provider's data-retention, logging and training-use terms for tool results.
- **OPEN-4** Whether the client stores transcripts, including tool results, locally or in the cloud, and for how long.
- **OPEN-5** Which fields the client approves reaching the provider (the "approved fields" column T0.10 asks for). None is approved by this document.
- **OPEN-6** Whether live `ControllerName`, `ActionName`, `UserAgent`, `PayrollUIVersion` or status values contain personal data, and whether `UserId` maps directly to a named employee. Answering this needs a data sample or the audited app's owner.
- **OPEN-7** Not run: the oracle and linkage behaviours (findings 3 and 4), and the actual output size and content on live data.
