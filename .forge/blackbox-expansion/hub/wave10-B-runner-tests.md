# TASK — write `internal/bbsql/runner_test.go` (package bbsql)

Write ONLY the test file for the BBX-5-10 CONTRACT runner. It lives in `package bbsql`,
so it may set unexported Runner fields (`samplers`, `state`, `running`, ...) — that is the
intended test seam; do not add exported setters just for tests.

Test doubles you must define at the top of the file:
- `type fakeQuery struct{ fn func(text string) ([]Row, error) ; calls []string }` with
  `Query(ctx context.Context, text string) ([]Row, error)` that records the text, honours
  `ctx.Err()` (a blocked fake returns `<-ctx.Done()` error when told to stall) and returns
  canned rows. Keep the canned rows valid for `decodeRows` (columns from `columnKeys`).
- `type fakeSampler struct{ id, kind string; interval time.Duration; rows []Row; err error }`
  implementing the `Sampler` interface (Decode calls `decodeRows` into the matching fixed
  struct so the C6/C9 path is exercised).
- `newTestRunner(t, fq, samplers...) *Runner` helper: `Dir: t.TempDir()`,
  `Connect` returning the fake, `Now` from a settable clock, `samplers` injected.

Tests to write (names and assertions are from the packet — implement all of them):
1. `TestRunnerSkipIfRunning` — a stalling fake keeps `running` true; a second `Tick`
   returns nil, increments `Skipped()`, and `calls` did not grow.
2. `TestRunnerCeilingFailureAutoOff` — fake ceiling error (`ErrTooManyRights` from the
   Connect/CheckCeiling path) -> `CurrentState().AutoOff == true`, status on disk says
   `enabled:false, state:"denied"`, and a following `Tick` performs zero queries.
3. `TestRunnerWritesNDJSON` — two due samplers -> exactly two lines in
   `SamplePath(dir, now)`; each line decodes into `SampleDoc` with `v==1`, right `kind`,
   numeric `rows`; reopen the file and assert it is append-only across ticks.
4. `TestRunnerStatusShape` — `status.json` decodes with `DisallowUnknownFields` into the
   four C11 fields only, and `state` is one of on/backing-off/auto-off/denied.
5. `TestRunnerIntervalRespect` — a 60 s sampler is not queried on a 15 s tick; it is
   queried once the 60 s elapsed; a 15 s sampler keeps running each tick.
6. `TestRunnerBackoffOnFailure` — one query failure doubles the interval, a second
   quadruples it, cap at 15 min; a `CodePermission` failure sets auto-off; state is
   re-read from disk (`Load`) and stays auto-off (restart cannot clear it).
7. `TestRunnerSlowDemotes` — three ticks where the fake sleeps just over half the item
   limit demote the interval to `DemotedInterval` and never below the base interval.
8. `TestRunnerTickBudgetKillsQuery` — a fake that ignores its deadline: assert the ctx
   passed to `Query` expires within the 5 s tick budget (use the fake's captured deadline).
9. `TestRunnerNoErrorTextPersisted` — after a failure, read `status.json`, `state.json`
   and the NDJSON file and assert none contains the fake's sentinel text
   ("SECRET-INSTANCE-NAME", "SECRET\login", "C:\secret") .
10. `TestRunnerNeverWritesRemoteTarget` — `Conn.Server: "sql.prod.example"` -> the fake
    Connect is never called (local check happens first) and auto-off/denied is recorded.

Table-driven where it saves lines, but keep each test readable and independent. Use
`t.TempDir()` everywhere; no real `Open` call, no network. Assert on the smallest thing
that proves the control.
