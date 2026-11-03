# TSK-47 s9-filter-reject

risk: medium — reads the IIS sub-status for the first time; a wrong code list would flag ordinary 404s.

## Outcome

`filter_reject` reads Yes when IIS request-filtering rejections (the listed status.sub-status pairs, or status 431) number at least `FilterMin` in the incident and rose at least `FilterRise` times over the comparison rate (or the comparison held none). No when the incident holds at least `GroupSizeP95` IIS rows and that is not the case. Missing `iis` without IIS rows; missing `substatus` when the IIS rows carry no sub-status. S9 itself is capped at orange by TSK-57.

## Write scope

- `internal/investigate/sus_s9.go` (replace the stub body)
- `internal/investigate/sus_s9_test.go` (new)

## Must not change

- Every other file.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `sus_s9.go`:
   ```go
   // filterCodes are the IIS request-filtering rejections: status.sub-status.
   // Status 431 (header fields too large) counts whatever its sub-status.
   var filterCodes = map[string]bool{
       "404.5": true, "404.6": true, "404.7": true, "404.8": true,
       "404.11": true, "404.12": true, "404.14": true, "404.15": true,
       "404.18": true, "404.19": true, "413.1": true,
   }

   // isFilterReject says whether one IIS row is a request-filtering rejection.
   func isFilterReject(r IISRow) bool {
       return r.Status == "431" || filterCodes[r.Status+"."+r.SubStatus]
   }

   // sigFilterReject counts request-filtering rejections in the IIS rows (S9).
   func sigFilterReject(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal
   ```
   Body:
   - `if !in.IISPresent { return missing("iis", "There are no IIS log rows for these windows.") }`
   - `if !in.IISSubStatus { return missing("substatus", "The IIS rows carry no sub-status, so request-filtering rejections cannot be told from other 404s.") }`
   - `var ts []time.Time` of `r.End` for rows where `isFilterReject(r)`; `b := CountBurst(ts, in.Incident, in.Reference)`.
   - `rose := b.RefRate() == 0 || b.IncRate() >= th.FilterRise*b.RefRate()`.
   - `b.Inc >= th.FilterMin && rose` → `sig(Yes, fmt.Sprintf("%d request-filtering rejections in the incident, at most %d in 5 minutes.", b.Inc, b.Peak5))`.
   - `iisRowsIn(in, in.Incident) >= th.GroupSizeP95` → `sig(No, fmt.Sprintf("%d request-filtering rejections in the incident, no rise over the comparison period.", b.Inc))`.
   - else `sig(Unknown, "Too few IIS rows in the incident to read request-filtering rejections.")`.
2. `sus_s9_test.go`:
   - `TestIsFilterReject`: `404/5` true, `404/0` false, `404/13` false, `413/1` true, `431/0` true, `500/19` false.
   - `TestSigFilterReject`: no IIS → `Missing == "iis"`; IIS rows with empty `SubStatus` and `IISSubStatus false` → `Missing == "substatus"`; 30 incident rows of which 4 are `404/7`, reference with none → `Yes`; 30 incident rows, 4 `404/7`, reference of the same length with 4 `404/7` → `No`; 5 incident rows, 1 rejection → `Unknown`.
3. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestIsFilterReject|TestSigFilterReject"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/... -skip "^TestGolden$"
```

PASS.

## Depends on

44 (stub, `iisRowsIn`, `FilterMin`/`FilterRise` via 43), 32 (`IISRow.Status/SubStatus`, `IISSubStatus`), 35 (the server fills them; meaning only).
