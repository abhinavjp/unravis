# Hub review, gate 4 (2026-11-05): uncommitted Unravis work vs HEAD d34cc29

Courier: Claude Code subagent. Remote hub https://hub.brainpayroll.ae. Nothing was applied, staged or committed.

Bundle: slim two-commit review repo (base = HEAD versions of the touched files, review commit = working versions, CRLF normalised to LF), under 5 MB, secret-scanned. The scan only matched fake test sentinels and identifiers. Three test files that held literal `password=` fixtures were left out of the bundle: internal/discover/detect_applog_test.go, internal/keeper/audit_test.go, internal/server/case_sources_test.go.
Excluded by brief: web/dist, cmd/unravis-setup/ui/assets, .dev, sql, internal/blackbox/**, line-ending-only files, docs screenshots, D:\Projects\brain-payroll*.

## Jobs
| Job | Lane | Scope | Result |
|---|---|---|---|
| 37875 | droid-az-fw-gpt-6.1-sol-2 | full | failed, "hub restarted", no output |
| 37876 | droid-az-grok46 | full | failed, "hub restarted", no output |
| 37881 | droid-az-fw-gpt-6.1-sol-2 | full bundle | done |
| 37882 | droid-az-grok46 | full bundle | failed: 400, prompt 200763 tokens over the 200000 limit |
| 37889 | droid-az-grok46 | Go half | failed, "hub restarted" |
| 37890 | droid-az-grok46 | web half (46 files) | done |
| 37897 | droid-az-grok46 | Go half (43 files, retry of 37889) | done |

Triage legend: CONFIRMED = I read the cited code and the problem is real as described (a note says where only part was verified). PLAUSIBLE = code read, but impact or intent not verified, or likely by design. FALSE = contradicted by the code.

---
## Reviewer 1: droid-az-fw-gpt-6.1-sol-2 (job 37881), verbatim

## Major

1. **Time-zone edits relabel existing imports without correcting their timestamps**  
   Confidence: High. `web/src/components/sources/SourceEdit.svelte:75`  
   Import a host-local 10:00 event with the computer set to UTC, then change its zone to `Asia/Dubai` here. The source now reports Dubai, but the stored event remains at 10:00 UTC instead of 06:00 UTC. Saving changes only the host settings, not imported events.  
   **Fix:** Require reimporting affected files before displaying the new zone as their import zone, and test this transition.

2. **Timestamp probing ignores the supplied date order**  
   Confidence: High. `internal/importer/log4net/blocks.go:326`  
   `UTC Time: 05/10/2001 01:02:03` parses differently with `date-order=dmy` and `date-order=mdy`, as the new parsing tests demonstrate. `ProbeTime` passes neither setting to normalization, so it returns the same result for both and cannot agree with both imports.  
   **Fix:** Pass the supplied date order into probing and add `ProbeTime`/`Parse` parity tests for ambiguous dates.

3. **The suspect rail reports the wrong first-rise time**  
   Confidence: High. `web/src/lib/tracing/suspects.ts:144`  
   For twelve bars containing `[10,10,10,40,40,40,40,40,40,40,40,40]`, the engine reports slot 6, although the rise starts at slot 3. It searches only the second half, while the rail states that the count “first rose” at that time. Existing tests cover only midpoint rises.  
   **Fix:** Find the onset across the complete series, or explicitly label the result as a second-half marker; add an early-onset regression test.

4. **Enabled lanes incorrectly rule out clock errors**  
   Confidence: High. `web/src/lib/tracing/suspects.ts:162`  
   Any enabled lane with events makes Q1 green, meaning “clock/time-zone/DST error ruled out.” An accepted but incorrect time zone, or calls ending before they start, produces the same result because neither condition is checked. The accompanying explanation explicitly admits the latter omission.  
   **Fix:** Return unknown unless clock-error evidence was actually checked, and replace the test that expects green merely for enabled lanes.

## Minor

5. **Failed collection reads produce a falsely complete database count**  
   Confidence: High. `web/src/components/sources/SourcesList.svelte:68`  
   If a database has two collections, one returning 10 documents and the other failing, the failed result is discarded and the database displays “10 docs.” This contradicts `mergeStats`’s rule that an unknown contribution makes the total unknown.  
   **Fix:** Preserve failed contributions as unknown, or omit the aggregate; test mixed success and failure.

6. **Valid database names render inherited object properties**  
   Confidence: High. `web/src/components/sources/SourcesList.svelte:128`  
   Before statistics arrive, a database named `constructor` displays `function Object() { [native code] }`; `__proto__` displays `[object Object]`. The lookup reads inherited properties from the ordinary `sizes` object.  
   **Fix:** Use a `Map` or own-property-only lookup, and test these database names.

7. **Enter in a time select prematurely applies the entire window**  
   Confidence: High. `web/src/lib/components/WindowPicker.svelte:156`  
   Pressing Enter on an hour, minute, or AM/PM select prevents its native action and calls `commitDraft()`. Keyboard users trying to open or confirm the select instead close and apply the whole picker.  
   **Fix:** Leave Enter handling to native selects and add a behavioral keyboard test.

8. **The AM/PM control cannot be unselected**  
   Confidence: High. `web/src/lib/components/TimeBox.svelte:46`  
   Selecting PM on an empty time creates `12:00`, but this control offers no unset option. Unlike the hour and minute controls, it cannot clear its selection, violating the requested deselection requirement. Even passing a blank period to `pick` produces a time rather than clearing it.  
   **Fix:** Add an unset period option and handle it as clearing the time; test selection followed by deselection.

9. **Opening and closing label editing loses keyboard focus**  
   Confidence: High. `web/src/components/sources/SourceEdit.svelte:33`  
   Opening the editor removes the focused Edit button. Cancel and successful Save remove the focused form control. Neither transition moves focus to the replacement control, and changing the filtered environment can remove the whole row.  
   **Fix:** Focus the first input after opening and a surviving edit button or list control after closing; add DOM-level focus tests.

10. **The environment field accepts values the server rejects**  
    Confidence: High. `web/src/components/sources/SourceEdit.svelte:86`  
    The field permits 80 characters, but `MaxEnvNameChars` is 60. A 61-character ASCII environment is accepted by the form and then rejected when saved.  
    **Fix:** Enforce the server’s 60-character limit and test the boundary.

11. **“Show on chart” does not reveal the suspect’s hidden lane**  
    Confidence: High. `web/src/components/SuspectsRail.svelte:67`  
    Hide the SQL lane, then press “Show on chart” for a SQL suspect. Only its slot is passed onward, so the chart highlights a column while the SQL bar and its tooltip evidence remain hidden.  
    **Fix:** Pass `momentLane` with the slot and reveal that lane before focusing the chart; test the hidden-lane case.

Validation: Targeted in-memory reproductions ran. Full Go/Svelte suites were not run because the toolchain/dependencies are unavailable.

**Counts: blocker 0 · major 4 · minor 7**

### Triage of reviewer 1
| # | Verdict | Note |
|---|---|---|
| 1 SourceEdit.svelte:75 zone relabel | PLAUSIBLE | The log4net importer fixes UTC at import time with the host zone, so a later zone edit cannot rewrite stored events. Whether other importers resolve the zone at read time was not checked. |
| 2 log4net/blocks.go:326 ProbeTime ignores date order | CONFIRMED | ProbeTime calls readStamp(..., nil) at line 326. Parse builds `applog.NewDateOrder(opts.Supplied[applog.SuppliedDateOrder])` at line 415. |
| 3 suspects.ts:144 first-rise | CONFIRMED | riseOf scans only from `mid` (lines 135-152). The moment copy says "count first rose at ...". For [10,10,10,40 x9] slot 3 is the onset, slot 6 is reported. |
| 4 suspects.ts:162 clock Q1 green | PLAUSIBLE | Code is as described (an enabled lane with events gives green "contradicted"), but the why-text discloses that end-before-start is not checked. The label overclaims. |
| 5 SourcesList.svelte:68 failed reads | CONFIRMED | `.catch(() => null)` then a filter on null before mergeStats drops the failed part, so the total looks complete. |
| 6 SourcesList.svelte:128 inherited props | CONFIRMED | `sizes` is a plain object read as `sizes[row.database]`; "constructor" resolves to Object. |
| 7 WindowPicker.svelte:156 Enter on select | CONFIRMED | onDialogKey treats a select like a text box and calls commitDraft() on Enter. |
| 8 TimeBox.svelte:45-49 AM/PM has no unset | CONFIRMED | The period select has only AM and PM. Mitigated in part: choosing "--" on hour or minute clears the whole time. |
| 9 SourceEdit.svelte:33 focus loss | CONFIRMED | There is no focus() call anywhere in SourceEdit.svelte. |
| 10 SourceEdit.svelte:86 env maxlength | CONFIRMED | The input has maxlength 80; `MaxEnvNameChars = 60` (internal/casestore/store.go:491). |
| 11 SuspectsRail.svelte:67 hidden lane | CONFIRMED (rail side) | onShow(slot) passes only the slot (SuspectsRail.svelte:67, 96); momentLane is not passed. How the chart treats a hidden lane was not fully traced. |

---
## Reviewer 2: droid-az-grok46, web half (job 37890), verbatim

Web-half review of `0128e62..HEAD` (Go files are not in this clone).

## Blocker

None.

## Major

1. **major** (confidence: high) `web/src/lib/tracing/windows.ts:146` / `web/src/components/TracingPaper.svelte:314`  
   **Failing scenario:** User switches Local on, then reads the controls metaline.  
   The pickers still type UTC (`PICKER_TEXT.zone` is `'UTC'`), but the metaline says “Times are in **{timeZone.zone}** (change it with the UTC / Local switch…)”. A Local zone such as `Asia/Dubai` makes that line false: the boxes and the UTC echo are still UTC.  
   **Fix:** Drop `timeZone.zone` from that sentence; say the typed times are UTC.

2. **major** (confidence: high) `web/src/lib/components/TimeBox.svelte:25`  
   **Failing scenario:** Empty time (`-- -- AM`). User changes AM/PM first.  
   `pick()` fills 12:00 from the period, so AM→PM silently writes `12:00`. The hour and minute still show `--`. Next hour/minute pick then uses AM again (`parts` is still null) and overwrites that 12:00.  
   **Fix:** Ignore period-only changes while hour and minute are unset, or keep a local draft so AM/PM survives the next pick.

3. **major** (confidence: high) `web/src/lib/copy/fileKindsCopy.ts:218` / `web/src/components/wizard/ImportFilesPanel.svelte:409`  
   **Failing scenario:** Wizard Add files.  
   Cards advertise “.zip of them” / “.zip ok”, and `expand()` now unpacks zips in the wizard. `refusalOf()` still treats `.zip` as compressed (`fileKinds.ts:99`), and the supported line still says “Extract compressed files first.” A chosen zip is refused as compressed instead of unpacked.  
   **Fix:** Exempt `.zip` from `isCompressed` (keep gz/7z/etc. refused), and drop the extract-first copy for zips.

4. **major** (confidence: high) `web/src/lib/copy/wizardCreateCopy.ts:78` / `web/src/components/wizard/CreateProgress.svelte:87`  
   **Failing scenario:** File is a duplicate (`outcome.kind === 'duplicate'`). Status is “Rejected”; Remove rejected files also leaves it out.  
   Duplicates were “Nothing new”; now `fileStatusText` maps them to `statusNothing` which is `'Rejected'`. `removeRejected()` uses `failedFileKeys()`, so a duplicate is treated like a failed import.  
   **Fix:** Keep a distinct duplicate word, and do not feed duplicates into Remove rejected files.

5. **major** (confidence: medium) `web/src/lib/tracing/suspects.ts:215`  
   **Failing scenario:** A catalogue `must`/`expect` name is missing from `SIGNAL_TEXT` (or `SUSPECT_ENTRY_TEXT[id]` is missing).  
   `evaluateSuspects` does `SIGNAL_TEXT[name]` and `text.title` with no guard. A missing key puts `undefined` in the table, then throws on `text.title`. Tests currently cover the 26 ids, but the rail has no fallback.  
   **Fix:** Use `SIGNAL_TEXT[name] ?? name` and skip/replace entries with no copy.

## Minor

6. **minor** (confidence: high) `web/src/lib/copy/sourcesCopy.ts:233` / `web/src/components/sources/AddDataPanel.svelte:67`  
   **Failing scenario:** Two or more saved SQL/Mongo connections.  
   `connReady(n)` for `n > 0` is always “Saved connection ready” (singular). `held(n)` is `"n found here"` with no unit.  
   **Fix:** Restore count-aware copy (`n saved connections ready`, `n sources found here`).

7. **minor** (confidence: high) `web/src/lib/tracing/lanes.ts:321` / `web/src/components/TracingChart.svelte:104`  
   **Failing scenario:** 6-hour window, 5-minute bars (`bucketSeconds === 300`).  
   `barSizeParts` adds the “longer periods group more time…” clause only when the window is `> 24h`. `axisCaption` still uses `bucketSeconds > 60`. The bold “Each bar” line then omits the grouping note that the axis caption still has.  
   **Fix:** Use the same `bucketSeconds > 60` gate in `barSizeParts`.

8. **minor** (confidence: high) `web/src/components/TracingChart.svelte:147`  
   **Failing scenario:** Click “Show on chart”, then click a different bar (or use arrows). Resize the window, or a later `$effect` re-run with the same `focus`.  
   The effect keys off `focus` and `grid`, not `token`. It re-calls `revealSlot`, stealing keyboard selection back to the old suspect bar and scrolling the chart.  
   **Fix:** Depend on `focus.token` (or clear `chartFocus` after the first reveal).

9. **minor** (confidence: high) `web/src/components/sources/SourceEdit.svelte:29` / `web/src/components/sources/SourcesList.svelte:139`  
   **Failing scenario:** File with `tz.why === 'host'` (computer named, zone already saved).  
   Zone form is still shown (`why === 'host'`), so a good zone can be overwritten in place. `fixHelp` / Add files is hidden only for `no-zone` / `host-gone`, so this row also loses the “add the file again” path even though the import rule is still host-local.  
   **Fix:** Offer zone edit only for `no-zone` / `host-gone`. Keep Add files for a `host` file whose zone still needs a re-import.

10. **minor** (confidence: medium) `web/src/lib/fileKinds.ts:70`  
    **Failing scenario:** Serilog/NLog/log4net file whose times are the machine’s local clock, imported as `app-log`.  
    Host need was `'optional'`; it is now `'host'`. `needsTimeZone('app-log')` is still true (`varies`), but `needsHost` no longer implies `host-tz`, so the zone picker is not required. Import then fails later as unknown host, or is stored without a zone.  
    **Fix:** Use `'host-tz'` for `app-log` if those importers need both computer and zone.

11. **minor** (confidence: high) `web/src/components/SuspectsRail.svelte:96`  
    **Failing scenario:** Collapsed rail, 26 suspects, 3 green.  
    Button is `See all (26)` (`view.total`), not remaining hidden cards (`rest.length - top.length` plus green). Pinned Q-cards are counted again.  
    **Fix:** Pass the hidden count into `seeAll`.

12. **minor** (confidence: medium) `web/src/lib/copy/fileKindsCopy.ts:218` / `web/src/components/wizard/ImportFilesPanel.svelte:402`  
    **Failing scenario:** Wizard Add files.  
    Comment still says “the IIS one is IIS_GUIDE”, but an IIS card was added to `CARD_GUIDES` / `FileGuides`. The wizard still also renders `<IisGuide open={false} />`, so two IIS guides appear.  
    **Fix:** Remove the extra `<IisGuide>` (or drop the IIS entry from `CARD_GUIDES`).

13. **minor** (confidence: medium) `web/src/lib/sourceSize.ts:10` / `web/src/lib/sourceSize.ts:45`  
    **Failing scenario:** Stats JSON has `"docs":"218000"` (string) or one collection of a DB returns `docs: -1`.  
    `api.sourceStats` maps a non-number to `-1`. `mergeStats` then makes the whole DB count unknown, so a good collection’s count is dropped.  
    **Fix:** Coerce numeric strings; treat unknown parts as skipped rather than poisoning the sum.

---

**Counts:** blocker 0 · major 5 · minor 8

Checks: reviewed `git diff 0128e62 HEAD` (46 files, web half only). No Go tests run (Go files absent; instruction was not to rely on the toolchain). No new tests written. Existing tests that lock some of these bugs in: `timelineE.controls.test.cjs` (metaline wording), `hubfixE.sourcelist.test.cjs` (`why === 'host'` zone editor), `log4net.kinds.test.cjs` (app-log host need / zip copy).

### Triage of reviewer 2 (web)
| # | Verdict | Note |
|---|---|---|
| 1 metaline says times are in zone | PLAUSIBLE | windows.ts:143-146 says "Times are in {zone}" while the pickers type UTC. Ambiguous wording rather than a clear falsehood. |
| 2 TimeBox period-first | FALSE | pick() returns a full 12:00 or 00:00 value; the bound value is non-empty, so the boxes then show 12 : 00 with the period, as the component header documents. Hour and minute do not stay at "--". |
| 3 zip refused | FALSE | ImportFilesPanel expand() unpacks zips before refusalOf runs. The stale copy "Extract compressed files first." in fileKindsCopy.ts is a nit. |
| 4 duplicate shown as Rejected | PLAUSIBLE | wizardCreateCopy.ts:78 relabels the duplicate status as 'Rejected' (copy change). The claim that Remove rejected files drops duplicates is FALSE: failedFileKeys (wizardCreate.svelte.ts:173) matches only failed and skipped. |
| 5 missing SIGNAL_TEXT guard | PLAUSIBLE | A static catalogue covered by tests; a defensive-only point. |
| 6 connReady/held plural copy | CONFIRMED | sourcesCopy.ts:233-234: connReady(n>0) is always "Saved connection ready" and held(n) is "n found here". |
| 7 barSizeParts gate | PLAUSIBLE | lanes.ts:321 uses span > 24h; axisCaption (line 328) uses bucketSeconds > 60. May be deliberate. |
| 8 TracingChart focus effect | PLAUSIBLE | The $effect at TracingChart.svelte:146 depends on focus and grid, not token. Impact is minor. |
| 9 zone edit when why is host | PLAUSIBLE | zoneEditable includes 'host' by design (SourceEdit.svelte header comment). |
| 10 app-log host need | PLAUSIBLE | fileKinds.ts:70 changed to 'host'; the zone requirement for app-log was not traced. |
| 11 See all (N) count | PLAUSIBLE | SuspectsRail.svelte:96 passes view.total; a wording point. |
| 12 double IIS guide in wizard | CONFIRMED (structure) | ImportFilesPanel.svelte renders FileGuides (CARD_GUIDES has an iis entry) and `<IisGuide open={false} />` at line 409. Whether this is intended is unknown. |
| 13 sourceStats -1 poisoning | PLAUSIBLE | mergeStats documents "unknown makes the sum unknown" (sourceSize.ts:40); string coercion not verified. |

---
## Reviewer 2: droid-az-grok46, Go half (job 37897), verbatim

Reviewed `1e0a14b..HEAD` (Go half only). Three real defects.

### [major] SQLite no-op `UPDATE` is reported as missing tag (confidence 90)
**File:** `internal/casestore/sqlite/tags.go:222`

```222:226:internal/casestore/sqlite/tags.go
	if n, err := res.RowsAffected(); err != nil {
		_ = tx.Rollback()
		return casestore.SourceTag{}, c.mapDBErr(err)
	} else if n == 0 {
		_ = tx.Rollback()
		return casestore.SourceTag{}, casestore.ErrNotFound
```

**Failing scenario:** `PUT /api/cases/{id}/sources/{sourceId}` with env/portal/api that already match the row (open the editor, save with no edits). SQLite's `sqlite3_changes()` does not count an `UPDATE` that assigns the same values, so `RowsAffected` is 0. The handler maps that to 404 `"That source tag was not found."` The memory store still succeeds on the same input.

**Fix:** Look the row up by `id` (or `RETURNING`) and treat only a missing row as `ErrNotFound`; do not use `RowsAffected == 0`.

### [major] Row-limit abort drops the previous complete block (confidence 80)
**File:** `internal/importer/log4net/blocks.go:506`

```506:514:internal/importer/log4net/blocks.go
		if lb.header() && b.startsBlock(lb) {
			if e := acc.Row(no); e != nil { // before the flush, so a row limit emits nothing of this block
				return acc.Fail(e)
			}
			if e := flush(); e != nil {
				return acc.Fail(e)
			}
			b.reset(no)
```

**Failing scenario:** `MaxRows = 1` and a two-block file (or any file with `MaxRows+1` blocks). Starting the overflowing block calls `acc.Row` first and returns on the limit error **without** `flush()` of the block already in `b`. That previous block was already counted as a row when it started, but it is never emitted. Default `MaxRows` is 10_000_000, so this is the last accepted entry when a cap is hit; `MaxRows = 1` emits nothing. `TestBlocksRowLimitEmitsNothing` only checks the error type.

**Fix:** `flush()` the current block, then `acc.Row(no)` for the new one; on a row-limit error the completed block is already emitted and the new one is not started.

### [major] PUT can duplicate a Mongo database classification (confidence 75)
**File:** `internal/server/case_sources.go:466`

**Failing scenario:** Two `mongo-database` tags, same label, env `Production` and `Staging`. `POST` uniqueness is `label + env` (`postMongoDatabase`). `PUT` the Staging tag with `env=Production`. Both tags now share identity. `GET .../mongo-sources` returns duplicates; a later POST replace only removes by label+env and can leave the extra row.

**Fix:** In `putSourceTag` (or `UpdateSourceTags`), reject or collapse a `mongo-database` env change that collides with an existing label+env pair, matching `postMongoDatabase`.

---

**Count:** blocker 0, major 3, minor 0

### Triage of reviewer 2 (Go)
| # | Verdict | Note |
|---|---|---|
| 1 sqlite/tags.go:222 no-op UPDATE gives 404 | FALSE | SQLite's changes() counts rows matched by an UPDATE even when the values are unchanged; that behaviour is MySQL's. The editor also blocks a save with no change. |
| 2 blocks.go:506 row limit drops previous block | PLAUSIBLE | The order is acc.Row then flush(), so at the limit the last completed block is not emitted. The import fails with a LimitError anyway. TestBlocksRowLimitEmitsNothing asserts only the error. Low impact. |
| 3 case_sources.go putSourceTag duplicates mongo-database | CONFIRMED | putSourceTag (lines 426-466) has no label+env collision check and takes no tagMu, whereas postMongoDatabase replaces on label+env. Editing Staging to Production leaves two tags with one identity. |

---
## Summary
Reviewer-reported counts: Sol 0 blocker / 4 major / 7 minor; Grok web 0 / 5 / 8; Grok Go 0 / 3 / 0. Total 27 findings, no blockers.
My triage: 12 CONFIRMED (Sol 2, 3, 5, 6, 7, 8, 9, 10, 11; Grok web 6, 12; Grok Go 3), 12 PLAUSIBLE, 3 FALSE (Grok web 2, 3; Grok Go 1).
No fixes applied. Nothing staged or committed.
