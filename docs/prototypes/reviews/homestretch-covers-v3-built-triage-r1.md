# Triage, goldfish round 1 on the built UI

Fixture artefacts (not product behaviour; the proxy rebuilds API answers from the prototype snapshot, so these cannot be fixed in the UI and are not live-data findings): Overview headline "Everything is passing" while a machine is offline (1), run page counters all "Passed" while Running or Failed (2, 3), moving start times and 17 h waits (4, 6), "0 of 1 machine online" next to a free slot in low-disk (5), Queue counters (11), job running on a machine last seen earlier (13), failed-run link (15). Recheck on live data once the `local` agent is back online.

Kept as approved in the prototype: "Machine problem: local is full" (12, prototype line 1190); "Start the Unravis agent on it" with a See machine link (8); the "Is the queue paused? Queue controls" question (9, triage decision); "slot N of M" with a hint on the Machines page (7).

Not in scope of this change, older components: Copy button fails in headless Chromium (21), Queue row click (17), Pause and Move to top banners (10), jargon in titles (18), Appearance panel (23), mobile clipping (20), text volume on Machines (22).
