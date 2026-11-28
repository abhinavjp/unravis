GOLDFISH REVIEW, round 1
Reviewer: claude-sonnet-5-5, effort: medium

## Tasks
1. Partly: On Queue, the blue "Queued" row (push develop, ci.yml) says "5 merges, 1 run" and "Waits for the run above, then checks all 5 together." That answers "will they all be checked". It does not say whether a merge saved after this line was written joins the 5. "Waits for the run above" points at the "Running pr 479 into develop" row, which is a different kind of run (pr, not push), so I cannot tell what it is waiting for or why. I know only because one sentence says so, not because the screen shows it. In the "One merge only" state the Covers line and sentence disappear and nothing says the single change will be checked.
2. Yes: The button "Covers #471, #468, #474 and 2 more" says 5. Clicking it ("Hide covered merges") lists #471, #468, #474, #477, #478, each with its title and a short code. For the "More than 6" state the button reads "Covers #471, #468, #474, #476, #475, #472 and 3 more" and "9 merges". The names #477 and #478 only appear after a click. "Covers" does not say whether these are changes saved or opened.
3. No: In "Done failed" the row says "Failed ... job go-tests-matrix.shard-3-2, step 5 Go - tests (shard 2 of 3)" (Queue page). That names a place, not a change. On Run, "GitHub results" marks all five (#471, #468, #474, #477, #478) "covered by run on 9888212c: failed". I cannot tell which change broke it. Nothing says what to do: no button, no "next step" sentence. The Run page, where the merges are listed, does not show the job/step text at all. In "Done passed" a failed ci.yml row also appears (see finding 3), so I could not even tell whether the check as a whole passed.

## Story test
| Screen / state | Where am I | What happened | Where is the problem | Steps | Everything fine | Next action |
|---|---|---|---|---|---|---|
| Queue, State: Waiting (landing) | P (header "Homestretch / Queue / Run / Prototype" but no sentence saying what this is) | P: one thing running, one queued, two passed | N/A, none shown | P: Running, Queued, Recently finished; order readable, no step list | P: blue/grey/green dots, no overall statement | N: nothing to click except "Covers..." and "Show 17 more", which only expand |
| Queue, State: Running | P | P: push develop ci.yml "Running on local." with 5 merges | N/A | N: no progress, no step | P | N |
| Queue, State: Done passed | P | N: a Passed fast-gate.yml row and a Failed ci.yml row for the same code 9888212c both appear under a "passed" state | Y but contradictory: the Failed row | N | N: contradictory | N |
| Queue, State: Done failed | P | P: "Failed push develop ci.yml ... job go-tests-matrix.shard-3-2, step 5" | P: a job/step name, not a change | N | N | N: no action offered |
| Queue, State: One merge only | P | P: the queued push row has no Covers line | N/A | N | P | N |
| Queue, State: More than 6 | P | P: "Covers #471 ... and 3 more", "9 merges, 1 run" | N/A | N | P | N |
| Run tab, Waiting | N: the title "Push develop, checks 5 merges" does not say which queue row this is | P: Queued, 5 merges listed (3 shown) | N/A | N: no mention of waiting on pr 479 | P | N: only "show 2 more" |
| Run tab, Running | P | P: "Running" badge only | N/A | N: no steps | P | N |
| Run tab, Done passed | P | Y: "GitHub results" rows say passed | N/A | N | Y | N |
| Run tab, Done failed | P | P: "Failed", all five "failed" | N: not located; no job/step text on this page | N | N | N |
| Run tab, One merge only | N: just "Push develop / Queued / ci.yml 9888212c", an almost empty card | N | N/A | N | P | N |
| Run tab, More than 6 | P | P | N/A | N | P | N |

## Scan test
Takeaway in one sentence: a queue of build runs where one is running, one waits and covers 5 merges, and two passed.
Scan or read: read (the key fact about the 5 changes is a small underlined link and a grey sentence).
Visual or wall of text: mostly a list of text rows with tiny coloured dots; no numbers or icons stand out. Heaviest block: the blue "Queued" row (the "Covers #471, #468, #474 and 2 more" button, the "5 merges, 1 run" chip, and the sentence "Waits for the run above, then checks all 5 together."). On Run, the expanded list of merge titles with branch names is the longest text.
Rough words on first screen: 95

## Findings
1. [major] [no-next-step] "Failed" row, Queue State: Done failed, and the Run tab Done failed
   Expected: a way to see which change broke and a button or sentence telling me what to do.
   What I saw / thought: "job go-tests-matrix.shard-3-2, step 5 Go - tests (shard 2 of 3)" on Queue only; Run shows no cause. Nothing clickable leads to details.
   Why it lost me: I cannot finish task 3.
2. [blocker] [wrong-signal] Run tab, "GitHub results" list, Done failed
   Expected: one change marked as the culprit, or a note that it cannot be known.
   What I saw / thought: all five say "covered by run on 9888212c: failed". That reads as "all five are bad".
   Why it lost me: I would take a wrong action (treat all five as broken).
3. [major] [disconnected] Queue, State: Done passed, the "Failed push develop ci.yml 17:02" row beside the "Passed push develop fast-gate.yml" row, same code 9888212c
   Expected: if the state is "Done passed", everything shown passed.
   What I saw / thought: both a pass and a fail for the same code. The Covers button is under the Passed row, so are the 5 merges ok or not? Run tab Done passed shows "passed" for all five, ignoring the failed ci.yml.
   Why it lost me: contradictory result, no way to know which wins.
4. [major] [disconnected] "Queued" row says "Waits for the run above" and the "Running pr 479 into develop" row above
   Expected: the waiting check and the running check to be related.
   What I saw / thought: they are two different kinds of run (pr vs push) with different workflow files. The Run tab (Waiting) does not mention the wait at all.
   Why it lost me: I could not tell what unblocks the queued run.
5. [major] [hidden-state] Run tab, title "Push develop, checks 5 merges", and the "State:" dropdown above the page
   Expected: the Run tab to show the run I am looking at on Queue or a way to pick one.
   What I saw / thought: switching the dropdown changes the Run page, but there is no label on it saying which queue row it belongs to. It is a prototype control placed like a real filter.
   Why it lost me: the Run page is a free-floating screen, and I could not tell where I came from.
6. [major] [disconnected] Queue "Recently finished" and the 17 hidden rows, e.g. "Replaced push develop ci.yml 3e34a06b A newer request took its place."
   Expected: the cover relationship for a replaced run.
   What I saw / thought: when a run is "Replaced", I cannot tell whether its merges moved to the newer run. This matters for task 1.
   Why it lost me: the answer to "will every change get checked" for replaced runs is not on screen.
7. [major] [other] Run tab, left column "#471" etc. (all states with a merge list)
   Expected: "#471" on one line.
   What I saw / thought: the number wraps as "#47" over "1", "#46" over "8", at 1440 and 390 widths, in both the merge list and "GitHub results".
   Why it lost me: I read #47 and #46 and had to re-read them.
8. [minor] [jargon] Row label "Replaced" and the sentence "A newer request took its place."
   Expected: a plain status.
   What I saw / thought: "request" is not defined anywhere; is a replaced run lost?
   Why it lost me: unclear whether this is good or bad.
9. [minor] [jargon] Row line "pr 479 into develop", "push develop", "fast-gate.yml", "ci.yml", "6be1cf38", "9888212c"
   Expected: names a person can read.
   What I saw / thought: file names and 8-character codes in every row, unexplained. The same code 9888212c appears on three different rows, so the code does not tell runs apart.
   Why it lost me: I could not tell which row is which without a key. (outside knowledge)
10. [minor] [too-much-text] Run tab merge rows, "Merge pull request #471 from abhinavjp/claude/bb-wizard-match-proto"
    Expected: a short title.
    What I saw / thought: "Merge pull request #… from <person>/<branch>" repeats the number shown right next to it. Could be just the branch name.
    Why it lost me: noise.
11. [minor] [wrong-signal] "Covers #471, #468, #474 and 2 more" on Queue
    Expected: a plain count of changes saved.
    What I saw / thought: it is underlined like a link and expands in place, with a "Hide covered merges" toggle. Nothing says it expands. Meanwhile "show 2 more" on Run uses lowercase and "Show fewer" is capitalised differently.
    Why it lost me: I did not know it was a toggle until it changed.
12. [minor] [dead-end] Whole page: rows ("pr 479 into develop", the Failed row, each #471 line) look like items but clicking does nothing, and there is no link to GitHub
    Expected: a way to open the thing I am looking at.
    What I saw / thought: only the Covers link, "Show 17 more" and the tabs act.
    Why it lost me: no path from a row to detail.
13. [minor] [outside-knowledge] The footer "Covers list built from real merge commits; the live field ships with PR 479"
    Expected: nothing about builds.
    What I saw / thought: it is a note to the developer, and "live field" / "PR 479" mean nothing to me. It sits at the bottom, under every screen, in small grey.
    Why it lost me: I cannot tell if the Covers list is real or invented.
14. [minor] [hidden-state] Times: "Running ... 18:10 UTC" on a pr row that is above a "Queued ... 15:47 UTC" row and "Recently finished ... 16:22 UTC"
    Expected: one clock that makes sense.
    What I saw / thought: the running run started after the queued one and after the finished ones, but the "Queued" time 15:47 is earlier than the finished 16:21 and 16:22. Is 15:47 when it was queued? Unlabelled.
    Why it lost me: I could not tell what the time means (started, queued, finished).
15. [minor] [other] "Prototype" badge at top right, and the "State:" label with the values "One merge only", "More than 6"
    Expected: nothing shown to a real user.
    What I saw / thought: a test dropdown sits above a page that looks like the product. "One merge only" and "More than 6" are not states of a run.
    Why it lost me: I wondered if I should pick one.
16. [minor] [no-next-step] Run tab, State: One merge only
    Expected: some sign it will be checked.
    What I saw / thought: almost empty card "Push develop / Queued / ci.yml / 9888212c" and no sentence.
    Why it lost me: no information about what it covers.

## Balance
right on text quantity, too sparse on answers; the words that exist are not the ones the three questions need. Missing: which change failed, what to do on failure, what replaced runs cover.

## Words I could not explain
Homestretch: top left, the product name, no explanation
Queue / Run: the two tabs, what they are for
Covers: Queue row button, Run page footer
merges: "5 merges, 1 run" chip
run: same chip
fast-gate.yml, ci.yml: second line of every row
develop, master: "push develop", "push master"
pr: "pr 479 into develop"
Running on local.: Queued row, "local" in code font
Replaced / Cancelled: finished rows
shard 2 of 3, go-tests-matrix.shard-3-2: Failed row
job / step: Failed row
GitHub results: Run tab
UTC: every row time
the 8-character codes (6be1cf38, 9888212c): every row

## Outside knowledge used
- I treated "merge", "pull request", "push develop" and "covers" as save/combine/test words because of Git knowledge.
- I understood "shard 2 of 3" and "go-tests-matrix" as a test split only from knowing build tools.
- I guessed the 8-character codes are version ids and that "fast-gate" is a quick check and "ci" a full one.
- I treated "Failed" for the push as a build failure because of prior CI knowledge.
