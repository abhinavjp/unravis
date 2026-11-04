# Hub review, 2026-11-04 (uncommitted work: chunked audit lane, timeline UI, audit-DB add)

- Hub: https://hub.brainpayroll.ae, lane `droid-az-fw-gpt-6.1-sol-2` (probe ok, not paused), job **37649**, status done, exit 0, timeout 1800.
- Input: slim 2-commit repo (base = HEAD versions of the 22 in-scope files, HEAD = working-tree versions), 134 KB bundle. No `web/dist`, `cmd/unravis-setup`, `sql/`, `.dev`, `.claude`, secrets. Only the fake `hunter2` strings from the tests are in it.
- One round, one job. Banned lanes not touched. Hub verdict: FIX-FIRST. Raw hub answer: scratchpad `hub_result.txt`.
- Hub returned 24 findings (11 major, 13 minor). Triage below was done by reading the working-tree code.

Totals: CONFIRMED 14, PLAUSIBLE 7, REJECTED 3.
Note: items 9, 10 and 11 of the hub list were labelled pre-existing by the hub; they are real in the current tree but not caused by this work.

## CONFIRMED (ranked)

| # | Sev | file:line | Finding | Minimal fix |
|---|---|---|---|---|
| C1 | major | internal/server/tracing.go:950-951 (pre-existing), UI web/src/lib/tracing/windows.ts:152-155 | A failed audit source listing (`auditErr != nil`) drops the audit lane and adds only a warning; the UI ignores `warnings` entirely. With empty file lanes, `windowHasNoData` is true and the person reads "Nothing was found in this window" for a failed read. Breaks the "failed read is never shown as no events" rule. | On `auditErr` append an incomplete audit lane built from `auditFailure(...)` (state incomplete, reason `error`) instead of only a warning. |
| C2 | major | web/src/components/TracingPaper.svelte:100-112 | The `catch` only ignores `AbortError`. `fetchTracing` turns an abort during `response.json()` into `unreadable()` (a `CaseApiError`, model.ts:277-279). So a superseded read (source toggle, which this work makes frequent) or Cancel pressed during the body read hits the error branch: `result = null; status = 'error'` while the newer read is still loading, or after Cancel set `status` to ready/idle. | First line of `catch`: `if (mine.signal.aborted) return;`. |
| C3 | major | web/src/components/sources/AddAuditDatabase.svelte:139,65-78 | The saved-connection `<select>` is disabled only for `readOnly \|\| busy`, not `testing`. Test A, switch to B before A answers: `resetFrom(B)` runs, then A's test resolves and sets `held = A`, `tested = true`. Add then connects A's credentials under B's label (`recent.label`). | Add `testing` to the select's `disabled`, and ignore a test answer whose `savedId` changed (generation counter). |
| C4 | minor-major | AddAuditDatabase.svelte:45,99-118 | `canAdd` ignores `testing`, so Add stays clickable during a retest. If the retest fails, `held = null`; `tag()` then throws, and `wording(cause, held)` in the catch runs `secretsOf(null)` (`null.uri` TypeError). Unhandled rejection in the click handler; source is connected but untagged and no message is shown. | Add `!testing` to `canAdd`; take `const h = held` at the start of `add()` and use `h` everywhere. |
| C5 | minor | web/src/lib/tracing/sources.ts:137-147 | `mergeUniverse` never drops audit chips. All-answer with no audit sources left (last one disconnected) keeps the old audit chips (`listedAudit` false); a subset answer that lists audit sources only adds. A removed or renamed-away audit source stays as a switchable chip; toggling it sends a dead id (server 404, UI falls back to ALL once, chip stays). | Treat a response that did a listing as authoritative for the audit chips (replace them, including with none). The server omits `auditSources` when it made no listing call, so use that to tell the cases apart. |
| C6 | minor | web/src/lib/components/StampField.svelte:39-50 (WindowPicker inline effect 221-245 repeats it) | Sync is skipped while a part is invalid (`readParts` returns `previous`, which equals the new outside value). If the date box is bad and the value changes from outside, time text stays stale; blur repairs only the date, so the old time is shown and republished on the next edit. | In the effect, sync each part separately: replace the untouched (valid) part from the outside value. |
| C7 | minor | AddAuditDatabase.svelte:67,72 | After a successful test with a typed password, `password = ''` and the placeholder says the password is kept, but `inputOf(recent)` sends the empty field on the next Test, so a retest of a connection without a stored credential fails. | In `test()`, if the field is empty and `held?.savedConnectionId === recent.id`, reuse `held.password`. |
| C8 | minor | AddAuditDatabase.svelte:174 | After Add of an Application-data-only database (`connected` stays false), the tick boxes and collection field stay enabled. Changing ticks does nothing (Add is hidden by `tagged`), so the screen shows a classification that was not saved. | Include `tagged` in the fieldset and collection input `disabled`. |
| C9 | minor | WindowPicker.svelte:153-165, 275 | Escape is handled on the dialog only. After Tab moves focus out of the non-modal popover, Escape does nothing and the popover stays open. | Handle Escape in the existing `<svelte:window>` while `open`, or close on focus leaving `wrap`. |
| C10 | minor | tracing_audit.go:290-302 | A source that is OK in chunk 1 and absent from chunk 2 keeps its `OK`; the chunk is flagged failed by `auditSourceOutcome` (so the lane is incomplete) but `SourcesRead` still names the source as read. Metadata only, lane state is right. | Track per-source OK across every finished chunk; drop a source from `SourcesRead` when a later chunk lacks it. |
| C11 | minor | AddAuditDatabase.svelte:71,74 | Discovery is cut to 200 names silently, and `ConnectionTestResult.warning` (types.ts:10) is dropped. A restricted-discovery or partial list reads as "no databases" or plain success. | Show a sanitized `warning` line; say "first 200" when cut. |
| C12 | minor | WindowPicker.svelte:320-332,346-356 (StampField same) | `inputmode="numeric"` gives a digits-only keypad on phones, so `:` and `-` cannot be typed. | Use `inputmode="text"` (or drop it) on date and time boxes. |
| C13 | nit | web/src/lib/tracing/pickerModel.ts:84-87 | `formatSpan` drops minutes once over a day: 24 h 59 min shows "1 day", 48 h 1 min "2 days", no approximation mark. | Add "about", or include remaining minutes. |
| C14 | minor | WindowPicker.svelte:53-54,361 | A `hint` passed to the popover variant gets an `aria-describedby` id, but only the inline branch renders the hint element. Latent: no caller passes `hint` with the popover today (Step0Frame uses inline). | Render the hint in both variants or ignore it for the popover. |

## PLAUSIBLE (real code path, narrow or by design)

| # | file:line | Note |
|---|---|---|
| P1 | web/src/lib/tracing/sources.ts:81-85, server tracing.go:105 | Toggling one chip off sends the rest as explicit ids; explicit lists over 50 get 400 (shown as the bad-window text) while the default call allows 50 audit + 50 file chips. Only hits a case with more than 50 total sources. |
| P2 | web/src/lib/appState.svelte.ts:1223-1233 | `addDatabaseSource` completes into whatever case is current if the person switches case mid-add (same pattern as the existing `addSource`; sources are server-wide, the tag uses the originating `caseId`). Narrow timing. |
| P3 | MongoClassify.svelte:35,54 | `setAside` is component-local (pre-existing, comment says the draft does not keep it); after a remount a retest re-ticks a database the person had set aside. |
| P4 | MongoClassify.svelte:181 | Pre-existing: `connectedTo = uri.trim()` shows the typed URI (with `user:pass@` if the person typed it) in the page. Shown to the same person who typed it; not in this diff. |
| P5 | sources.ts:153,197 | Per-case sessionStorage entries are not cleared on case erase (same as `unravis.tracing.windows.`). Tab-scoped, small; quota failure is swallowed. |
| P6 | WindowPicker.svelte:149-151 | Hub wants focus restored to the trigger after an outside click. The browser already moves focus to body on that click, so this is an expectation, not a defect; keyboard Escape/Apply paths do refocus. Kept as design note. |
| P7 | AddAuditDatabase.svelte:81-86 | `pick()` while `connected` is true is blocked by the disabled fieldset only for ticks, not the database select (select is disabled only for readOnly/busy). Switching database after a connected-but-untagged failure resets `connected`, so the retry connects again. Duplicate source possible on that path. |

## REJECTED

| # | Hub claim | Reason |
|---|---|---|
| R1 | tracing_audit.go:129-142: sub-millisecond endpoints lose precision and a 24 h + 0.5 ms window skips chunking | `newTracingAxis` already snaps Start (floor) and End (`msUp`) to whole milliseconds (tracing_axis.go:106-118); `axis.Start/End` are ms-exact, so `UnixMilli()` loses nothing. |
| R2 | WindowPicker.svelte:359: `onchange` runs before `bind:checked`, so "Ends on a different day" keeps the old end date | Compiled with the project's Svelte 5.57.2: `bind_checked` registers a direct listener, `onchange` is delegated (`$.delegated('change', ...)`), and delegated handlers run later at the root. Binding is updated first; `toggleOtherDay` already assumes the new value. |
| R3 | (hub list 10 of 11 minor) Escape/focus on outside click is a defect | Folded into P6 above; not counted as a defect. |

Hub categories with no finding: (b) soft-deadline and cancel races and resource ownership, (d) sourceIds all vs none (no conflation on the normal path; `composeSourceIds` keeps `nothing` distinct), (i) test quality (no empty tests found), XSS (no `{@html}`; escaped interpolation), rune loops (none found). I agree with these from reading `readAuditLane` (cancel returns `fail` with no text, deadline keeps earlier chunks) and `composeSourceIds`.

## Fix order suggested
C1, C2 (user-visible wrong state), then C3 and C4 (same component, small), C5, C6, then the rest. All fixes are small and local; no design change needed.
