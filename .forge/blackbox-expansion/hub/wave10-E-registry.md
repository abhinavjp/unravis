# TASK — write `internal/bbsql/runner_registry_test.go` (package bbsql)

Test ONLY the sampler registry half of the BBX-5-10 CONTRACT: `RegisterSampler`,
`Samplers()` and the `Sampler` interface contract. Small, tight file (~90-130 lines),
`package bbsql`. The runner itself is tested elsewhere — do not duplicate those tests.

Because the registry is process-global, save and restore it:
`defer func(before := Samplers(); func(){ resetRegistry(before) })()` is NOT available
(there is no exported reset). Instead give each sub-test a unique `Kind` name
(`"reg-"+t.Name()`) so entries never collide, and assert on your own entries only.
If you conclude a package-internal `resetRegistryForTests()` helper is unavoidable, put it
in THIS file with a `// only used by tests` comment — that is acceptable, keep it 3 lines.

Cases:
1. `TestRegisterSamplerNilIgnored` — `RegisterSampler(nil)` does not panic and does not
   add an entry.
2. `TestRegisterSamplerDuplicateKindIgnored` — registering a second sampler with the same
   `Kind` keeps the FIRST one (the registry is not a last-write-wins map) and
   `Samplers()` still has one entry for that kind.
3. `TestSamplersStableOrder` — register five samplers out of alphabetical `Kind` order and
   assert `Samplers()` returns them sorted by `Kind`, and that two calls return the same
   order (deterministic file naming depends on it).
4. `TestSamplersSnapshotNotAliased` — mutate the slice returned by `Samplers()` and assert
   a fresh call is unaffected (callers must not be able to corrupt the registry).
5. `TestSamplerIDIsAllowlisted` — every sampler registered by this package's own tests
   uses an id present in `Statements`; and separately assert the seven SAMPLING ids
   (`bb:requests`, `bb:waits`, `bb:schedulers`, `bb:semaphores`, `bb:tempdb`,
   `bb:filestats`, `bb:ag`) are all `Statement`-resolvable and that the two ceiling ids are
   NOT meant for a Sampler (they belong to `CheckCeiling`).
6. `TestConcurrentRegisterSafe` — 8 goroutines registering distinct kinds while 8 read
   `Samplers()`; no race, no panic, all entries present. (If the contract registry is not
   concurrency-safe, THIS test is the reason it must be — write it against a mutex-protected
   registry and note in the trailing bullets that `runner.go` must guard the registry.)
7. `TestSamplerIntervalAllowed` — an interval that is neither `BaseInterval` nor
   `DemotedInterval` is refused at registration time (return value or ignored entry — pick
   the behaviour the static block implies and state which you assumed in the trailing
   bullets, since `RegisterSampler` has no return value in the contract, assert it is
   IGNORED).

Use plain `t.Fatalf` assertions, no testify, no external deps.
