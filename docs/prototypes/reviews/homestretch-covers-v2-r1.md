GOLDFISH REVIEW, round 1
Reviewer: claude-sonnet-5-5, effort: medium

## Tasks
1. Partly: On the Waiting state a chip reads "checks 5 changes", which suggests the waiting check will cover them. It looks like a plain tag, so I did not know it meant "all of them". Nothing on screen says "none will be skipped". When I switched to Running, Done passed and Done failed, the change list on Overview and Queue disappeared. After that I could not tell whether the changes saved during the run were included. The "One change only" and "More than 6" states show different counts, but nothing explains why. I answered "yes" only by guessing from the chip.
2. Partly: From Overview I found a link starting "See which…". It scrolls the page and the list seems to overlap it. It leads to Queue with the changes list (#/queue?changes=1), and "Show 13 more" opens the rest. I clicked it only because it was the one underlined phrase near the waiting card. The word "Queue" in the Overview side card has the look of a link but does nothing. Only the top nav "Queue" works. In the Running, Passed and Failed states the same path leads to an empty area, so the route only works when the state is Waiting.
3. Partly: In "Done failed", the run page shows red. A section called "Where it failed" holds text that is not a failure message, and the failed job id reads "shard-3-2" while other text says "shard 2 of 3". "See the log" opens a note saying "Prototype only". Job details say "Job details are unavailable in this snapshot". "Re-run all jobs" and Retry exist, but Retry also says "Prototype only". So I can see that something failed, but not what, and the visible next step goes nowhere.

## Findings (full list as returned; 21 items)
1. [blocker] [dead-end] "See the log" button on the Done failed run page. Opens a note saying "Prototype only"; the only way to learn the cause of red does nothing.
2. [major] [other] "Where it failed" section on the Done failed run page. Content is not a failure message; failed id "shard-3-2" vs "shard 2 of 3".
3. [major] [dead-end] "Job details are unavailable in this snapshot" on a run's job row. "Snapshot" has no meaning.
4. [major] [hidden-state] Change list on Overview and Queue in the Running, Passed and Failed states. It vanishes with no word.
5. [major] [wrong-signal] "checks 5 changes" chip on the Waiting card looks like a grey tag, not a clickable answer.
6. [major] [disconnected] "See which…" link on Overview. Page scrolls on its own, list appears to overlap the link area; works only in the Waiting state.
7. [major] [wrong-signal] "Queue" heading in the Overview side card looks like a link and does nothing. Same for "Latest release" text.
8. [major] [dead-end] Machines, Holds and Activity in the top nav show "Not part of this prototype"; at 390 wide they sit off-screen to the right.
9. [major] [wrong-signal] Pause and Cancel buttons on Queue show nothing visible.
10. [minor] [dead-end] "Move to top" on Queue and Retry on the failed run say "Prototype only".
11. [minor] [wrong-signal] Stat tiles on the Done passed run page show "–" in every tile.
12. [minor] [jargon] "Stopped", "Replaced", "Cancelled" status words.
13. [minor] [disconnected] "queued" chip on Queue vs "waiting" on Overview and Runs.
14. [minor] [jargon] "Re-run all jobs" on a Waiting run page.
15. [minor] [jargon] Releases labels "Tag", "Verify", "channel", "rc".
16. [minor] [jargon] "head", "shard", "Fast gate", "Full CI", "ci.yml" on run pages.
17. [minor] [jargon] "GitHub status … will get passed" line on a run page: unclear subject and tense.
18. [minor] [too-much-text] "Prototype only" and "Not part of this prototype" notes repeated on many screens; one banner would do.
19. [minor] [wrong-signal] Top "1 running · 1 waiting" text opens Runs, not the item, no sign it is a link.
20. [minor] [hidden-state] A tooltip stayed after Escape.
21. [minor] [other] The 390 wide nav row runs off the right edge with no sign more exist.

## Balance
too wordy in places and too sparse in others. The failure path (cause, log, job details) is too sparse; repeated prototype notes and run-step sentences are too wordy.

(Story test table, scan test and word lists omitted from this file for length; scan test: takeaway "Something is running and something is waiting, and some number of changes are involved", read not scan, about 90 words on the first screen.)
