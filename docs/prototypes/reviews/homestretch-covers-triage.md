# Homestretch covers prototype: triage of goldfish rounds 1 and 2

Round 1: 11 items fixed (see r1 brief: honest failed wording, consistent passed rows, "Starts when the run above ends", back-link, state-switcher label, "run" not "request", nowrap PR numbers, branch-name titles, chevron + aria-expanded, footer, time labels, one-merge line). Rejected: file names/codes (same as the real dashboard), rows inert (prototype).

Round 2 (r2 report saved unchanged):
| # | Sev | Verdict | Reason |
|---|---|---|---|
| 1 | blocker | Fixed | "Open failing step" now shows an inline prototype note; verified by click test, no page errors |
| 2 | major | Fixed | Same button added on the Queue failed row, next to the sentence |
| 3 | major | Rejected | The run cannot tell which merge broke it; saying so is the honest state |
| 4 | major | Deferred to real UI | Prototype shows one fixed run by design; real Run page opens from a queue row |
| 5 | major | Rejected | ci.yml and fast-gate.yml rows on one commit are real data |
| 6 | major | Rejected | Terms match the real dashboard; help text is a real-UI "?" task |
| 7 | major | Rejected | State switcher is a test control, dashed and labelled |
| 8 | minor | Deferred to real UI | Highlight rule |
| 9 | minor | Deferred to real UI | Shorten the Queued card line; drop the third "5" |
| 10 | minor | Deferred to real UI | Label style and ordering of covered rows |
| 11 | minor | Rejected | Dot colours are the real dashboard tokens; the word and shape also carry state |
| 12 | minor | Deferred to real UI | Old-failure note under "Show 17 more" |
| 13 | minor | Deferred to real UI | Explain "local" |
| 14 | minor | Deferred to real UI | Links between the Queue and Run tabs |
| 15 | minor | Rejected | Prototype chip and footer are required honesty about demo parts |
