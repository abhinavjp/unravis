# Blackbox expansion — execution state

Orchestrator memory. Updated after every step so a fresh session can resume.
Branch: `feat/blackbox-expansion` (from `feat/investigation-timeline`). Baseline at start: `go build ./...` OK; `go test ./internal/blackbox/... ./internal/server/...` OK (blackbox ~44s, server ~7min). Go 1.27.3 at `../.dev/toolchains/go/bin`.

Status legend: pending / dispatched / done / review / fixing / committed / blocked.
Reviewer: U = Ultimate, P = Performance. Tier: E = Efficient, P = Performance.

## Wave 1
| Packet | Status | Tier | Wave | Review | Commit |
|---|---|---|---|---|---|
| BBX-1-01 budget | committed | P | 1 | U: 1 HIGH livelock fixed + re-reviewed OK | 06add92 |
| BBX-1-02 engine | committed | P | 1 | U: clean (CounterReading rename accepted) | f11137c |
| BBX-1-03 config targets | committed | E | 1 | U: clean | cf63b04 |
| BBX-1-04 group types | committed | E | 1 | U: clean | f11137c |

## Wave 2
| BBX-1-05 group registry | committed | P | 2 | U: clean | (wave 2 merge) |
| BBX-1-06 PDH source | committed | P | 2 | U: 1 CRITICAL (PdhExpandWildCard 5th arg) fixed + arity re-verified | (wave 2 merge + fix) |
| BBX-1-10 wizard endpoint | committed | P | 2 | U: clean (pool run-state is live check) | (wave 2 merge) |

## Wave 3
| BBX-1-07 budget loop | committed | P | 3 | U: HIGH CPU-unit + MED false-gap fixed (r1); race + reader-late fixed (r2, atomic effective) | (wave 3 + fix r1/r2) |
| BBX-1-08 resolver proc | committed | P | 3 | U: LOW Start==0 cache fixed (r1) | (wave 3 + fix r1) |
| BBX-1-09 benchmark gate | committed | P | 3 | U: clean (0.335ms/tick, 86 allocs) | (wave 3) |
| BBX-1-12 wizard picker | committed | P | 3 | U: clean (1-line wizard mode prop + scn1 guards accepted) | (wave 3) |

## Wave 4
| BBX-1-11 resolver map | committed | P | 4 | U: HIGH all-or-nothing cache + HIGH stale PDH cache + wiring gap fixed; re-reviewed OK | (wave 4 + fix) |
| BBX-2-01 HTTP.sys | committed | P | 4 | U: clean | (wave 4) |
| BBX-2-03 disk+CPU | committed | P | 4 | U: clean; pre-existing clumsy test race stabilized (test-only) | (wave 4) |
| BBX-2-04 SQL host counters | committed | P | 4 | U: MED bRefresh 2nd call fixed; Windows enum wired; dedup pdhDetailWizard | (wave 4 + fix) |

## Wave 5
| BBX-2-02 IIS worker | committed | P | 5 | U: MED out-of-process match + paren parsing fixed | (wave 5 + fix) |
| BBX-3-01 CLR | committed | P | 5 | U: LOW no-target footprint fixed | (wave 5 + fix) |
| BBX-3-02 .NET10 fallback | committed | P | 5 | U: paren parsing + no-target footprint fixed | (wave 5 + fix) |
| BBX-3-03 per-proc I/O | committed | P | 5 | U: paren parsing + no-target footprint fixed | (wave 5 + fix) |

## Wave 6
| BBX-4-01 event log | committed | P | 6 | U: clean (IDs/numbers only) | (wave 6) |
| BBX-4-02 TCP | committed | P | 6 | U: HIGH resets cumulative→KindRate fixed | (wave 6 + fix) |
| BBX-4-03 ASP.NET | committed | E | 6 | U: clean (RequestsRejected kind = live check) | (wave 6) |
| BBX-4-04 mem pressure | committed | E | 6 | U: clean | (wave 6) |
| BBX-4-05 busiest shape | committed | P | 6 | U: clean; Step 9 finds w3wp (server suite green) | (wave 6) |

## Wave 7 (SQL 5a)
| BBX-5-01 SQL service+DACL | committed | P | 7 | U gate: SID/SDDL/account verified vs real SCM; clean | (wave 7) |
| BBX-5-03 recorder codes | committed | P | 7 | U gate: HIGH store.go:422 C10 leak fixed (+test) | (wave 7 + fix) |
| BBX-5-05 allowlist+lint | committed | P | 7 | U gate: C1/C2 lint SOUND; columns compliant | (wave 7) |
| BBX-5-08 back-off | committed | P | 7 | U gate: C7 fully met; fails closed | (wave 7) |

## Wave 8
| BBX-5-02 SQL CLI+grant | committed | P | 8 | U: MED --config binding + LOW revoke fixed; WinService wired direct | (wave 8 + fix) |
| BBX-5-04 /status C11 | committed | P | 8 | U: clean (C11 shape, no SQL over HTTP) | (wave 8) |
| BBX-5-06 conn controls | committed | P | 8 | U: HIGH trust-anchor + LOW C6 reader cap fixed; loopback dial assert | (wave 8 + fix) |
| BBX-5-09 output+caps | committed | P | 8 | U: clean (C9/C6, pins applied); Row dedup | (wave 8) |

## Wave 9 — COMPLETE (merged + Ultimate-reviewed + fixed)
| BBX-5-07 ceiling+errors | committed | P | 9 | U: HIGH C8 principal-id bypass fixed (SUSER_ID) + 8-role coverage | (wave 9 + fix) |
| BBX-6-02 dump catalogue | committed | P | 9 | U: clean (stat-only, caps, never content) | (wave 9) |
| BBX-5-16 lane server | committed | P | 9 | U: MED false-Complete/zero-fill + overflow fixed | (wave 9 + fix) |

RESUME next: collect wave 11 (5-11/12/13 in `.bbx-wt/*`) -> Ultimate GATE verdict on BL-123+BL-124 -> fix round -> group-7 PR (waves 10+11). Blocked packets still: 3-04, 6-01, 6-03. Known ceiling limitation (PR note): rights inherited via custom server roles / Windows groups not yet checked (needs recursive sys.server_role_members - owner decision).

## Wave 10 - committed 285b7ad (branch bbx-5-10, fast-forwarded into feat/blackbox-expansion)
| BBX-5-10 runner+C13 | committed | P | 10 | Ultimate gate RUNNING (whole SQL subsystem, BL-123+BL-124) | report BBX-5-10.md; C13 measured, not asserted |

## Wave 11 (samplers)
| BBX-5-14 filestats+AG | committed | P | 11 | covered by the wave-10 gate run | ea2284d; 8 tests, 6/6 mutants killed |
| BBX-5-11 requests | in flight | P | 11 | after integration | `.bbx-wt/5-11` (branch bbx-5-11) |
| BBX-5-12 waits | in flight | P | 11 | after integration | `.bbx-wt/5-12` (branch bbx-5-12) |
| BBX-5-13 schedulers | in flight | P | 11 | after integration | `.bbx-wt/5-13` (branch bbx-5-13) |

## Wave 12/13 (remaining)
| BBX-5-17 lane web | pending | P | 13 | - | last dispatchable packet (5-16 + 6-02 were pulled forward and committed in wave 9) |

## Blocked (written, not dispatched)
| BBX-3-04 EventPipe | blocked | P | — | live pipe proof + cost | — |
| BBX-6-01 VM steal | blocked | P | — | live typeperf on VM | — |
| BBX-6-03 alloc sampling | blocked | P | — | EventPipe proof + cost + owner go | — |

## Log
- 2026-11-13: repo cloned, branch created, docs read, surveys done, Go 1.27.3 installed, baseline green. PLAN.md, 44 packets, GRAPH.md, STATE.md written.
- 2026-11-13: subagents created in .qoder/agents. NOTE: this session's Agent tool only exposes general-purpose/Explore/Plan; custom types not dispatchable and /agents reload not available as a tool. Dispatch uses general-purpose with the tier prompt embedded; per-dispatch model tier cannot be forced from here (agent files carry the intended tier for CLI use).
- 2026-11-13: Wave 1 (1-01..1-04) implemented in worktrees, merged, Ultimate batch review found 1 HIGH (budget slow-group livelock) + 1 LOW; fix round applied (NoteSkipped + GroupTiming optional flag), re-review confirmed resolved. Committed 06add92. CounterReading rename propagated to packets 1-06/1-09; NoteSkipped wiring added to 1-05.
- 2026-11-13: Wave 2 (1-05,1-06,1-10) implemented, merged, integration tests green. Ultimate review found 1 CRITICAL: PdhExpandWildCardPathW called with 4 args (missing dwFlags) → nondeterministic wildcard expansion. Fixed (5th arg 0 at pdh_windows.go:99,107); all PDH call arities re-verified against Win32 signatures; build/vet/tests pass.
- 2026-11-13: Wave 3 (1-07,1-08,1-09,1-12) implemented, merged. Ultimate review found HIGH (CPU self-throttle divided by cores → budget never fires) + MED (widening fabricates running-late/false gaps) + LOW (resolver Start==0 cache) + missing 1-07 report. Fix r1 applied; re-review confirmed but flagged a NEW data race (EffectiveInterval read from HTTP goroutines) + offline-reader false-late. Fix r2: atomic.Int64 effective interval + manifest EffectiveIntervalSeconds used by DeriveState. All blackbox tests pass. -race not runnable here (no gcc); recommend owner runs -race on a CGO host. Server-suite FAIL confirmed pre-existing flake (TestAsyncImportCommitsInTheBackground, fails on pristine tree).
- 2026-11-14: Wave 4 (1-11,2-01,2-03,2-04) implemented, merged. 2-03 blocked by pre-existing clumsy_test race (real-PDH group widens window) → test-only stabilization (processed-tick barrier in hostRig). 2-04 Windows PDH object enumeration wired (sqlhost_windows.go). Integration caught pdhDetailWizard redeclaration (parallel worktrees) → deduped. Ultimate review found HIGH (all-or-nothing pool/service cache) + HIGH (W3WP PDH cache never refreshed → stale on recycle) + MED (sqlhost 2nd PdhEnumObjects bRefresh) + wiring gap (resolver never called in host.go). All fixed (independent caches, refresh-before-items, processChangeGate wiring, bRefresh=0); re-review confirmed; suite ×3 green.
- 2026-11-14: Wave 5 (2-02,3-01,3-02,3-03 per-process groups) implemented, merged; suite ×3 green in isolation. Ultimate review found MED (IIS misses out-of-process .NET10 pools — matched retargeted child PID not w3wp) + LOW-MED (instance parsing drops names with ')') + LOW (clr/proc10/procIO sample machine-wide with no targets). Fixed: IIS matches by pool name (reports w3wp PID), ')\ ' parsing in iis/proc10/procIO, no-target short-circuit. Tests added (out-of-process, paren, no-target-zero-collect). NOTE: pre-existing load-sensitive clumsy listen-timeout flake (TestClumsyWrongReadKeyIsRefusedEveryWay) appears only under parallel test load, passes in isolation.
- 2026-11-14: User requested all subagents use model Qwen4-max-preview; set model field in all 4 .qoder/agents files. LIMITATION disclosed: this session's Agent tool has no per-dispatch model param and only dispatches general-purpose/Explore/Plan; the running subagents' model is governed by the app selector (user must select it there). Wave 6 (4-01 events,4-02 tcp,4-03 aspnet,4-04 mempressure,4-05 busiest) implemented, merged; suite ×3 green. 4-05 interrupted then completed (Step 9 finds w3wp, server suite green). Ultimate review found HIGH (TCP Connections Reset is cumulative but KindPDHRate emitted gated totals) → fixed to KindRate (true delta rate, reset-guarded). ASP.NET RequestsRejected kind flagged for live check (object absent on dev box).
- 2026-11-14: Wave 7 (SQL 5a: 5-01 service+DACL, 5-03 recorder codes, 5-05 allowlist+lint, 5-08 back-off) implemented, merged. Ultimate SQL gate: SID independently recomputed (matches sc.exe showsid), SDDL = 3 ACEs no Users/Everyone, host account unchanged, C1/C2 lint judged SOUND, C7 met. Found HIGH C10 leak (store.go:422 wrote path+err.Error() into shipped event) → fixed to fixed code + fallback-branch test. Pinned 3 integration hazards into downstream packets: 5-09/5-11 empty wait type → sentinel RUNNING (no regex loosening); 5-09 FileStats needs SizeOnDiskBytes; 5-02 must wire blackbox.WinService directly (setup adapter drops Account).
- 2026-11-14: Wave 8 (5-02 CLI+grant, 5-04 /status C11, 5-06 conn, 5-09 output) implemented, merged. Ultimate review found HIGH (encrypt=true with no trust anchor → can't connect to default local SQL) → fixed with owner-supplied serverCertificate/certificate+HostNameInCertificate (NOT trustservercertificate) + post-resolution loopback dial assert; MED (install-sql --config secured one folder but registered service against another) → bound service to validated config path; LOW (C6 reader unbounded + 3 statements no TOP) → reader stops at cap+1 + TOP n added; LOW (revoke script lacked SQL<=2019 path) → both permissions printed. Row type deduped (conn.go canonical).
- 2026-11-14: STRATEGIC BREAK after wave 8 (owner request). Wave 9 (5-07 ceiling+errors, 6-02 dumps, 5-16 lane server — pulled forward to parallelize) implemented + tested, preserved on branches bbx-5-07/bbx-6-02/bbx-5-16, NOT merged. Main branch feat/blackbox-expansion at e374223 (wave 8). 28 of 41 dispatchable packets committed; 3 implemented-paused; 10 remaining (5-10 runner, 5-11/12/13/14 samplers, 5-17 lane web; blocked 3-04/6-01/6-03). No PR opened yet (GitHub account has READ-only access to abhinavjp/unravis — needs a fork or collaborator access; confirm with owner before pushing).
- 2026-11-14: Owner resumed: "PR and merge all done work; finish wave 9." Wave 9 merged + Ultimate-reviewed. Found HIGH C8 bypass (bb:ceiling_perms used DATABASE_PRINCIPAL_ID — a database id — against server-principal-keyed sys.server_permissions; guest login read public role → over-privileged login accepted) → fixed to SUSER_ID() + extended role check to 8 privileged fixed roles (mutation-tested). Lane MED (Complete:true + zero-fill on unread hours; int64 overflow on untrusted rate/interval) → fixed (ReadReport gaps + coverage, rate/interval bounds). 31 of 41 dispatchable packets now committed (waves 1-9). PR/merge still blocked by READ-only GitHub access + the standing "never merge until real-server test" rule — awaiting owner decision.
- 2026-11-14: DELIVERY. Owner chose: fork to account + open PRs only (owner merges after real-server test). Forked abhinavjp/unravis → shahnachiketdipak-coder/unravis. Reconstructed 6 cumulative per-group branches (bbx-group-1..6) by replaying packet commits in dependency order; resolved cross-group coupling (shared PdhEnumObjectsW proc moved to pdh_windows.go; stabilized hostRig test barrier included in group-1; pdhDetailWizard/Row dedup). Each branch builds + vet + tests green. Pushed to fork; opened 6 PRs into feat/investigation-timeline (NONE merged): #3 G1 Foundation, #4 G2 Core evidence, #5 G3 Per-process, #6 G4 Host evidence, #7 G5 SQL+lane, #6→#8 G6 dumps. Stacked: merge in order 1→6. Remaining work (NOT in PRs): wave 10 (5-10 runner) → Ultimate gate → wave 11 samplers (5-11/12/13/14) → 5-17 lane web; blocked 3-04/6-01/6-03.
- 2026-11-14: Wave 10 (BBX-5-10 runner) implemented (hub drafts reviewed + rewritten by the orchestrator) and committed 285b7ad. Runner owns the 5s tick budget with 3s/5s item limits, skip-if-running, the single connection + C8 ceiling at every connect, the new Gate seam (bb:hadr guards bb:ag), NDJSON append with hourly rotation + free-space floor (new CodeDiskLow), temp+rename status writes and the C7 back-off wiring; cmd/unravis-blackbox/sqlrunner.go replaces runSQLStub. Two design gaps closed on the way: nondeterministic TOP-n truncation (ORDER BY added to every capped statement - without it the Go-side delta baselines are wrong) and no retention control for the \sql folder (disk-filling hazard). C13 is now MEASURED: realistic tick 25.8ms CPU / 680B mean line / 3.2KB mean tick; at every row cap 35.2ms CPU / 3.5KB mean line / 59.4KB worst tick -> ~342MB/day, ~2.4GB over the 168h retention. Owner decisions surfaced, not hidden by thresholds: the 4 463B 40-file filestats group vs the <2KB/sample claim, worst-case storage, named instances (no probe.server; (local) only).
- 2026-11-14: BBX-5-14 (filestats deltas + AG behind the bb:hadr gate) committed ea2284d on branch bbx-5-14; both commits fast-forwarded into feat/blackbox-expansion. 8 tests; 6/6 mutants of the safety guards killed (gate truncated/NULL fail-closed, rollback baseline, first-cycle baseline, idle-drop, bounded prev, gauge-vs-delta). Verified the parent commit builds and tests green WITHOUT these files (bbx-5-10 standalone).
- 2026-11-14: Ultimate gate on BL-123+BL-124 dispatched (read-only, whole SQL subsystem: allowlist/lint/conn/ceiling/backoff/output/runner/cmd wiring + DACL + recorder error paths) before the remaining samplers ship. Wave 11 samplers 5-11/5-12/5-13 dispatched in parallel in .bbx-wt/{5-11,5-12,5-13} on branches bbx-5-11/12/13 (new files only, no shared edits, mutation evidence required). NOTE: the Agent tool's worktree isolation cannot be used from this session - it resolves HEAD in the session root (not the repo) and fails; manual `git worktree add` + absolute paths is the working pattern.
- 2026-11-14: Ultimate gate FIX ROUND done and committed. All HIGHs fixed: H1 query seam keeps the driver codes so permission/login failures reach C7 auto-off (fixedQueryError, conn.go:361); H2 owner config now reaches the runner (MinFreePercent / KeepDays*24 / MaxBytes/4) and the reconnect clock is nil-safe; H3 the sql folder DACL is verified fail-closed before the first connect (internal/bbsql/folder*.go, new code folder-lock) - BEHAVIOUR CHANGE: an unlocked folder now refuses to start, so dev/CI must use install-sql or a locked folder. M4a byte cap (whole hours oldest-first, newest never dropped), M5 slow-demotion survives a success, M6 sweep marked only after success (fixed disk-low sentence), M7 unreadable free space stops the tick, L8 dead import/stub cleanup, L9 per-statement row caps pinned (found bb:ceiling_perms TOP 2 vs Go cap 1 and bb:ceiling_role passing only by fallthrough), L10 hourly reconnect re-proves the C8 ceiling, L12 C13 measured on real RSS via psapi (60 ticks, +684 KB, cap 10 MB) with maxLine pinned in both tiers, L13 VIEW ANY DATABASE grant+revoke with the lint extended. L11 (rows:null reader) and M4b (group-inherited rights) deliberately NOT touched - real-server/next-round items. Mutation harness: 8/8 killed, byte-exact restores; M4 first SURVIVED, so TestByteCapDropsOldestHours now calls rotate directly with room for nothing. Green: go vet windows+linux, GOOS=windows GOARCH=386 build, go test ./internal/bbsql/ ./cmd/unravis-blackbox/ (30.6s / 3.2s ok). Report .forge/blackbox-expansion/reports/BBX-5-10-fix.md (measured numbers, live-server-only checks, owner decisions incl. hub-token rotation).
- 2026-11-14: Wave 11 INTEGRATED (BBX-5-11 requests, 5-12 waits, 5-13 schedulers/semaphores/tempdb, 5-17 lane label) - 39 new Go tests + 14 new web tests, all green. Gate changes: (a) bb:waits moved from 15 s to the documented 60 s (security review line 10 + BACKLOG BL-102 beat my order; C4 now gives it the 5 s item limit), bb:tempdb kept at 60 s because 5-13 correctly refused my order and flagged the conflict; (b) budget_test.go was scheduling 3 of 7 kinds on the wrong cadence, so the footprint figures were wrong - re-measured: realistic ~19 MB/day (mean tick 3 269 B, worst 7 826 B), at-cap worst tick 59 421 B with the DAILY PROJECTION now computed per kind at its own cadence = 199 877 760 B/day (~200 MB/day, 168 h ~1.4 GB, not the 2.4 GB previously quoted); sqlrunner.go/runner.go/fix-report comments corrected (default 2 GB gives the SQL folder ~537 MB ~2 days saturated); (c) new sampler_registry_test.go = anti-drift gate over the live registry (cadence per kind, allowlisted ids, caps, only ag gated, and budget fixture cadence+cap must equal the registry in both directions); (d) Semaphores Forced/Timeouts KEPT int32 with the MaxInt32 guard (widening rejected: payload shape + missing beats wrong); (e) the web lane label was false after the cadence fix, so it now reads 'every 15 to 60 s' with its 4 assertions. Carried forward as real-server-only: wait/semaphore/filestats delta parity, bb:tempdb semantics (session_space_usage is accumulated allocation by current sessions, NOT tempdb pages in use - a statement change if the owner wants the other), TOP 16 semaphores reality, loaded-box CPU/RSS, benign-wait filter parity, L11 rows:null. Evidence: go test ./... exit 0, vet windows+linux clean, web 3021/3021, svelte-check 0 errors. Report .forge/blackbox-expansion/reports/BBX-5-11-12-13-17-integration.md.
