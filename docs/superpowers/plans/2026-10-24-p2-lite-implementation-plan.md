# P2-lite implementation plan (incident patterns and ledger)

**Spec:** roadmap rev 9 §8.3 (patterns), §8.6 (ledger), §6.6 (wordings), rulings R9, R10, R14, R15. Branch `phase/p2`.

**Goal:** B1 stall, B2 episode, B6 audit-insert stall, B12 volley, plus an analyst-edited hypothesis ledger with guards. No new Mongo code: the engine reads through `SlowestReader` and `Repository.Requests`, so every P1a budget (10 s `maxTimeMS`, concurrency 1, 7-day window) still applies. MCP is not extended (R10).

## Global constraints
- Evidence label is one of `Symptom`, `Correlation`, `Strong correlation`. No "Proven cause" anywhere (R15).
- Every finding prints: label, n, low-n flag, incomplete/pending/invalid counts, and the coverage figure (or why none ran).
- A pattern is `recurring` only over 3 or more independent episodes. One episode prints "single episode, n requests", never "n stalls".
- §6.6 wording is copied exactly. Copy lint (`web/tests/copylint.test.cjs`, `web/src/lib/copy/copylint.ts`) must stay green; causal verbs never sit next to a weaker label.
- Live results never say "complete". Findings carry no URL, payload, IP or user agent.
- Repo conventions: stage only named files, never `gofmt -w` a directory, commit trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Go is at `.dev/toolchains/go/bin` (not on PATH).

## Rule defaults (all in `patterns_types.go`)
| Rule | Definition | Label |
|---|---|---|
| B1 | observed `DurationMs` >= 45000 | Symptom |
| B2 | stalls merge into one episode when the next start is within 600 s of the episode's last end | Symptom |
| B6 | `AuditCreateTime` >= 5000 ms and >= 0.3 of observed | Symptom; wording "Audit create took <n> s as measured from the client side." |
| B12 | 2 or more stalls from one user starting within 1000 ms; records end stagger | Correlation |

## Contract (committed before the tasks; do not change signatures)
- Go: `internal/audit/patterns_types.go` (types), `patterns_run.go` (`ValidatePattern`, `RunPatterns(ctx, repo, in)`), `internal/ledger/types.go` (types and `Store`), `ledger.go` (`Open`, `Guards`).
- HTTP: `POST /api/patterns` (PatternInput -> PatternReport); `GET /api/ledger`; `POST /api/ledger {text}`; `PUT /api/ledger/{id}` (Input); `POST /api/ledger/{id}/reuse`; `DELETE /api/ledger/{id}`. A guard block is HTTP 422 `{error, code:"guard_blocked", reasons:[...]}`.
- Web: `web/src/lib/types.ts` and `api.ts` carry the types and functions; `App.svelte` mounts `PatternsPanel` (props `query, zone, sourceLabels, onClose`) and `LedgerPanel` (prop `onClose`).

## Tasks and file ownership (disjoint)
| Task | Model | Owns |
|---|---|---|
| A engine | Opus | `internal/audit/patterns_run.go`, `patterns_run_test.go`, `patterns_demo_test.go` |
| B ledger | Opus | `internal/ledger/ledger.go`, `guards.go`, `ledger_test.go` |
| C server | Sonnet | `internal/server/patterns.go`, `ledger.go`, their tests, `server.go` route lines and `WithLedger` option, `cmd/audit-explorer/main.go` wiring |
| D patterns UI | Sonnet | `web/src/components/PatternsPanel.svelte`, `web/src/lib/patterns.ts`, `web/tests/patterns.test.cjs` |
| E ledger UI | Sonnet | `web/src/components/LedgerPanel.svelte`, `web/tests/ledger.test.cjs` |
| F copy and docs | Haiku | glossary terms and copy-deck entries, `README.md` P2-lite section, MCP.md untouched |

Demo data: A checks whether the demo repository yields stalls; if not, A adds a demo-only deterministic scenario (three episodes, one 3-request volley) without changing any existing test's counts.

## Verification
Per task: its own tests plus `go vet`/`npm run check`. After all tasks merge into `phase/p2`: one batched Opus review of the whole branch against §8.3, §8.6 and §6.6, then fixes, `web/dist` rebuild, `build.ps1`, merge to `master`, restart the demo, push.

## Review focus
1. Window with zero stalls: prints "none", not an error.
2. A source that is not a `SlowestReader`: clear unavailable message.
3. Stall rows missing `InitiatedAt` or `CompletedAt`: counted as incomplete, never dropped silently.
4. More than 500 stalls: `truncated` true, and the episode count is labelled partial.
5. Ledger: overlap-only support, empty alternatives and an unstated presence rate must block "strong".
