# TSK-33 burst-count

risk: low — a pure helper with a table test; nothing calls it yet.

## Outcome

One shared counter answers "how many rows in the incident, how many in the reference, and the most in any 5 minutes of the incident", used by S1, S10, S11, S12, D16, D17 (Phase 07) and D2 (TSK-34).

## Write scope

- `internal/investigate/burst.go` (new)
- `internal/investigate/burst_test.go` (new)

## Must not change

- Any existing file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Create `burst.go`:
   ```go
   package investigate

   import "time"

   // BurstCount is a row count for the incident and reference windows, the
   // most rows in any 5 minutes of the incident (Peak5), and the window lengths
   // in minutes, so rates can be compared.
   type BurstCount struct {
       Inc, Ref, Peak5 int
       IncMin, RefMin  float64
   }

   // IncRate and RefRate are rows per minute; zero when the window is empty.
   func (b BurstCount) IncRate() float64 { if b.IncMin <= 0 { return 0 }; return float64(b.Inc) / b.IncMin }
   func (b BurstCount) RefRate() float64 { if b.RefMin <= 0 { return 0 }; return float64(b.Ref) / b.RefMin }

   // CountBurst counts ts in each window. Peak5 slides a 5-minute span over the
   // incident rows (two pointers over the sorted incident times).
   func CountBurst(ts []time.Time, inc, ref Window) BurstCount
   ```
   Implement `CountBurst`: copy incident times into a slice, `sort.Slice` ascending, two pointers `i, j`; for each `j`, advance `i` while `t[j].Sub(t[i]) >= 5*time.Minute`; `Peak5 = max(Peak5, j-i+1)`. `IncMin = inc.End.Sub(inc.Start).Minutes()` (0 if invalid), same for `RefMin`. Use `Window.Contains` (`types.go:40`).
2. In the same file add the readers:
   ```go
   // httpErrTimes returns the times of HTTPERR rows whose reason is one of reasons
   // (case-insensitive).
   func httpErrTimes(in Input, reasons ...string) []time.Time
   // sqlTimes returns the times of SQL event rows of the given kind.
   func sqlTimes(in Input, kind string) []time.Time
   ```
   `httpErrTimes` uses `strings.EqualFold`.
3. `burst_test.go`: table test `TestCountBurst`:
   - empty → all zero;
   - 3 rows at incident minutes 0, 2, 4 and 1 at minute 30 → `Inc 4, Peak5 3`;
   - rows at minutes 0 and 5 exactly → `Peak5 1` (half-open span);
   - a reference row is counted in `Ref`, never in `Peak5`;
   - `IncRate`/`RefRate` with a 60-minute incident and a 120-minute reference.
   And `TestHTTPErrTimesFold`: reason `timer_headerwait` matches `Timer_HeaderWait`.
4. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestCountBurst|TestHTTPErrTimesFold"
```

PASS.

## Depends on

32 (`SQLEvent`, `HTTPErrRow`).
