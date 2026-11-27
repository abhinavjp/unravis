GOLDFISH REVIEW, round 1
Reviewer: claude-sonnet-5-5, effort: low
Subject: Homestretch run-page prototype v2 (file as of 27 Nov 2026 12:06 UTC)

## Tasks
1. No: Nothing on screen says "release". The page is "Full CI on Push to master" with a "Failing" badge. The top bar has "Releases", but clicking it changed nothing. I can tell this run is not fine ("This run will fail. 2 of 7 started jobs have already failed."). I cannot tell whether "the release" is this run, something else, or whether it shipped. "Is everything fine" for a release cannot be answered from this screen.
2. Partly: What and where: the orange banner and two cards name "Package the app, step 16 of 23, screenshots tests of the tools themselves" and "Go tests, shard 3 of 3, step 5 of 5". The log panel shows red lines (121 "not ok 20 - run-screens.ps1 on a busy port ..."). What to do: no button says re-run, retry or fix. The only actions are "Show the failing lines" and "Copy the error". Card 1 says "it looks like a timing slip in the test, not the app". Card 2 says "this machine's account lacks a right those tests need". Neither tells me whether to do anything, or what. "Stop run" is greyed out with "not built yet".
3. Partly: Three headed columns: "1 · GET THE CODE", "2 · CHECKS, SIDE BY SIDE", "3 · FINAL GATE". Cards show status words ("running 13 min 30 s", "Waiting for a free machine slot", "not started"). Running now: "Linux checks, step 12 of 18" and "Go tests, shard 1 of 3, step 5 of 5". Next: "Final gate build", which "will fail when it runs". The order inside the middle column is unclear. The numbers #1 to #6 are not explained, and the order of "Go tests shard 2" and "Go race" vs the running ones is a guess. I could not tell whether anything will actually happen next, because the run is already doomed and nothing says what that leads to.

## Story test
| Screen / state | Where am I | What happened | Where is the problem | Steps | Everything fine | Next action |
|---|---|---|---|---|---|---|
| Landing (1440, dark): Runs / Full CI · Push to master | P: "Runs / Full CI · Push to master", but the PROTOTYPE strip and "nothing here is connected to the app" make me doubt it is a real run | P: "This run will fail." Header says "Failing", but the line under it says GitHub shows "still pending" | P: two cards name two jobs; the bottom panel shows only one | P: three columns, but "9 jobs" and "7 started" do not obviously match what I see | No, banner says so | N: nothing says what to do. Only "Copy the error" or read logs |
| Clicked "Package the app" card (default state) | P | P: step 16 failed after 28 s | Y: red lines 121+, "Jump to first error" | P: left list shows steps 1 to 23 with most collapsed ("7 passed steps hidden · 54 s") | No | N: no action beyond reading the log |
| Clicked "Show the failing lines" on the "Go tests, shard 3 of 3" card | P: the lower panel switched, but it is far below where I clicked and I saw no hint | P: "Step 5 of 5 failed ... 23 error lines. The first is on line 66" | P: first error shown is the dotnetdiag test, while the card above says 15 of 16 failures are in internal/setup | P | No | N |
| Clicked running job "Linux checks" | P | "Step 12 of 18 is running" | None shown | Step list with "Step 7 · no output (skipped or empty)" | Unknown | N |
| Clicked queued job "Go tests, shard 2 of 3" | Y | "Not started yet, so it has no steps." | n/a | n/a | n/a | N: "Its steps appear here as soon as it starts" is the only text |
| Clicked "Final gate build" | P: the lower panel did not change to show the gate; it kept showing a different job | I got no new information | n/a | n/a | n/a | N |
| Clicked "Which files changed" / "Getting the code" / "PowerShell scripts" (passed) | P | All steps passed | None | Step list; for "Getting the code" the log is a pile of git commands | Yes for this job | N |
| Filter chips "Failed 2 / Running 2 / Not started 4 / Passed 3" | Y | Cards dim; nothing is removed | n/a | n/a | n/a | P |
| Whole log tab / search box / line numbers | P | Whole log opened. Typing in "Search this log" produced no visible response I could detect. Clicking line number 121 changed nothing visible | n/a | n/a | n/a | N |
| Light / dark | Y | Theme switched | n/a | n/a | n/a | n/a |
| Top bar "Overview", "Releases", "Machines", and breadcrumb "Runs" | N | Nothing happened when clicked | n/a | n/a | n/a | N: dead end |
| Mobile 390x844 | N | Layout is broken (see finding 12) | n/a | n/a | n/a | N |

## Findings
1. [blocker] [no-next-step] Whole page: banner "This run will fail ...", the two red cards, and the bottom log panel.
   Expected: a clear action for a failure (re-run, retry, ignore, ask someone).
   What I saw / thought: only "Show the failing lines" and "Copy the error". The only action-like button, "Stop run · not built yet", is greyed out.
   Why it lost me: I know what broke but have no step I can take on this screen. I would have to copy the error and take it somewhere unnamed.
2. [blocker] [disconnected] Task framing versus the page: top bar "Releases" tab and page title "Full CI on Push to master".
   Expected: some words connecting this run to "the release".
   What I saw / thought: no word "release" appears on the page body. The "Releases" tab does nothing.
   Why it lost me: I cannot tell whether a release exists, shipped, or is blocked by this run.
3. [major] [wrong-signal] Top bar "Overview", "Releases", "Machines", and breadcrumb "Runs" (underlined like a link).
   Expected: navigation.
   What I saw / thought: clicking did nothing, no message, no change of URL.
   Why it lost me: they look live, and I only learn they are dead by trying them. The breadcrumb "Runs" looks like a link and goes nowhere.
4. [major] [wrong-signal] Header badge "Failing" versus the sentence "On GitHub this run shows as the check local/build, still pending."
   Expected: one verdict.
   What I saw / thought: here it is failing, and on GitHub it is pending.
   Why it lost me: I do not know which one counts. The next sentence ("Homestretch only watches for now: GitHub's own checks decide whether a pull request can merge") adds "pull request" to a page about a push to master, so I cannot tell whether anything is actually blocked.
5. [major] [disconnected] Count chips "2 failed / 2 running / 4 not started / 3 passed" and the sentence "2 of 7 started jobs".
   Expected: these to match the cards.
   What I saw / thought: I count 4 green cards ("Getting the code", "Which files changed", "PowerShell scripts", "Web app checks") but "3 passed". The headline mixes "jobs" and cards. The coloured bar's segments do not obviously match the numbers.
   Why it lost me: I cannot verify "is everything fine" from numbers that do not add up on screen.
6. [major] [disconnected] "Show the failing lines" buttons on the two cards, versus the lower log panel.
   Expected: to see the failing lines near the card I clicked.
   What I saw / thought: the panel is about 600px below. I saw no scroll and no highlight. Clicking the first card's button while it was already selected did nothing visible.
   Why it lost me: I thought the button did nothing.
7. [major] [disconnected] Card text "16 Go tests failed in 2 packages, and 15 of them hit the same Windows message" versus the log panel after clicking its "Show the failing lines".
   Expected: lines for the 15 failures in internal/setup.
   What I saw / thought: the first error line shown is the dotnetdiag one (line 66, "the server never saw the client hang up"). I did not find the setup lines without scrolling the 23 error lines.
   Why it lost me: the story on the card and the evidence below do not line up.
8. [major] [no-next-step] Cards' advice: "so it looks like a timing slip in the test, not the app" and "this machine's account lacks a right those tests need".
   Expected: whether to ignore, retry, or fix, and who does it.
   What I saw / thought: a guess about the cause, with no recommendation.
   Why it lost me: I would be guessing whether the run can still be trusted or what to change.
9. [major] [hidden-state] Strip under the nav: "real data from run 20261127-111001 (as it looked at 15:28, when you took the screenshot)" versus "Started 18 min ago" and "running 13 min 30 s".
   Expected: to know if the page is live.
   What I saw / thought: it is a frozen snapshot, but the page also says "running" and "finished, not streaming" for a log.
   Why it lost me: I cannot tell if "running" is still true, or if the numbers will change.
10. [minor] [other] Copy button "Copy the error".
    Expected: a lasting confirmation of what was copied.
    What I saw / thought: it shows "Copied" briefly (it disappeared on my next click). Nothing says what was copied or how much.
    Why it lost me: I cannot tell whether I have the right text.
11. [minor] [dead-end] Log toolbar: "Search this log", line numbers ("Click a line number to link to it"), the three icon-only buttons (copy, download, full screen).
    Expected: visible results.
    What I saw / thought: typing "busy" in search showed no highlight or match count I could detect. Clicking line number 121 produced no visible change. The icon buttons have only tooltips (I did not click them).
    Why it lost me: it is unclear whether search and links work.
12. [blocker] [other] Mobile 390x844.
    Expected: the same page, narrower.
    What I saw / thought: the content is squeezed into a roughly 110px column, the nav is cut off ("Machi"), and the page scrolls horizontally (scrollWidth 805). The banner is unreadable at that width.
    Why it lost me: I cannot use this screen on a phone.
13. [major] [jargon] Failed-step list: "Steps 17 to 22 · not run, because step 16 failed" followed by "1 passed step hidden · 1 s" (step 23).
    Expected: all steps after a failure to be not run.
    What I saw / thought: step 23 passed even though 17 to 22 did not run.
    Why it lost me: it contradicts "stops at failure" and is unexplained.
14. [minor] [jargon] Step rows such as "Step 7 · no output (skipped or empty)", "Step 2 · no output (skipped or empty)", "Step 10 · no output".
    Expected: skipped or empty, one or the other.
    What I saw / thought: it says both, so I do not know which.
    Why it lost me: I cannot tell if a step was skipped (bad?) or just quiet (fine).
15. [minor] [jargon] Step names that look cut or machine-made: "Go lint tool which version and where it is kept", "Go install golangci lint only when the cache had no usable", "Set up go the self test lists the signing tool s dependencie".
    Expected: readable names.
    What I saw / thought: names are cut mid-word or missing punctuation.
    Why it lost me: I cannot tell what the step did.
16. [minor] [other] "Final gate build" card: "Waits for 3 jobs that have not finished. It will fail when it runs, because Go tests 3/3 and Package failed." The chip list also shows five more chips.
    Expected: to see the gate's own status in the lower panel.
    What I saw / thought: I got no gate log, and "Desktop sidecar" (not started) is not among the chips while "Web" and "Which files changed" are.
    Why it lost me: I cannot tell what the gate checks beyond what is printed. Clicking it did not change the bottom panel.
17. [minor] [other] Job number chips "#1 ... #6" and the sentence "#1 to #6 have started, 3 are queued."
    Expected: a meaning for the numbers.
    What I saw / thought: probably start order or machine slot. Not stated.
    Why it lost me: they look like a ranking or an ID.
18. [minor] [other] Card "Getting the code", stage 1.
    Expected: a duration and the same shape as the other cards.
    What I saw / thought: no duration, a different look from "Which files changed", and it seems not to count in the "3 passed".
    Why it lost me: it is unclear whether it is a job.

## Words I could not explain
shard / "shard 3 of 3": "Go tests, shard 3 of 3" card and header
Go race tests: Stage 2 card
Desktop sidecar: Stage 2 card
Final gate / "gate": stage 3 header, "Final gate build"
build (monospace next to "Final gate"): Final gate card
local/build: line under the header
local: header chip (a machine? a mode?)
ci.yml: header chip
319ad09 / master: header chips (version? branch?)
baseline: "go tests compared with the baseline" (Linux checks)
machine slot: "Waiting for a free machine slot", "waited 9 min 41 s for a machine"
#1 to #6: job chips
Full CI: page title
Push to master: page title
Homestretch: top-left brand (the product name, never explained)
Package / "Package the app": what is packaged, for whom
Screenshots tests of the tools themselves: step name
security ID / owner of this object: card 2 quote
internal/setup, internal/dotnetdiag: card 2
"not ok 20", "ERR_ASSERTION", "deepStrictEqual", "TAP version 13": raw log lines
"Errors and context" vs "Whole log": the tabs, unexplained difference
"Blue outline: what it waits for and what waits for it": instruction line; the outlines are not easy to tell from the selection outline
temp folder: card 1

## Outside knowledge used
- That "Failing" / red means broken and that a "gate" is a last check that only passes if the earlier ones pass.
- That "shard 3 of 3" means one third of a test set split into parts, and that tests failing in "tests of the tools themselves" are separate from the product.
- That a "push to master" and a "pull request" are different things in Git (the header line mixes both).
- That "Stop run" being greyed out with "not built yet" is a prototype limit, not a real state.
- That cutting log lines to the "first error" is a normal way to read a log.
