GOLDFISH REVIEW, round 2
Reviewer: claude-sonnet-5-5, effort: medium

## Tasks
1. Partly: First screen Queue card "The next run will check 5 changes. See which" and develop chip "checks 5 changes"; Queue page "Will check 5 changes together". "Every" is never answered: no total of saved changes. Stopped runs in Runs ("a newer head 9888212cd2 arrived", "the pull request is no longer open") are not tied to the 5. In Running state "This run is checking 5 changes", "0 queued"; nothing says what happens to changes saved after it began. Reason for the wait is only "Waiting for a free machine."
2. Yes: chip "checks 5 changes" in the develop row, or "See which" after "The next run will check 5 changes." opens "Push develop · ci.yml" with "Changes this run checks" and 5 rows (#471, #468, #474, #477, #478). I knew where to click because the sentence precedes "See which"; the chip in a table row does not look like a link. Queue page chevron "Will check 5 changes together" expands the same list. In "One change only" Overview has no "See which".
3. Partly: red is "The master branch is failing." plus row "Failed master 9f20445 Go - tests (shard 2 of 3)". The headline does not click. The row opens "Push master · ci.yml" with "1 failed job recorded", a red card "Go tests (shard 2 of 3) (go-tests-matrix.shard-3-2)" and "See the log", which only shows "The full log opens on GitHub in the real app." No Re-run, no who or how. In Done failed the develop run says "This run failed. We cannot tell which change broke it." and marks all 5 "Marked failed on GitHub"; the only action is "See the log".

## Story test
(table condensed: 13 screens/states visited; every state reached; main gaps: Waiting/Running/Passed run pages have no next action and four "–" tiles; failed pages end at "See the log"; Machines/Holds/Activity are stubs.)

## Scan test
Takeaway in one sentence: master is red, develop is waiting on 5 changes, a pull request is running, with queue and releases links on the side.
Scan or read: scan (headline, red and yellow icons, "5 changes"); the right-hand card and two chips next to the headline needed reading.
Visual or wall of text: mostly visual, small tables. Run page heavier (187 words).
Heaviest block of text: "The next run will check 5 changes. See which" and the Queue column header "Where it is, or why it waits".
Rough words on first screen: 120

## Findings
1. [major] [no-next-step] "See the log" on master run page and develop failed state: shows one grey line, no log, no Re-run (only in a dashed Runs "..." menu).
2. [major] [no-next-step] "This run failed. We cannot tell which change broke it." above the list: all 5 rows "Marked failed on GitHub" read as 5 bad changes; no next step.
3. [major] [wrong-signal] Overview "The master branch is failing." headline is inert; only the table row clicks and nothing shows rows are clickable.
4. [major] [too-much-text] Queue counts "1 running / 1 queued / 1 waiting for a machine", header "1 running · 1 waiting", tab badge "Queue 2", card "1 running, 1 queued.": four counts, three words, 2 rows.
5. [major] [disconnected] "Stopped … a newer head arrived" / "Replaced" / "Cancelled" name the same state in 3 words; nothing says changes saved during a run are not lost.
6. [major] [hidden-state] develop row "Waiting for a free machine." / "queued 15:47 UTC" while captured 18:12: 2.5 h wait with no reason; Machines is a stub.
7. [major] [wrong-signal] "Will be marked passed on GitHub when this run ends" promises "passed" before anything ran.
8. [major] [wrong-signal] Run counters failed/running/waiting/passed all "–" in Waiting, Running and Passed, even "passed –" beside a "Passed" banner.
9. [major] [dead-end] Tabs Machines, Holds, Activity: only "Not part of this prototype"; Holds never explained.
10. [major] [jargon] Link text "See which" and chip "checks 5 changes" (chip looks like a static tag).
11. [minor] [too-much-text] "The run, in order" holds one entry or a fallback line; failed job appears twice, "shard-3-2" vs "shard 2 of 3".
12. [minor] [jargon] "Fast gate · PR 479", "Full CI · Push to develop", "Push develop · ci.yml".
13. [minor] [outside-knowledge] "change" defined only in the "?" on the run page.
14. [minor] [other] Mixed naming style in change rows (branch names vs one title).
15. [minor] [wrong-signal] Dashed-outline buttons look like main actions; Cancel on the running row shows nothing.
16. [minor] [wrong-signal] "Latest release 0.1.0-rc.68" chip and "1 waiting" chip do not click.
17. [minor] [other] Overview "Recent pushes" bars have no legend.
18. [minor] [too-much-text] Queue header "Where it is, or why it waits"; "local" unexplained.
19. [minor] [other] "Stopped … the pull request is no longer open" on PRs that are in the 5 changes.
20. [minor] [hidden-state] "captured 18:12 UTC" with no refresh and no statement that the page is a snapshot.
21. [minor] [hidden-state] Runs "..." menu "Copy link to this run" answers "Could not copy the link."
22. [minor] [other] 390 px: Machines/Holds/Activity tabs cut off; failing step name hidden on the master row.
23. [minor] [other] "Show state" and "Where this fits" controls cover table rows at the bottom edge.
24. [minor] [other] Releases: "Verify / Not recorded here", "rc channel", "stable channel unknown", planned-link note; no connection to the waiting check.

## Balance
right on the first screen, too wordy on run and queue pages. Worst: Queue header and counts line; five identical "Will be marked passed…" lines; failed job card repeating the name twice.

## Words I could not explain
Homestretch, Unravis, Fast gate, Full CI, Push develop / master, ci.yml, shard 2 of 3, head, Replaced / Stopped / Cancelled, Priority "Normal", Move to top, local, Holds, Verify, rc / stable channel, Tag, GitHub status, commit hash, UTC, Recent pushes bars.

## Outside knowledge used
pull request and merge feed develop; "master failing" means tests broke; "head" is the latest saved version; 7-character codes are version ids; "shard 2 of 3" is a slice of a test run; "machine" runs checks; "queued" and "waiting" mean the same.

(Saved with the story-test table condensed to a one-line summary; all tasks, findings, balance and word lists are verbatim.)
