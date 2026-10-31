# Packet D2 — Frame step (Step 0): behaviour and copy

## 1. Release (optional)

Builds on the existing `frame.releases[]` list (today labelled "Changes you know about (optional)"); this section specifies the parts the current page is missing: default-empty copy and a check against the incident window.

- **Field**: unchanged shape — "When (UTC)" (datetime-local) + "What changed" (text, 200 chars), added via a button, removable, capped at 50 entries. No new field added; this section only adds the two missing behaviours below.
- **Default state (no release added yet)**: the existing `"No change listed."` note stays, plus one new line directly under the "Changes you know about" legend:
  `"If you know of a deploy, flag flip or config change near the start of the slowdown, add it here. Unravis does not assume one happened."`
- **Scenario — no release notice given**: list stays empty, `releasesEntered` left unchecked. Page shows only the existing `"No change listed."` note. The steps run exactly as today (no release markers drawn in Step 5). No warning, because an empty list is a valid, deliberate answer.
- **Scenario — release time inside the incident window**: as soon as a release's "When" falls between `frame.incidentStart` and `frame.incidentEnd`, show this line directly under that row (role="status", non-blocking, does not disable Run):
  `"This change is inside the incident window (<time>, UTC). It may be the cause rather than just a marker — Step 5 will line it up against the onset."`
  `<time>` is the release's own UTC time formatted as today's onset line (`"26 Oct 09:14 UTC"` style).

## 2. Estimate line

One sentence, shown directly above the Run button, recomputed on every change to the windows or the source count. Inputs: total window length (incident + reference) and number of sources (SQL, Mongo, Host, API — whichever are configured for the case).

**Exact copy template**:
`"This reads ≈ <rows> calls from <n> source<s> and takes about <time>."`

Row/time numbers are **proposed** — R2 gives no number for Unravis, only examples from other tools — using an assumed ~20 calls/minute per source per window (2 sources x 2 windows; the demo fixture is far busier at about 600 calls/minute, so these are placeholders), to be replaced by real collector throughput later:

| Window length | Example string |
|---|---|
| 1 hour | `"This reads ≈ 4,800 calls from 2 sources and takes about 3 s."` |
| 1 day | `"This reads ≈ 115,000 calls from 2 sources and takes about 20 s."` |
| 30 days | `"This reads ≈ 3.4M calls from 2 sources and takes about 4 min."` |

For the 30-day case, append (same sentence, not a separate banner): `" Shown as hourly averages — see below."`

## 3. Overlap warning

Non-blocking banner (role="status") under the Reference window fieldset. Run always stays enabled; these only guide, per R2 §4–5 ("never block").

- **Overlap** (windows intersect):
  `"The reference window overlaps the incident window by <duration>. Comparing against a period that includes the problem is not a fair baseline."`
  Buttons: `"Move reference before the incident"` (shifts reference to end right before incident start, same length) and `"Keep anyway"` (dismisses the banner).
- **Touching** (reference ends exactly when incident starts, zero gap):
  `"The reference window ends the instant the incident starts, with no gap between them. Calls right at the boundary may fall in either window."`
  Buttons: `"Add a 5-minute gap"` and `"Keep anyway"`. (The 5-minute gap is **proposed**; R2 gives no number.)
- **Length differs by more than 2×** (the 2x ratio is **proposed**; R2 gives no number):
  `"The reference window is <ratio>× the length of the incident window (<refLen> vs <incLen>). Rates are compared per minute, but very different lengths make rare events harder to compare fairly."`
  Buttons: `"Match the incident window length"` (resizes reference to equal incident length, same end) and `"Keep anyway"`.

Only one banner shows at a time, priority: overlap > touching > length ratio.

## 4. Long windows

From R2 §1 (rollup by default) and §5 (pagination), applied to Unravis. R2 gives no exact threshold for this product, so the cutoff is **proposed**:

- **Rollup threshold (proposed)**: any window (incident or reference) longer than **24 hours** switches that window's charts from per-minute points to hourly averages.
- **Label shown** (replaces the per-minute chart's caption wherever it appears for that window):
  `"Showing hourly averages because the window is longer than 24 hours."`
- **What is lost**: stated once, directly under the label:
  `"Spikes shorter than an hour may not show as a peak at this scale."`
- **Pagination of the slow-calls table**: page size **25** rows (proposed; no paginated table exists in the wireframe yet; R2 suggests a hard cap plus "load more", Previous/Next is the chosen alternative). Footer copy:
  `"Showing 1 to 25 of <N>."`
  with `"Previous"` / `"Next"` buttons, `Previous` disabled on page 1, `Next` disabled on the last page. No "load all" option — matches R2's "pagination as the cancel mechanism."

## 5. Recurrence selector

A control beside the window fields, default `"One window"` (today's behaviour, unchanged).

**Options** (radio group):
1. `"One window"` — inputs: start, end (the two existing fields; no recurrence).
2. `"Same hours on the previous days"` — inputs: start time, end time (HH:MM UTC), number of days back (N, 1–30, proposed range).
3. `"Same weekday on previous weeks"` — inputs: weekday, start time, end time (HH:MM UTC), number of weeks back (N, 1–26, proposed range).
4. `"Last Thursday of every month"` *(weekday/ordinal fixed in the label; generalised control underneath lets the owner later generalise to "last `<weekday>` of every month")* — inputs: start time, end time (HH:MM UTC), number of months back (N, 1–24, proposed range).

**Summary sentence**, shown after any recurrence option other than "One window" is chosen, recomputed live:
`"Comparing <startTime>–<endTime> UTC on <description>, for <N> <unit> (<firstDate> to <lastDate>)."`
Example: `"Comparing 09:00–10:00 UTC on the last Thursday of the month, for 6 months (1 May 2026 to 29 Oct 2026)."`

**Calendar heatmap (day-of-week × hour)**:
- One cell per hour-of-week bucket, coloured by that bucket's historical value of the chosen metric (e.g. p95 latency).
- Colour meaning (legend text shown beside the grid):
  `"Darker amber = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour."`
- Cells matching the chosen recurrence (e.g. every "last Thursday 09:00–10:00") get a highlighted border, distinguishing "every Thursday" from "last Thursday."

**Overlay of N occurrences**:
- Draws each matched occurrence as its own line, aligned to the same relative offset (t=0 = window start).
- Max overlay lines: **8** (proposed, from R2's "cap at roughly 6 to 10 lines" guidance).
- If the recurrence matches more than 8 occurrences, show the 8 most recent and note:
  `"Showing the most recent 8 of <N> occurrences; all <N> appear in the heatmap."`

## 6. Time zone and DST

R2 contains no explicit DST rule for this product (the research doc's recurrence/calendar-alignment material does not cover it); the following paragraph is **proposed**, built from the wireframe's existing UTC-only design (`frameForm`'s current note: *"Times are UTC... enter them as UTC"*):

All window and recurrence times in Step 0 are entered and stored in UTC only; there is no timezone picker. A recurrence rule like "same hours on previous days" or "last Thursday of every month" matches the same **UTC** clock time on each occurrence, not the same local wall-clock time — if a reader's local timezone observes daylight saving, the occurrences will appear to shift by an hour in their local clock across the change, even though the UTC times line up exactly. Step 0 does not convert or adjust for this; it only flags it.

**Notice copy** (shown when a chosen window or recurrence range crosses a DST change in any commonly-used zone, e.g. US or EU clocks changing):
`"This window crosses a daylight-saving change on <date>. All times above stay in UTC; the same UTC time may map to a different local clock time before and after this date."`

## 7. Test scenarios

| scenario | setup | what the page must show |
|---|---|---|
| No release notice given | Leave Release list empty, run the steps | `"No change listed."` stays; no warning; Step 5 shows no release markers |
| Release inside incident window | Add a release at 09:20 UTC with incident window 09:00–10:00 UTC | Inline line: `"This change is inside the incident window (09:20 UTC)..."`; Run stays enabled |
| Estimate for a 1-hour window | Incident + reference each 1 h, 2 sources configured | Run bar shows `"This reads ≈ 4,800 calls from 2 sources and takes about 3 s."` |
| Estimate for a 30-day window | Incident window set to 30 days | Run bar shows the 30-day estimate string plus `" Shown as hourly averages — see below."` |
| Overlapping windows | Reference window set to overlap incident by 3 days | Non-blocking banner: `"...overlaps the incident window by 3 days..."` with `"Move reference before the incident"` / `"Keep anyway"`; Run still enabled |
| Touching windows | Reference end = incident start, no gap | Banner: `"...ends the instant the incident starts, with no gap..."` with `"Add a 5-minute gap"` / `"Keep anyway"` |
| Reference far longer than incident | Incident 1 day, reference 10 days | Banner: `"...is 10× the length of the incident window (10d vs 1d)..."` with `"Match the incident window length"` / `"Keep anyway"` |
| Long window triggers rollup | Incident window set to 3 days | Label `"Showing hourly averages because the window is longer than 24 hours."` plus the lost-spikes line |
| Slow-calls table paginates | 140 slow calls in the result | Footer reads `"Showing 1 to 25 of 140."`, `Previous` disabled, `Next` enabled |
| Recurrence: same weekday on previous weeks | Choose that option, weekday Thursday, 09:00–10:00, N=6 | Summary sentence with computed date range; heatmap highlights 6 matching cells; overlay draws up to 6 lines |
| Recurrence: last Thursday of every month, N over cap | Choose that option, N=12 | Overlay shows 8 most recent with `"Showing the most recent 8 of 12 occurrences; all 12 appear in the heatmap."`; heatmap still marks all 12 |
| Window crosses a DST change | Incident window spans 1 Nov 2026 (a US DST change date) | Notice: `"This window crosses a daylight-saving change on 1 Nov 2026..."` shown near the window fields, non-blocking |

Status: PASS
Checks: n/a
Concerns: sections 1, 4, 5 (overlay cap), and 6 use numeric thresholds marked `proposed` because R2 gives no exact figures for this product; a later packet should confirm them against real collector throughput before implementation.