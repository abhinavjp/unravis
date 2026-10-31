# Round 6 rulings, explained plainly

## Read this first
1. A reviewer checked our written plan for the evidence product and found nine defects in the words, not in any running code.
2. All nine findings were accepted and fixed by editing the plan; none reopens a decision the owner already made.
3. The biggest fixes: the fingerprint must cover the actual observed data, a changed re-pull must replace the old one instead of piling up, and the timeline buckets must stay consistent.
4. Two items are optional confirmations for the owner; everything else is a delegated repair.
5. A second, independent reviewer then read the whole revised plan, found 21 leftover wording slips, and they were fixed too. No code was run: this checks the written plan only, not that the software works, and nothing is guaranteed correct.

---

## 1. The fingerprint did not actually look at the data [R6-01]

**What was wrong:** The plan's fingerprint (a "content digest", a short code derived from data to tell whether two pulls are the same) was computed from only each row's timestamp and id. Two pulls with the same ids and times but different durations or status codes would look identical — like comparing two parcels by shipping label alone, never opening the box.

**What I decided:** Keep the fingerprint idea but widen it. Hash every observed, evidence-affecting field of each row in order, with explicit markers for missing versus present-but-empty values. Two pulls count as "unchanged" only if what was observed is genuinely byte-identical.

**Why:** A fingerprint that ignores the payload can silently call new evidence old, and that is exactly the kind of quiet loss this product exists to prevent.

**Cost if wrong:** A changed pull could be discarded as a duplicate, losing evidence without any alarm.

**Does this reopen an earlier decision?** No — it refines R5-06, the earlier coverage-fingerprint ruling.

---

## 2. The timeline grid contradicted its own short-window table [R6-02]

**What was wrong:** The plan anchors timeline buckets to whole UTC hours (a fixed global grid). Separately, it promised "a 1-hour view uses 60-second buckets" as a fixed rule. For a window starting at 10:07, the hour-aligned grid spans 10:00–12:00, which yields 120 buckets, not 60 — so two parts of the plan cannot both be true.

**What I decided:** Keep the owner-approved UTC-aligned grid. Define the "ladder" (the table of bucket sizes by window length) as the span-nominal size, then present the coarser of that and the size needed to keep the total count at or under 120. Rewrite the acceptance checks to test the aligned count, not the old "60 or 96 buckets" numbers.

**Why:** The owner already chose the aligned axis, and the fix is local to unlocked prose about short windows.

**Cost if wrong:** Some off-hour windows show a different bucket count than users might expect, so chart labels must state the width actually displayed.

**Does this reopen an earlier decision?** No — it preserves R5-03 (and R4-001); the reviewer's proposed replacement axis is rejected.

---

## 3. A changed live pull could double-count evidence [R6-03]

**What was wrong:** When a re-pull found changed content, the plan said to store a new import but never said to remove the old one. Totals are rebuilt from "all active events", so the old and new copies would both be added — like re-scanning the same shelf and counting every book twice. Windows that only partly overlap were similarly undefined.

**What I decided:** Make a pull an "observation" scoped by source, lane, fingerprint and window. A changed pull for a window that exactly matches a stored one must, in one transaction, validate the new data, delete the superseded import and its rows, insert the new one, rebuild the affected totals, then commit. A partly overlapping window is refused with an HTTP 409 error naming the remedy.

**Why:** Whole-window replacement is the smallest change that removes double-counting and reuses the existing import transaction and cascade deletes.

**Cost if wrong:** A partial-overlap refusal could annoy someone wanting an easy union, but a silent union risks inflated totals that look authoritative.

**Does this reopen an earlier decision?** No — it refines R5-06 and R4-007, and does not reopen D-7; keeping superseded versions would be a new owner decision not taken here.

---

## 4. "Exactly two raw edge scans" was simply false [R6-04]

**What was wrong:** The plan said every rollup-backed view needs exactly two small raw-data "edge" reads to complete the first and last partial buckets. For an hour-aligned window there are zero such partials; for windows with both boundaries in the same hour, one read can serve both sides.

**What I decided:** Say "zero, one, or two distinct boundary fragments, zero when aligned", with a same-hour single-read optimisation. The time and row budget applies per fragment, and exceeding it marks that fragment incomplete.

**Why:** The old claim was arithmetically wrong on the plan's own rules, and a wrong budget assumption could hide incomplete data.

**Cost if wrong:** If the same-hour optimisation is mis-implemented, a view might do a redundant read or, worse, double-charge its budget — hence the per-fragment test.

**Does this reopen an earlier decision?** No — it refines DEC6 R3-004 and R3-009, whose flags and budgets stand.

---

## 5. The work-packet graph and its waves disagreed [R6-05]

**What was wrong:** The dependency table (which work packet needs which) is the real source of truth, but the hand-written "wave" list and inline edge list beside it were incomplete and put packets in the same wave as things they depend on. A builder following the waves literally would start work before its prerequisites — like a recipe listing "bake" and "mix batter" on the same line.

**What I decided:** Make the dependency table the single authority, and generate both the inline edge list and the waves from it with a checked-in tool. The tool fails on unknown references, duplicates, or cycles, and a packet's wave is one more than its deepest dependency's.

**Why:** Hand-maintained schedules drift; generated ones cannot contradict the table.

**Cost if wrong:** A bad generator could mis-order work, but it fails loudly rather than silently, and tests compare generated output to the table.

**Does this reopen an earlier decision?** No — it refines DEC5 R2-014 and preserves the WP-58 edges from R5-05.

---

## 6. A flag meant for lanes was reused for coverage [R6-06]

**What was wrong:** `allLanesStored` means "every lane came from stored case data". It was also attached to the coverage endpoint's response, which is a list of coverage entries with no lanes at all. On an empty list the claim was vacuously true — like a certificate saying "all my pets are vaccinated" when you own none.

**What I decided:** Restrict the flag to tracing responses only. A coverage response returns its entries, a reference time, any gaps, and coverage-specific provenance; an empty ledger returns an empty list with state "no-coverage" and non-empty gaps, never a stored-complete claim.

**Why:** Reusing a lane-shaped claim on a different response shape produces a misleading promise of completeness.

**Cost if wrong:** A UI relying on the old flag could mislabel an empty coverage view as fully stored, so the tests check exactly that case.

**Does this reopen an earlier decision?** No — it refines R5-08 by restricting that field to its one valid shape.

---

## 7. "Complete" was read as "a stable snapshot" [R6-07]

**What was wrong:** The plan admits the live audit query is not a snapshot — rows arriving during the read may be included. Yet `complete:true` was described so loosely that a live lane and a saved lane could be compared as if both were frozen at the same instant. That is like comparing two store inventories taken minutes apart and calling any difference a theft.

**What I decided:** Define `complete:true` to mean only that the read command finished inside its budget with no cap, error or unfinished boundary. Add a per-lane `consistency` label (live-non-snapshot, stored-as-of, or snapshot), and when any lane is live, set a mixed-consistency flag and show a non-dismissible note that cross-lane totals are not a conservation check.

**Why:** The defect is a labelling and comparison problem, and naming the consistency axis fixes it without banning live reads.

**Cost if wrong:** If the mixed-lane note were omitted, two lanes read at different times could be misread as a precise cross-source total.

**Does this reopen an earlier decision?** No — it refines R5-07 and R5-08 and keeps D-5, the live audit lane.

---

## 8. The revision counter's owner packet claimed too much [R6-08]

**What was wrong:** Work packet WP-65a was scoped to one storage file but claimed its revision-bump function would be "called from every export-affecting mutation site". Those call sites live in other files. A builder could add the function with no callers and still pass — like installing a doorbell with no wires to any button.

**What I decided:** Split the work. WP-65a owns only the primitive and its storage tests. A new WP-65b owns wiring every mutation site, with each site named and its own before/after revision test. WP-65 (the export confirmation) depends on both.

**Why:** Separating the primitive from its callers makes each piece independently testable and stops a no-op from passing.

**Cost if wrong:** Without the split, an export could be confirmed against a revision that never changed, undermining the confirmation guarantee.

**Does this reopen an earlier decision?** No — it refines R5-05 and R4-004; the owner already approved WP-65a as the primitive.

---

## 9. The release checklist could quietly lose a test [R6-09]

**What was wrong:** A checked-in list of required test ids was its own authority. If a required test were deleted from the checklist and from both test suites, the release gate would still pass — like checking off a packing list you wrote after packing, rather than the customer's order.

**What I decided:** Generate the required id set from the accepted scenario inventory in the spec, and fail the release gate unless every id appears in exactly one suite, every suite id exists in the inventory, both test reports are present, and both suites pass. Additions and deletions are printed, and a deletion needs a reviewed baseline update.

**Why:** Comparing both directions catches both missing and orphaned tests, which a one-way list cannot.

**Cost if wrong:** A dropped regression test could reach release undetected, so the deletion path must be loud and require review.

**Does this reopen an earlier decision?** No — it strengthens R5-12 in the direction that ruling already intended.

---

## Needs your OK (answered 2026-10-31: both confirmed)

| Item | Ruling | What it is | Why it is optional |
|---|---|---|---|
| UTC-aligned time axis | R6-02 | Keep the floor-aligned presentation grid and coarsen when the bucket count would exceed 120, rewriting the old short-window bucket promises. | This preserves your earlier R5-03 choice, but the reviewer wanted a request-anchored axis; that is the one genuine taste question, so you can confirm or overrule it. |
| 409 refusal for a partly overlapping pull | R6-03 | A re-pull whose window only partly matches a stored observation is refused with a 409 and a named remedy instead of being silently merged. | This is the chosen overlap policy for MVP-2; whole-window replacement is settled, but you may want to confirm that refusing partial overlap is the behaviour you want. |

All nine verdicts are delegated rulings. None is owner-changing: they fix specification text within the frozen architecture and reopen no owner decision. The two rows above are the only items flagged as optional confirmations.