# PR #267 review 3: the delta after review 2

**Scope.** Delta `1312634d..0d7c2d41`, one commit ("scope note cannot be forged or cut off, panicked call still gets a row, host made before begin"). Worktree: `/home/claude/wt/rev267`.

**Method.**
- I did not change the PR. My probe files were deleted after use.
- `go test -race -count=1 ./internal/codemode/...` passes on the new head.

## Verdict: FIX FIRST

The note can no longer be forged. The panic row works. One small MUST is left: the stored note can still lose its closing bracket. The fix is one line.

## M1 (review 2): what holds and what does not

### What holds

**A forged note is gone.**
- ASCII `[` and `]` in the caller's text become `(` and `)`. The text "[sources: all]", the exact note text, "[SOURCES: all]" and long bracket runs all end up with exactly one `[sources:` in the stored reason.
- Newlines and C0 controls are dropped by `cleanText`.
- Very long reasons (pre-cut to 800 characters) and an empty reason are handled.
- An empty scope gives `[sources: none]`.
- Mutations, each run against the new test `TestReasonCannotForgeOrDropTheScopeNote`:
  - without the bracket rule, the test fails;
  - without the address replacement in the broker, the test fails.

**The note is always last.**
- Probe: a fuzz of 400,000 random reasons built from address pieces, brackets, `::`, ports, zones, fullwidth brackets, U+2028, U+202E, NUL and emoji.
- The fuzz used five scopes (all, none, one id, ids holding an address or a `]`, a 90-character id) and both modes.
- A copy of the store's own step was applied: `ReplaceAddresses`, then cut to 200.
- Results: no forged note, the note was never missing, and the reason was never over 200 characters.

### What does not hold

**MUST. The note is not always whole: the store can still cut off its closing bracket.**

How it happens:
1. `composeReason` replaces addresses in the caller's text first, and then cuts it to `room` characters.
2. The cut can leave a piece that is an address only once it is cut. For example, `::ffff:[ip]` (left by the replacement) is cut to `::f`.
3. The store replaces addresses again on the joined text: `::f` (3 characters) becomes `[ip]` (4). The text is now 201 characters, so the store cuts the last character, which is the note's `]`.

Real sealed-store probe (scope `["demo-west2"]`, hidden mode):

```
Reason: strings.Repeat("x", 174) + " ::ffff:1.2.3.4"
stored tail: "xxxx [ip] [sources: demo-west2"
```

How often and how bad:
- In the fuzz, 2,375 of 400,000 reasons lost exactly the closing `]`.
- None lost more. A piece of 3 characters or fewer grows by at most 1 character, and longer pieces shrink.
- So the meaning of the note is kept. It is still the invariant the commit claims ("always last and whole"), and a reader that parses the note strictly would refuse it.

Fix (one line): after the cut, run `ReplaceAddresses` on the caller's part again, and cut again until it fits and is stable. Two rounds are enough in practice; cap it at 4. Or simply leave one spare character: `room := MaxRunReason - len(note) - 1`, then cut again after the second replace. Add the probe above to `TestReasonCannotForgeOrDropTheScopeNote`, and check the full `[sources: demo-west2]`.

## Panic row (review 2 S1): fixed

**How the code behaves.**
- The `recover` in `handleCall` calls `fail(internal_error)` first.
- If the row was not yet written, it then calls `recordPanicked`. That function writes:
  - a fixed `internal` error envelope;
  - status `error`;
  - the same `seq`, tool and arguments;
  - a fresh `done_seq` taken under `serial`.

**Why it is safe.**
- Unlock order is correct: the handler's `serial.Unlock` and `inflight--` defers run before the recover, so `recordPanicked` gets the lock.
- A `done_seq` skipped by a panic between `doneSeq++` and the write is harmless, because the store asks only that `done_seq` be unique.
- A panic inside `recordPanicked` is swallowed.
- The stopping-call wait still ends, because `fail` is called first.

**Tests.**
- `TestPanicInTheCallHandlerEndsOnlyTheRun` checks for one `error` row for `audit_get_guide`. Removing `recordPanicked` makes it fail.
- Moving `recorded = true` before `RecordCall` changes nothing in the tests. That is expected: no test panics inside `RecordCall` itself, and the flag placement is correct as written.

**The host factory.** It now runs before `BeginRun`, under a recover. A panic gives the refusal `internal_error` and leaves no row (`TestPanickingHostFactoryLeavesNothingOpen`). This closes review 2 NIT 1 for the factory. The killer and reader goroutines are still not under a recover (minor, unchanged).

## SHOULD

**S1. Unicode format and separator characters survive in the caller's text.**
- `cleanText` drops only characters below U+0020 and U+007F. U+202E (right-to-left override), U+2066 to U+2069, U+200B, U+2028 and U+2029 stay.
- A right-to-left override can reorder the reason when it is shown, so that a fake `(sources: all)` or a fullwidth `［sources: all］` looks like the last thing. The stored text is still correct.
- Fix: drop Unicode categories Cf, Zl and Zp in `composeReason`, for example with the privacy package's `normalize` rule.

## NIT

1. Fullwidth brackets (`［sources: all］`) and other look-alike brackets are kept. Only a human reader can be fooled by them, and only after S1.
2. Source ids are not run through the bracket rule. A grant id that holds `]` (operator-controlled) makes the note ambiguous. Fine until the scope gets its own field (BL-574).

## Not verified

No real worker, no Windows, no live case. golangci-lint was not run locally.
