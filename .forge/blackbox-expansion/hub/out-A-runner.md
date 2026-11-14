Done. The file implements the complete BBX-5-10 contract:

- **Exported types**: `Sampler`, `ConnectFunc`, `SampleDoc`, `StatusDoc`, `Runner`
- **Exported functions**: `RegisterSampler`, `Samplers`, `NewRunner`, `Tick`, `Run`, `Skipped`, `Running`, `CurrentState`, `StatusPath`, `SamplePath`
- **Unexported helpers**: `defaultConnect`, `isDue`, `itemLimit`, `dropConnection`, `saveState`, `writeStatus`, `appendSample`
- **Tick semantics**: Follows the 8-step order exactly (re-entrancy guard, auto-off, tick budget, connect/ceiling, due samplers with per-query timeouts, state updates, atomic status write)
- **Run loop**: Uses `time.Timer` re-armed per tick with `r.state.Interval` (back-off takes effect immediately)
- **Safety**: No goroutines/channels in `Tick`; auto-off never auto-cleared; files 0600/dirs 0700; status via temp+rename; logging uses fixed `*Error.Code` only; `NewRunner` loads `state.json` directly from `dir` (not `StatePath`)
- **Context deadline**: Uses `ctx.Deadline()` for remaining time calculation (no `time.After`)
- **Query timing**: Measures per-query duration for `OnSlow`
