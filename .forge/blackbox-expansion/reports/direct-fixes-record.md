# Direct orchestrator fixes — the eight findings with no subagent report

Date: 2026-11-15. A cross-check of all 84 review finding IDs against the fix reports
found eight that were fixed **directly by the orchestrator** rather than by a delegated
worker, so they had no fix-report entry. They are all fixed and committed; this file
closes the documentation gap so the audit trail is complete.

| Finding | Wave | Commit | What was wrong | What changed |
|---|---|---|---|---|
| W4-05 | 4 | `8d2a938` | `parseHTTPPath` split the instance at the first `)`, so a pool named `MyApp (Staging)` was silently dropped even with a valid reading. | Split on the `)\` terminator, matching the other groups' corrected parsers. Test covers nested/repeated parentheses and the missing-terminator refusal. |
| W7-02 | 7 | `7aec853` | Two invalid DMV source columns: `sys.dm_exec_requests` has `wait_time`, not `wait_time_ms`; `sys.dm_exec_query_resource_semaphores` has `timeout_error_count`, not `timeout_count`. Every requests batch failed, and ten failures latched the whole SQL stream auto-off. | `r.wait_time` in both requests expressions; `timeout_error_count AS timeout_count` so the fixed output mapping is unchanged. `bb:waits` correctly keeps `wait_time_ms` (different DMV). |
| W7-04 | 7 | `5ab1b3b` | `Load` only rejected JSON syntax errors; `null`, `{}`, missing/unknown keys and a latch disagreeing with its label all normalized into an enabled state. | Pointer-mirror parsing with `DisallowUnknownFields` and a strict single-value EOF check; 16-case refusal table; refusal still returns fail-closed auto-off. |
| W8-05 | 8 | `ca1a7ac` | Sample statements selected `TOP n` with n equal to the rows kept, so the n+1 truncation witness could never arrive and an over-cap result looked complete. The lint actively required `TOP == cap`, pinning the defect. | Samples fetch `TOP cap+1` (requests 257/256, waits 21/20, semaphores 17/16, filestats 257/256, ag 65/64); control statements keep `TOP == cap`. Lint now requires the sentinel row. |
| W10-01 | 10 | `16b8baf` | x/sys `DACL()` returns **defaulted**, not present. Reading it as presence made every explicitly locked folder look unlocked, so the service refused to start on a correct install. | Presence from successful retrieval; a defaulted DACL refused explicitly; NULL DACL still refused. Added the positive platform-adapter test the review asked for. |
| W10-12 | 10 | `16b8baf` | ACE table wrong: 8 is `SYSTEM_ALARM_OBJECT_ACE`, 11 is `ACCESS_ALLOWED_CALLBACK_OBJECT_ACE` (a **grant**). Listing 11 as a deny silently ignored an unreadable grant. | Callback denies corrected to 10 and 12; every unreadable allow form and every audit/alarm form falls through to Unknown and refuses. |
| W10-04 | 10 | `5ab1b3b` | `Save` overwrote `state.json` in place with no flush (torn file on power loss), and `Runner.fail` discarded the persistence error while later auto-off ticks only wrote status — so a denial that never reached disk was forgotten across restart. | Atomic temp+sync+rename; a `stateDirty` flag retried even while auto-off; a failed save logs the fixed `state-save` token and never claims a durable latch. Real filesystem fault test. |
| W4-03 | 4 | `1ec80c2` | A successful pre-runtime module snapshot cached runtime 0 for the process's whole life. **Same defect as W3-02**, fixed once by the resolver worker: `runtimeState{runtime,known,next}` with a 15 s pending retry independent of process-set change. | No separate change needed; recorded here so W4-03 is not mistaken for an open item. |

## Verification

Each was verified when committed (see the commit messages for the exact commands). The
current tree has been re-verified end to end after all waves landed:

```
go build ./...                          clean
go vet ./...                            clean
GOOS=linux go vet                       clean
go test ./internal/bbsql/               ok
go test ./internal/blackbox/            ok
go test ./cmd/unravis-blackbox/         ok
web: svelte-check 0 errors; npm test 3070/3070
```

## What is genuinely still open (not fixable locally)

These are **not** missing fixes; they are decisions or evidence only the owner can supply:

1. **C13 “under 2 KB per sample” is unmet** at the C6 row caps — ~4.4 KB for a realistic
   filestats line, ~53 KB at-cap maximum width; ~319 MB/day worst case. The bound was
   **not** relaxed. It needs an owner ruling or a row-cap/payload change.
2. **`-race` is unrun** — no C compiler on this host. The concurrency was designed to avoid
   shared mutable state, but the owner should run `-race` on a CGO host.
3. **W9-01 effective-rights matrix** — the code landed; proving coverage of custom/nested
   roles, Windows groups, public and SQL 2022 `##MS_*##` roles needs a real SQL Server.
4. **All live-server checks** — real elevated install, PDH paths/types/units on English and
   non-English Windows, multi-core CPU parity, `Creation Time` FILETIME semantics and
   same-second PID reuse, native `EvtRender`/record-ID/reset behaviour, `GetTcpTable2` churn
   and IPv4/IPv6 coverage, driver handshake/cancellation latency, loaded-host
   CPU/working-set/handles, and the lane UI against real recordings.
5. **NTFS hardlinks** — a hardlink is a regular file and cannot be refused without opening
   it; reported as a known limit, not weakened.

`docs/BACKLOG.md` remains unticked: none of this is verified on the real data path.
