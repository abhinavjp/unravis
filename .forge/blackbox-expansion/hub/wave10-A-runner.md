# TASK — write `internal/bbsql/runner.go` (package bbsql)

Produce the complete implementation file for the BBX-5-10 CONTRACT in the static block:
`Sampler`, `RegisterSampler`, `Samplers`, `ConnectFunc`, `SampleDoc`, `StatusDoc`,
`Runner`, `NewRunner`, `Tick`, `Run`, `Skipped`, `Running`, `CurrentState`, `StatusPath`,
`SamplePath`, plus the unexported helpers you need (`defaultConnect`, `effInterval`,
`itemLimit`, `writeStatus`, `appendSample`, `min`-style helpers if Go 1.27 `min` for
time.Duration is not usable — it is, since Go 1.21: prefer the builtins `min`/`max`).

Requirements that a reviewer will check line by line:
- Follow the numbered Tick semantics in the contract exactly, in that order.
- One connection only; reconnected only after a failure or after `Run` restarts.
- Never re-entrant: `Tick` returns immediately with `skipped++` when a tick is running.
- No goroutines, no channels, no `time.After` inside `Tick`. `Run` uses one `time.Timer`.
- Auto-off is honoured before connecting and is never cleared by `NewRunner` or `Tick`.
- Persisted bytes: NDJSON lines and `status.json`/`state.json` only; nothing that could
  carry driver text. Logging goes through `r.Log` with fixed codes and no `%s` of an error
  string — pass the `*Error.Code` only.
- File modes: directories 0700, files 0600. Status written temp-file + `os.Rename`.
- `NewRunner` must load `state.json` from `dir` by joining directly (NOT `StatePath(dir)`).
- Doc comments on every exported name, in the terse style of the package (one to three
  lines, explaining the WHY: the control it satisfies, e.g. C4/C7/C11/C13).
- Imports must be used; no `fmt` unless you actually use it.

Sizing guidance: this file is ~230-300 lines including comments. Do not add features that
the contract does not list (no metrics, no retries inside a tick, no config file reading).
