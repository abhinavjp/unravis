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
| BBX-1-07 budget loop | pending | P | 3 | — | — |
| BBX-1-08 resolver proc | pending | P | 3 | — | — |
| BBX-1-09 benchmark gate | pending | P | 3 | — | — |
| BBX-1-12 wizard picker | pending | P | 3 | — | — |

## Wave 4
| BBX-1-11 resolver map | pending | P | 4 | — | — |
| BBX-2-01 HTTP.sys | pending | P | 4 | — | — |
| BBX-2-03 disk+CPU | pending | P | 4 | — | — |
| BBX-2-04 SQL host counters | pending | P | 4 | — | — |

## Wave 5
| BBX-2-02 IIS worker | pending | P | 5 | — | — |
| BBX-3-01 CLR | pending | P | 5 | — | — |
| BBX-3-02 .NET10 fallback | pending | P | 5 | — | — |
| BBX-3-03 per-proc I/O | pending | P | 5 | — | — |

## Wave 6
| BBX-4-01 event log | pending | P | 6 | — | — |
| BBX-4-02 TCP | pending | P | 6 | — | — |
| BBX-4-03 ASP.NET | pending | E | 6 | — | — |
| BBX-4-04 mem pressure | pending | E | 6 | — | — |
| BBX-4-05 busiest shape | pending | P | 6 | — | — |

## Wave 7 (SQL 5a)
| BBX-5-01 SQL service+DACL | pending | P | 7 | — | — |
| BBX-5-03 recorder codes | pending | P | 7 | — | — |
| BBX-5-05 allowlist+lint | pending | P | 7 | — | — |
| BBX-5-08 back-off | pending | P | 7 | — | — |

## Wave 8
| BBX-5-02 SQL CLI+grant | pending | P | 8 | — | — |
| BBX-5-04 /status C11 | pending | P | 8 | — | — |
| BBX-5-06 conn controls | pending | P | 8 | — | — |
| BBX-5-09 output+caps | pending | P | 8 | — | — |

## Wave 9
| BBX-5-07 ceiling+errors | pending | P | 9 | — | — |

## Wave 10 — ULTIMATE GATE (BL-123+BL-124) before samplers
| BBX-5-10 runner+C13 | pending | P | 10 | — | — |

## Wave 11 (samplers)
| BBX-5-11 requests | pending | P | 11 | — | — |
| BBX-5-12 waits | pending | P | 11 | — | — |
| BBX-5-13 schedulers | pending | P | 11 | — | — |
| BBX-5-14 filestats+AG | pending | P | 11 | — | — |

## Wave 12
| BBX-5-16 lane server | pending | P | 12 | — | — |
| BBX-6-02 dump catalogue | pending | P | 12 | — | — |

## Wave 13
| BBX-5-17 lane web | pending | P | 13 | — | — |

## Blocked (written, not dispatched)
| BBX-3-04 EventPipe | blocked | P | — | live pipe proof + cost | — |
| BBX-6-01 VM steal | blocked | P | — | live typeperf on VM | — |
| BBX-6-03 alloc sampling | blocked | P | — | EventPipe proof + cost + owner go | — |

## Log
- 2026-11-13: repo cloned, branch created, docs read, surveys done, Go 1.27.3 installed, baseline green. PLAN.md, 44 packets, GRAPH.md, STATE.md written.
- 2026-11-13: subagents created in .qoder/agents. NOTE: this session's Agent tool only exposes general-purpose/Explore/Plan; custom types not dispatchable and /agents reload not available as a tool. Dispatch uses general-purpose with the tier prompt embedded; per-dispatch model tier cannot be forced from here (agent files carry the intended tier for CLI use).
- 2026-11-13: Wave 1 (1-01..1-04) implemented in worktrees, merged, Ultimate batch review found 1 HIGH (budget slow-group livelock) + 1 LOW; fix round applied (NoteSkipped + GroupTiming optional flag), re-review confirmed resolved. Committed 06add92. CounterReading rename propagated to packets 1-06/1-09; NoteSkipped wiring added to 1-05.
- 2026-11-13: Wave 2 (1-05,1-06,1-10) implemented, merged, integration tests green. Ultimate review found 1 CRITICAL: PdhExpandWildCardPathW called with 4 args (missing dwFlags) → nondeterministic wildcard expansion. Fixed (5th arg 0 at pdh_windows.go:99,107); all PDH call arities re-verified against Win32 signatures; build/vet/tests pass.