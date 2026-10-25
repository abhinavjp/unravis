# Clumsy-user review: "This computer" (Discover) panel

Date: 2026-10-25. Build: dist\unravis.exe on port 18795, fresh config dir, own browser tab. Source was not changed.
Data used: local Windows event CSV exports and `httperr1.log` from this machine only. No client data, no tokens recorded here.

## Automated tests

| Command | Result |
|---|---|
| `node --test tests/*.test.cjs` (web) | 773 tests, 773 pass, 0 fail, 0 skipped |
| `go test ./internal/server/ -run "Discover\|Sampler"` | ok (7.4 s), 57 PASS lines, 0 FAIL |

## Defects, ranked

### 1. HIGH - time window is ignored for `httperr1.log`
- Repro: scan, tick only httperr1.log, notice ticked, choose a far-future custom range (2030) or "Last 1 hour", click Import.
- Expected: nothing imported, or only rows in range. The panel itself warns "None of the ticked sources has entries in this time range" for sources it can check.
- Actual: all 10 rows added from a file whose newest entry is about two days old. The size estimate ("About 2.3 KB will be read") is wrong in the same way.
- Cause (code read): `discover_import.go` builds `importer.Options{Host: p.hint}` without the window (about line 564). The httperr importer has no `TimeProber`, so `localread.NextWindow` returns `WindowAll` for a file under the cap. The window is then silently dropped.
- Impact: user believes a range was applied when the whole file was imported.

### 2. MEDIUM - cancelling a scan shows "This scan is over an hour old. Scan again."
- Repro: Scan, click Cancel while it runs.
- Expected: a plain "Scan cancelled" state.
- Actual: the stale-scan message. `cancelScan` issues DELETE, the next poll returns 404, and `startScan`'s catch block maps 404 to the expired text without checking `cancelWanted`.
- Cancel takes effect on the server at once (DELETE returns 204 quickly). The client waits up to the 700 ms guard before enabling Cancel.

### 3. MEDIUM - stale "Recording" state after the case is erased or closed during recording
- Repro: start Live recording, then Close case or Erase case (the server stops the sampler correctly).
- Actual: the panel keeps "Recording since ..., N samples", the toggle stays ticked and enabled. The 5 s poll stops when the case is no longer ready, so the line only corrects after the user unticks the box or switches case. The erase confirmation does not mention that a recording will stop.

### 4. MEDIUM - erase during an import: panel reports success for a case that no longer exists
- Repro: new case, scan, Import, click Erase case then confirm within about 100 ms.
- Actual: the case is erased (gone from the case list), yet the panel finishes with "Done. Added 3 of 4 sources" and keeps those results on screen when another case is created. A clean "case erased, import stopped" message is missing. Results from the old case also remain visible under the new case until "Back to the list".

### 5. MEDIUM/LOW - recording is invisible after reload
- Repro: start recording at 5 s, reload the page.
- Actual: no recording indicator until the user selects the case and scans. The label then reads "every 15 seconds" (the local default), not the real running interval.

### 6. LOW/MEDIUM - closed case behaviour
- A closed case shows "In use" and "Closed" at the same time. "Use this case" on a closed case does not reopen it; the panel says "Choose or start a case above first."
- After Escape, resize and reopening the drawer, the open case was no longer marked "In use" (observed once on a fresh case; the panel said to choose a case first). Not reproduced deliberately, cause not confirmed.

### 7. LOW - host list
- Renaming the row "local" and saving works, but every Discover import silently re-adds a `local` host (the PUT /api/hosts before each import). A refusal for an unlisted host therefore cannot be provoked from Discover, and the panel text "Check it in the host list" points to a list that may not contain the name. Even an empty host list imports fine as "local". Not harmful, but the host-list promise ("A file that names a host that is not on this list is refused") does not apply to this path.
- Result lines say "Added 1 rows".

### 8. LOW - other
- "Import 0 sources" button label when nothing is ticked.
- Estimate text for a future range is not meaningful.
- Sample count in the Recording line lags up to 5 s.
- Interval typed as 1.5 via the API gives "Request body is invalid JSON." (UI select prevents it).
- The notice tick carries over between cases, and the scan state is lost when the panel is closed.
- Cold first scan takes about 4 s with no progress text beyond the spinner.
- After Escape closes the drawer, focus lands on `body`, not on the control that opened it.

## Test log

| # | Action | Expected | Actual | Severity |
|---|---|---|---|---|
| 1a | Double and triple click Scan | One scan | One POST (202); others suppressed | OK |
| 1b | Scan then Close at once | Panel closes, no error | OK; scan state lost on reopen | Low |
| 1c | Scan then Cancel, Scan again | Cancel clean, rescan works | Cancel takes effect at once; expired-scan message shown; rescan fine | Medium (#2) |
| 2a | Import with nothing ticked | Disabled or message | Button "Import 0 sources", blocked with a message | Low |
| 2b | Import with notice unticked | Refused with a reason | Refused, clear text | OK |
| 2c | Custom range with to before from | Refused | Refused with a clear message | OK |
| 2d | Far-future range (2030) | Nothing imported | Panel says nothing in range for checkable sources; httperr1.log still added 10 rows | High (#1) |
| 2e | 10-year range | Refused (31-day maximum) | Refused with a clear message | OK |
| 3a | Import twice in a row | Second says nothing new | "Nothing new to add" (Skipped) | OK |
| 3b | Click Import repeatedly | One job | One POST (202) | OK |
| 3c | Close case mid-import | Import finishes or stops cleanly | Import finished, data committed, case closed | OK |
| 3d | Erase case mid-import | Import stops, clear message | Case erased; panel says Done, Added 3 of 4 | Medium (#4) |
| 4a | Live recording without notice | Refused | Refused, clear text | OK |
| 4b | Intervals 5, 15, 30, 60 | Each starts | Each accepted | OK |
| 4c | Toggle on and off rapidly | No error | Guarded; server 409 on a duplicate start, UI recovered | OK |
| 4d | Stop and start again | Works | Works | OK |
| 4e | Erase/close case while recording | Recording stops, UI says so | Server stops; UI stale | Medium (#3) |
| 4f | Reload while recording | UI shows recording | Hidden until case chosen; interval label wrong | Medium/Low (#5) |
| 5a | Mobile 375x812 | No overflow, reachable buttons | No horizontal overflow, drawer 351 px wide, buttons at least 30 px tall | OK |
| 5b | Tablet 768x1024 | Same | Same | OK |
| 5c | Tab order | Sane, trapped in dialog | Logical order, wraps inside the dialog | OK |
| 5d | Escape | Closes drawer | Closes; focus goes to body | Low |
| 6 | Rename "local", Scan, Import event channel | Refusal if machine name mismatches | No refusal; "local" re-added silently; import succeeded | Low (#7) |
| 7 | Console and network | No unexpected errors | Console shows only the 400s and one 409 caused by the invalid inputs I sent on purpose. DELETE calls show `ERR_ABORTED` with a 204 status (cosmetic, from the client dropping the response). No 5xx. | OK |
