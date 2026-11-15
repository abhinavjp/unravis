# P-01 Live field-name probe

model: sonnet, effort: medium. Reason: needs judgement to match field names to roles; touches a session token.
risk: medium. Review batch: RB-3 (the report only). Wave: W0. Requirement: R-08.

## Outcome
`screen/probe/fields.md` lists, for one live audit source, every field path whose name suggests SQL, Redis, HTTP, filter or hit-count timing, with its BSON type, and names the path chosen for each perf role, or says "none found".

## Write scope
- `.forge/investigation-timeline/drill/screen/probe/fields.md` (new)

## Must not change
- Any source file. Any app setting. The session token must never be printed, logged or written to a file.

## Changes
1. The installed Unravis must be running with a live (non-demo) source connected. If it is not, report BLOCKED with that reason; do not start a demo.
2. Find the port and token: `session.json` in `$env:AUDIT_CONFIG_DIR`, else `%AppData%\APIAuditExplorer` (struct `sessionRecord{port,token,pid}`, `internal/mcp/sessionfile.go`). Read them into shell variables in one command; never echo the token.
3. List sources with the MCP tool `mcp__api_audit_explorer__audit_list_sources` (or `GET /api/sources` with header `X-Audit-Token`). Pick up to 3 live source ids.
4. For each: `GET http://127.0.0.1:<port>/api/sources/<id>/fields` with header `X-Audit-Token: <token>`. The reply is `{sampled, paths:[{path, types}], truncated}`: names and types only, no values (`internal/server/fields.go`, 5 newest documents, max 200 paths, depth 4).
5. From the paths, keep those matching (case-insensitive) `sql|db|database|redis|cache|http|outbound|external|filter|hit|step|stage|pressure|cpu|memory|thread`.
6. Write `fields.md` with: date, number of sources sampled (no source names, no ids), a table `path | types`, then a table `role | chosen path | why` for the roles `_perfHitCount, _perfSqlCount, _perfSqlMs, _perfRedisCount, _perfRedisMs, _perfHttpCount, _perfHttpMs, _perfSlowSql, _perfSlowHttp, _perfSteps, _perfPressure, _perfTextStored` (keys from `internal/store/record_perf.go` `recordPerfFields`). A role with no clear match gets `none found`. Guessing is not allowed: a role is chosen only when the name and the type (number for Ms and Count) both fit.
7. If `truncated` is true, say so in the file.

## Proof
- [ ] `fields.md` exists and has both tables.
- [ ] `Select-String -Path <fields.md> -Pattern '[A-Za-z0-9_\-]{32,}'` returns nothing (no token-like string).
- [ ] The file holds no source id, host name, user name or value.
- [ ] Every role row is either a path that appears in the first table or `none found`.
- [ ] State plainly: "live-verified on N sources" or "BLOCKED: no live source".

## Depends on
Nothing. Needs the installed app running with a live source.
