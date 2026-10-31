# Round 5 Rulings, Explained Simply

**Read this first:**
1. Round 5 fixes 13 problems found in the plan for the tracing tool ("Unravis Tracing Paper").
2. Two rulings **reverse** earlier choices (R4-009 and R3-011); the rest refine or confirm locked decisions.
3. Nothing here is a new feature — it is corrections to wording, arithmetic and order of work.
4. Three items need your explicit OK (they reverse or refine owner choices); see the table at the end.
5. Terminology: a "lane" is one source of results in a trace; a "bucket" is one bar on a chart; "coverage" means which time ranges have been read.

---

## 1. One button cannot both read live data and refuse to [R5-01]

**What was wrong.** The plan said the chart button "may open a read-only live connection" and, in the same paragraph, that it "never re-reads a source." That is like a sign on a door reading both "Open" and "Do not enter."

**What I decided.** Three operations are now clearly separated: the trace button may do one throwaway read-only peek (saves nothing); the coverage screen only reads saved notes it already has and reports gaps; only the Pull button reads the customer's database and saves rows.

**Why.** Each operation needs one honest description, and "Pull" is the only one allowed to write.

**Cost if wrong.** A coverage screen might be mistaken for a live read when it is showing stale saved data.

**Does this reopen an earlier decision?** No — it sharpens R4-002 (which already allowed read-only peeks) without reconsidering it.

---

## 2. A test's arithmetic was simply wrong [R5-02]

**What was wrong.** A test claimed a 30-day window gives slices of 21,600,500 milliseconds (ms). Thirty days is exactly 2,592,000,000 ms, and dividing by 120 gives exactly 21,600,000 ms — the test was off by 500 ms, like a recipe saying "bake 30 minutes" while the timer is set 30 minutes and half a second.

**What I decided.** Correct the fixture to 21,600,000 ms, and derive every expected slice width by integer arithmetic instead of copying a hand-written example.

**Why.** A promise about exact bar widths must match the real division.

**Cost if wrong.** A wrong number in one test makes the whole test suite look untrustworthy.

**Does this reopen an earlier decision?** No — it corrects R4-001's example value without changing its 120-slice rule.

---

## 3. The "only two edge pieces" promise was false [R5-03]

**What was wrong.** For a window starting at 00:01, the old rule re-anchored the chart to that odd start, so every 6-hour slice straddled a real hour boundary, producing 240 fragments instead of the promised two.

**What I decided.** Keep slices on whole UTC hours ("UTC" is the world clock used for all data). If that would create more than 120 slices, move to a wider slice from the approved ladder (up to 24 hours), so there are never more than 120 slices and still only two edge pieces.

**Why.** The chart must obey its own limits, and whole-hour boundaries match the saved hourly rollups.

**Cost if wrong.** A 30-day chart starting off the hour may show 8-hour bars where you expected 6-hour bars.

**Does this reopen an earlier decision?** No — it refines R4-001 (keeps ≤120 and always-served) by changing only how the count is held down.

---

## 4. An export cannot be byte-for-byte identical [R5-04]

**What was wrong.** The plan implied confirming an export recreates exactly the same bytes as the preview. But each export uses a fresh secret pseudonym key and stamps its own creation time, so the bytes always differ.

**What I decided.** Promise only "same case snapshot, same hiding rules class" — not identical bytes. An optional extra check could compare a digest of the *inputs*, never of the output bytes.

**Why.** You cannot promise what the machinery cannot deliver, and a false integrity claim is worse than a narrow true one.

**Cost if wrong.** A user comparing files may see different bytes and worry data changed, when only the key and timestamp did.

**Does this reopen an earlier decision?** No — it refines R3-001 and R4-004 by narrowing the guarantee.

---

## 5. Two steps depended on a later step [R5-05]

**What was wrong.** The export steps were told to use a revision counter owned by a later packet that itself depended on them — a loop, like a cookbook saying "add the sauce (made in step 9)" while step 9 says "use the dish from step 4."

**What I decided.** Split out a small first packet (WP-65a) that builds the counter alone, then the export steps depend on it, then the confirm-integration packet.

**Why.** A foundational piece must exist before the things that rely on it.

**Cost if wrong.** Packets could report "done" while the counter they need does not yet exist.

**Does this reopen an earlier decision?** No — it confirms R4-004's intent (one named owner for `case.revision`) and repairs its edge order.

---

## 6. A "same window" check silently dropped late rows [R5-06]

**What was wrong.** The pull check said "this window was already pulled" by looking only at the first and last record. A new record arriving in the middle left that check unchanged, so the new data was dropped while coverage still claimed "complete" — like a librarian saying "I already counted this shelf" without looking at the middle.

**What I decided.** Compare a digest of the actual rows seen. Same rows means "no change"; changed rows means save a new import. Coverage now means "read as of this time," not "current."

**Why.** Real data arrives late, and a completeness claim must not hide that.

**Cost if wrong.** Late-arriving events could be missed and the chart would say it is up to date.

**Does this reopen an earlier decision?** No — it refines R4-006 and R2-008; the fingerprint/interval reuse stays.

---

## 7. Counting page by page can double-count or miss rows [R5-07]

**What was wrong.** The audit lane was described as walking raw rows page by page, with caps that could falsely report "complete" if a resume happened. A live dataset changing while you page can duplicate or skip records.

**What I decided.** Let the database count by bucket in one bounded grouping call per source, under the existing 60-second budget. Say plainly it is a live count begun at a stated time, not a frozen snapshot.

**Why.** A single grouped call is both cheaper and safer than merging raw pages.

**Cost if wrong.** A busy source could briefly show counts that include a row inserted mid-read.

**Does this reopen an earlier decision?** No — it refines R4-010 by mapping the bounded path to the code's real constants.

---

## 8. One true/false field hid which lanes were live [R5-08]

**What was wrong.** A single top-level `fromStored` flag described the whole trace, even though a trace can mix a live audit lane with saved SQL and IIS lanes. One flag, like a single "fresh" sticker on a bag holding both fresh and canned food.

**What I decided.** Every lane states its own origin (Live or Saved), its read time, completeness and reason. The old flag is kept only as `allLanesStored`, true only when every lane is saved.

**Why.** Users need to know which part of a result is live and which is from stored data.

**Cost if wrong.** Someone might treat a partly live trace as fully saved, or the reverse.

**Does this reopen an earlier decision?** No — it refines R4-005 and R4-008 by narrowing the old flag.

---

## 9. One bad duration threw away the whole file [R5-09]

**What was wrong.** An impossible duration (NaN, infinity, negative) caused the whole imported file to roll back with no record — so the one problem event, possibly the most important clue, vanished along with all its healthy neighbours.

**What I decided.** Keep the event with no duration and mark it `durationStatus: "invalid"`. It counts in `count_all` but not in `count_with_duration`, and no bad number is ever stored. (Win32 error codes elsewhere are unaffected.)

**Why.** In an incident tool, losing the suspicious event defeats the purpose of collecting it.

**Cost if wrong.** An event with a corrupt timing stays visible and filterable, and totals include it.

**Does this reopen an earlier decision?** **Yes — it reverses R4-009** (which chose to reject the whole event). This one needs your OK.

---

## 10. Two hiding rules can leak sensitive fields [R5-10]

**What was wrong.** One scrub rule protected the events view and a different rule protected exports. A field like a client IP ("Internet Protocol address," a device's network address) or cookie value could leak from a surface nobody checked.

**What I decided.** One privacy function is used by every outward view — events, exports, reports, tooltips. By default it drops `client-ip`, `referer` and every `cookie:*` key; including them requires explicit opt-in. A planted canary value tests every surface.

**Why.** One gate is easier to test than several, and a canary catches leaks automatically.

**Cost if wrong.** A sensitive field could appear where it should not.

**Does this reopen an earlier decision?** No — it refines R2-009 by centralising the projection.

---

## 11. Sharing one read between tabs added races for no gain [R5-11]

**What was wrong.** The plan built a subscriber-count and server-work-id system so two browser tabs could share one live read. No measurements justified it, and sharing can serve a stale read to a tab that asked fresh — coordination for its own sake.

**What I decided.** Drop it for MVP-1. Each tab does its own limited read (at most two live reads per source at once), cancelling one tab never affects another, and the client keeps a simple per-tab request counter.

**Why.** Fewer moving parts means fewer races, and the concurrency cap already contains the cost.

**Cost if wrong.** Two tabs with the same query each pay for one bounded live read.

**Does this reopen an earlier decision?** **Yes — it reverses R3-011 for MVP-1** (and R4-003's dedup). This one needs your OK.

---

## 12. The release check proved "not zero," not "all present" [R5-12]

**What was wrong.** The archived-test gate passed if the archive listed more than zero tests. That can pass even if most intended tests were accidentally dropped — like checking a suitcase is not empty rather than checking every item is packed.

**What I decided.** Keep a checked-in list of required test IDs. The release command fails unless every required ID appears in exactly one suite and both suites pass with non-empty reports.

**Why.** Retention of *specific* coverage is the real release condition.

**Cost if wrong.** A missing test could reach release unnoticed.

**Does this reopen an earlier decision?** No — it strengthens R4-011 from "non-zero count" to "manifest coverage."

---

## 13. A no-touch rule could hide a compile break [R5-13]

**What was wrong.** Packets were forbidden to edit any file outside their list, so an agent might leave a compile error unfixed to stay compliant — hiding the breakage rather than reporting it.

**What I decided.** Allow the smallest edit needed to compile or protect an invariant, but require it to be reported (file, symbol, one-line reason). Any change to a public contract, schema or route must stop and escalate.

**Why.** A small, reported fix is safer than a hidden break, while contract changes genuinely need review.

**Cost if wrong.** An agent could make an unnoticed out-of-list edit — mitigated by the mandatory report and diff summary.

**Does this reopen an earlier decision?** No — it refines R4-004/DEC7 R4-012's packet scoping with a bounded, reported escape hatch.

---

## Needs your OK

| Item | What it does | Alternatives | If wrong |
|---|---|---|---|
| **R5-09** reverses R4-009 | Keeps an invalid-duration event with `durationStatus:'invalid'` and null duration instead of rejecting the whole (in code, the whole file) event. | (a) keep-with-invalid (chosen); (b) reject whole event (R4-009); (c) file-level quarantine. | An event with an obviously corrupt duration stays visible and filterable with no timing, and `count_all` includes it. |
| **R5-11** reverses R3-011 (and R4-003) for MVP-1 | Removes `serverWorkId`/subscriber dedup. | (a) remove dedup, keep semaphore + `requestSeq` (chosen); (b) keep dedup with a canonical key encoding. | Two tabs with the same query each pay one bounded live read. |
| **R5-03** refines R4-001 | Replaces re-anchoring with a coarser UTC-aligned rung; a 30-day off-hour window may show 8 h bars. | (a) coarser aligned rung (chosen); (b) allow 121 buckets; (c) per-bucket sub-hour decomposition. | The 30-day chart may show 8 h bars where you expected 6 h. |
| **R5-01** refines R4-002 | The three-operation split is a wording choice consistent with locked intent; listed for visibility. | three operations (chosen) vs one shared caching operation. | A coverage screen could be mistaken for a live read. |
| **R5-04** wording choice | Guarantee scope ("same case, same hiding rules," not identical bytes); the optional input-manifest is at your discretion. | snapshot+policy class (chosen) vs byte-identical claim (unattainable). | A user sees differing bytes and worries data changed when only key and timestamp did. |

*Note: Win32 above means the IIS Win32 error code, not a Windows version.*
