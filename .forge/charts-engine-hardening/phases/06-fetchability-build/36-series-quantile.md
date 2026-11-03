# TSK-36 series-quantile

risk: medium — the series endpoint is shared by every chart; a selector term that is parsed wrong breaks all of them, and per-bucket bins can use a lot of memory.

## Outcome

A series selector may ask for a per-bucket quantile of the duration (`q=95`, FETCHABILITY-AUDIT B3). Each non-empty bucket then carries `q`, read from log-1.1 bins (the same binning as the audit histogram, so a series P95 and an audit P95 agree within one bin). Selectors without `q` answer exactly as before.

## Write scope

- `internal/casestore/series.go`
- `internal/casestore/series_test.go` (append; create if missing)

## Must not change

- The JSON of `SeriesPoint` for selectors without `q` (new field is `omitempty`).
- Any other term's parsing, the scan budget, `MaxSeriesBuckets`, `MaxSeriesCount`.
- `internal/audit/*` (call `audit.BinIndex` / `audit.BinBounds`, do not edit them).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `series.go` doc comment of `SeriesQuery` (~59–66): add a line `q=50|90|95|99         optional, needs field=duration; adds a per-bucket quantile`.
2. Consts (~25–27): add `MaxSeriesQuantile = 4 // selectors with q= per request` (memory bound: 4 × 2000 buckets × 194 bins × 8 bytes ≈ 12 MB worst case).
3. `selector` (~110–114): add `q float64` (0 = none).
4. `parseSelector` switch (~157–186): add
   ```go
   case name == "q":
       switch value {
       case "50", "90", "95", "99":
           n, _ := strconv.Atoi(value)
           sel.q = float64(n) / 100
       default:
           return bad("q is 50, 90, 95 or 99")
       }
   ```
   After the loop (~188–191): `if sel.q > 0 && !sel.duration { return bad("q needs field=duration") }`.
5. In `Series` after parsing (~276): count selectors with `q > 0`; above `MaxSeriesQuantile` return `seriesErr(SeriesTooManySeries, "At most %d quantile series per request.", MaxSeriesQuantile)`.
6. `SeriesPoint` (~80–86): add `Q *float64 \`json:"q,omitempty"\``.
7. `seriesAgg` (~222–230): add `bins [][]int64` (per bucket, lazily `make([]int64, 194)` on first value in that bucket; 194 = audit overflow bin 193 + 1).
8. `add` (~279–317): after `a.count[b]++`, if `sel.q > 0`: allocate `a.bins` (len `nb`) on first use, then `a.bins[b]` on first use, then `if k := audit.BinIndex(v); k >= 0 { a.bins[b][k]++ }`.
9. Output loop (~441–449): when `sels[i].q > 0` and `a.bins[b] != nil`, compute nearest rank `r := int(math.Ceil(q*float64(a.count[b]) - 1e-9))` (min 1), walk the bins to the bin holding rank `r`, and set `Q` to that bin's **upper** bound from `audit.BinBounds` (lower bound for the overflow bin, whose upper is nil). Clamp `Q` to `[Min, Max]` of the bucket so a one-value bucket gives that value. Put this in a small helper `binQuantile(bins []int64, n int64, q, min, max float64) float64`.
10. If `go build` reports an import cycle with `internal/audit`, do NOT move code across packages: copy the two pure functions `BinIndex`/`BinBounds` (`internal/audit/histogram.go:24–63`) into `series.go` as unexported `seriesBin`/`seriesBinBounds` with a comment naming the source, and record that in `progress.md`.
11. `series_test.go`: copy the case set-up of an existing series test. Add:
    - `TestSeriesQuantileParse`: `kind=request;field=duration;q=95` parses; `q=95` without `field=duration` and `q=97` are `SeriesBadSelector`; five `q` selectors → `SeriesTooManySeries`.
    - `TestSeriesQuantileValue`: one bucket with durations 1..100 ms → `Q` within one 10% bin of 95 and inside `[Min, Max]`; a one-event bucket → `Q == Min`.
    - `TestSeriesNoQuantileUnchanged`: a selector without `q` → every point has `Q == nil`, and `json.Marshal` of a point has no `"q"` key.
12. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/casestore/... -run "TestSeries"
.dev\toolchains\go\bin\go.exe test ./internal/casestore/... ./internal/server/...
cd web; npm test
```

PASS.

## Depends on

Phase 02 complete (ordering only).
