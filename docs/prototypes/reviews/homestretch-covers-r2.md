GOLDFISH REVIEW, round 2
Reviewer: claude-sonnet-5-5, effort: medium

## Tasks
1. Partly: On the Queue tab, the Queued card says "Starts when the run above ends, then checks all 5 together." That is a promise for the 5 listed. The screen says "merges", never "changes" or "saved", so I had to guess they mean the same thing. Nothing says what happens to a change saved after the queue was formed. The Running card ("pr 479 into develop") is not in the covered list, and nothing says whether it is checked by its own run (I assumed yes). Nothing on screen proves the check will actually happen.
2. Yes: Queue card shows the chip "5 merges, 1 run" and "Covers #471, #468, #474 and 2 more". I had to click "Show 2 more" to see the rest: #471, #468, #474, #477, #478. Collapsed, I could name only 3 of 5. In the "More than 6" state the Queue card names 6 and says "and 3 more", while the Run tab shows only 3 and "Show 6 more", so the two screens disagree on what "more" means.
3. Partly: In "Done failed", the Failed card shows "go-tests-matrix.shard-3-2, step 5: Go - tests (shard 2 of 3)" and "Open the failing step on GitHub, fix it, merge again." That gives a place and an action. But on the Run tab the "Open failing step" button does nothing when clicked. The Queue card has no link at all. "Fix it" says nothing about how, and the Run tab says "We cannot tell which merge broke it." The marker is rust-orange, not red. I could not tell which of my 5 changes to fix.

## Story test
| Screen / state | Where am I | What happened | Where is the problem | Steps | Everything fine | Next action |
|---|---|---|---|---|---|---|
| Queue, "Waiting" (first screen) | P: "Homestretch / Queue" and a dashed "Prototype: show state" box. I did not know what Homestretch is or what is being queued | P: one Running item and one Queued item. The Queued one covers 5 merges | N/A, nothing red | P: Running -> Queued -> Recently finished. Nothing says what the Running run is for | P: no red, but "Waiting" and the dot colours are the only evidence | N: nothing to click except "Show 2 more" / "Show 17 more" |
| Queue, "Running" | P | P: the push develop run shows Running on local, started 16:21 UTC. There is no Queued card now | N/A | N: no progress or stage shown, only "started" | P: no failure shown, but also no sign of progress | N: no action offered |
| Queue, "Done passed" | P | Y: Passed push develop fast-gate.yml 9888212c finished 16:21 UTC, "Covers ... 5 merges, 1 run" | N/A | N: nothing says what ran | P: green "Passed" label | N: nothing to do, and no statement that I am done |
| Queue, "Done failed" | P | P: Failed push develop ci.yml 9888212c finished 17:02 UTC | Y-ish: "go-tests-matrix.shard-3-2, step 5: Go - tests (shard 2 of 3)" | N: I see only the failing step, not the steps before it | N | P: the text says "Open the failing step on GitHub", but no link or button here. The only button is on the Run tab, and it is dead |
| Queue, "One merge only" | P | P: Running pr 479 plus Queued push develop. The covers line is gone | N/A | N | P | N |
| Queue, "More than 6" | P | P: Queued run covers 9 merges | N/A | N | P: the chip says "9 merges, 1 run" | N: after expanding, nine rows with mixed naming |
| Run tab (any state) | P: it opens a run I never chose. "From the Queue: push develop 9888212c" looks like a back link | P: "Push develop, checks 5 merges" plus a status | in the failed state, the same step text and "We cannot tell which merge broke it" | P: the covered merges are listed, but not the steps of the run | P | failed state: the dead "Open failing step" button. Other states: no action |
| Run tab, "Done passed" | P | Y: "GitHub results": five rows saying "Run passed on 9888212c" | N/A | N | Y: all green | N |

## Scan test
Takeaway in one sentence: A list of automated runs with Running/Queued/Passed labels, where one queued run waits and covers 5 merges.
Scan or read: scan, partly. The status words and dots carry most of it, but I still had to read to know what "covers" means.
Visual or wall of text: mostly visual, with small rows, dots, and a "5 merges, 1 run" chip. Not a wall. The text is mostly short fragments of codes (fast-gate.yml, 6be1cf38).
Heaviest block of text: the Queued card's line "Covers #471, #468, #474 and 2 more ... Show 2 more ... 5 merges, 1 run ... Starts when the run above ends, then checks all 5 together." That is one crowded line, repeating "5" three times.
Rough words on first screen: about 95 (about 110 counting the state-picker options).

## Findings
1. [blocker] [wrong-signal] "Open failing step" button, Run tab, "Done failed" state
   Expected: it opens the failing step.
   What I saw / thought: it looks like a normal button. Clicking it changes nothing: no new page, no message, no sign it is disabled.
   Why it lost me: it is the only concrete action for the red state, and it does nothing. The label on its own tooltip even says "prototype only".
2. [major] [no-next-step] Failed card, Queue tab, text "Open the failing step on GitHub, fix it, merge again."
   Expected: a link next to that sentence.
   What I saw / thought: plain grey text with no link. The only button is on a different tab.
   Why it lost me: I am told where to go but there is nothing to click on the screen I am on. "Fix it" has no pointer to how.
3. [major] [disconnected] Run tab, failed state: "This run failed. We cannot tell which merge broke it." next to five "Run failed on 9888212c" rows
   Expected: some pointer to which change to fix.
   What I saw / thought: all five get an identical red "Run failed" line.
   Why it lost me: I am told to "fix it, merge again" but I do not know which of the five merges to touch. The five identical rows look like five failures but are one.
4. [major] [disconnected] "Run" tab in the top bar
   Expected: either a run I picked, or a list of runs.
   What I saw / thought: it always shows "push develop 9888212c", never the "pr 479" run that is Running, and never an older failure. The "From the Queue: push develop 9888212c" line looks like a back link; it takes me to the Queue tab.
   Why it lost me: no row on the Queue tab opens a run (I clicked card, title, hash, covers text; nothing happened). I cannot tell how I am meant to arrive on "Run", or why it shows that run.
5. [major] [disconnected] Queue rows in "Waiting": Queued "push develop ci.yml 9888212c" vs Recently finished "Passed push develop fast-gate.yml 9888212c finished 16:21"
   Expected: one result per commit, or a reason for two.
   What I saw / thought: the same code (9888212c) is already Passed and also Queued, and in the failed state it is Failed (ci.yml) and Passed (fast-gate.yml) at the same time.
   Why it lost me: I cannot tell whether the commit is good or bad. Nothing says ci.yml and fast-gate.yml are different checks.
6. [major] [jargon] Words "Covers", "merges", "run", "push develop", "pr 479 into develop"
   Expected: plain words I can place.
   What I saw / thought: "merges" and "covers" are never defined; the task says "changes saved". "pr", "push", "develop" are not explained.
   Why it lost me: I had to guess that merge = a saved change, and that "covers" = "is included in this check". (outside knowledge)
7. [major] [hidden-state] "Prototype: show state" dropdown (Waiting / Running / Done passed / Done failed / One merge only / More than 6)
   Expected: the state to follow from what happened.
   What I saw / thought: switching it silently rewrites the page and swaps which runs exist (for example "pr 479" disappears, "pr 477" appears). The Queue and Run tabs keep the state.
   Why it lost me: nothing on screen tells me the page changed or why. Times do not add up (queued 15:47 while Running started 18:10; failed 17:02 listed above 16:22).
8. [minor] [wrong-signal] Queued card's blue-left-border highlight on every first card
   Expected: highlight means "click me" or "problem here".
   What I saw / thought: the highlighted card is the Queued one in Waiting, and the Passed one in "Done passed". Clicking does nothing.
   Why it lost me: the highlight is shown on cards that are not clickable and are not problems, so it reads as a selection that leads nowhere.
9. [minor] [too-much-text] Queued card, "Covers #471, #468, #474 and 2 more   Show 2 more   5 merges, 1 run   Starts when the run above ends, then checks all 5 together."
   Expected: one short line.
   What I saw / thought: the number 5 appears three times in one row ("and 2 more", "5 merges", "all 5").
   Why it lost me: the layout already shows a chip. The sentence could be just the chip plus a "?" help.
10. [minor] [disconnected] Expanded covers lists: row names for #471/#468/#474/#477 are branch names (claude/bb-wizard-match-proto) but #476/#475/#472/#473/#478 are long titles ("Local CI: test that a web-flow committer keeps owner trust only when the owner pushed (RV-SEC-3, W-10, O-4)")
    Expected: one kind of label.
    What I saw / thought: mixed styles; the codes RV-SEC-3, W-10, O-4 mean nothing to me.
    Why it lost me: I cannot tell which line is which change. Order is not numeric (#476, #475, #472, #477, #473, #478).
11. [minor] [wrong-signal] Status dots: "Failed" dot is orange-brown, "Passed" green, "Queued" grey
    Expected: red for failed (task says "Something is red").
    What I saw / thought: the failed dot is rust, close to the Running blue and Queued grey in dark mode.
    Why it lost me: I had to read the word "Failed" to be sure; colour alone did not say it.
12. [minor] [hidden-state] "Show 17 more" under Recently finished
    Expected: more results of the same kind.
    What I saw / thought: expands to 17 rows of Replaced, Cancelled, Failed, Passed. Two Failed rows ("push master ci.yml 9f204457", "pr 473 ... job go, step 12") sit there with no next action, and no way to tell if they matter now.
    Why it lost me: hidden failures with no note whether they are old or still my problem. "Replaced" ("A newer run took its place.") and "Cancelled" ("the PR is no longer open") are not told apart from Failed in colour terms; I could not tell if they were fine.
13. [minor] [jargon] "Running on local."
    Expected: where the check runs.
    What I saw / thought: "local" is not explained (which machine?), and it only appears on the Running card, not on Queued or finished ones.
    Why it lost me: I cannot tell whether "local" is good or a warning. (outside knowledge)
14. [minor] [dead-end] Run tab, Waiting/Running/Passed states and "One merge only"
    Expected: something to do next.
    What I saw / thought: "Waiting for the run on the Queue tab to end." and "Checks this one merge on its own." end with no action and no link to that other run. "Covered merges get this result on GitHub when the run ends." says GitHub but gives no way to look.
    Why it lost me: I was sent to another tab without a link, and told to wait without a time or a way to be told.
15. [minor] [other] Top-right "Prototype" chip and "Homestretch" title
    Expected: chip = a label or button; title = home.
    What I saw / thought: neither does anything. "Prototype note: the covers list is built from the real recent merges. The live list arrives with the next dashboard update." is the only explanation of why things are partial.
    Why it lost me: I could not tell if the data was real or a demo. The footer reads as real status text.

## Balance
right; leaning too wordy on the Queued card. Worst places: (1) the Queued card's covers/"5 merges, 1 run"/"Starts when the run above ends..." row; (2) the failed card's "go-tests-matrix.shard-3-2, step 5: Go - tests (shard 2 of 3)" plus its instruction sentence; (3) the expanded covers rows with long titles and RV-/W-/O- codes. Replace the repeated "5" and the sentence with the chip and a "?" help.

## Words I could not explain
Homestretch: page title and top-left
Covers / covered merges: Queued card, Run tab
merges, "5 merges, 1 run": Queue chip and Run tab heading
push develop / pr 479 into develop: every row
fast-gate.yml, ci.yml: every row (two names; unclear why one commit has both)
9888212c, 6be1cf38 etc.: grey code beside each row (assumed a version id)
Running on local: Running card
go-tests-matrix.shard-3-2, step 5: Go - tests (shard 2 of 3): failed card
Replaced / Cancelled: Show 17 more list ("A newer run took its place", "the PR is no longer open")
GitHub results: Run tab
Prototype: show state: top of both tabs
UTC: every time

## Outside knowledge used
- Merge = a saved change that has been joined into the main code, and PR = a request to do that. The screen never says this.
- "develop", "push", "ci.yml", "fast-gate.yml" are build-system names; I guessed they are different kinds of check.
- A grey code like 9888212c is a version id.
- "Run" = one automatic check; "GitHub" is where results are shown. I understood the failing step only because I could guess "step 5 of a test job" means the place the test stopped.
- That a green/rust dot beside "Passed"/"Failed" means pass/fail came from the word, not the colour.
