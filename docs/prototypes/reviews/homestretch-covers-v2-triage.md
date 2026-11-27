# Triage: homestretch-covers-v2

## Round 1
| # | Verdict | Note |
|---|---|---|
| 4, 6 | fixed | The change list entry stays reachable in every state: Overview and Queue show "checked 5 changes" / "will check 5 changes" with a working link to the list on the run page; "See which" no longer depends on the Waiting state and does not overlap. |
| 5 | fixed | The "checks 5 changes" chip on Overview becomes a real link to the list, with the same words. |
| 1, 2, 3 | fixed | "See the log" now shows the recorded failing job, step and reason first; the "Prototype only" tag moves to one line after it. "snapshot" wording removed from job rows. |
| 11 | fixed | Tiles with no recorded count get a tooltip "Not recorded for this run". Numbers are not invented. |
| 17 | fixed | Status wording: "Will be marked passed on GitHub when this run ends" / "Marked passed on GitHub". |
| 18 | fixed | One "Prototype" banner at the top; inline notes only appear after clicking an inert control. |
| 2 (column "Where it failed") | rejected | Real dashboard column name. |
| 7, 19 | rejected | Matches the real dashboard (card heading and "1 running · 1 waiting" are text there). |
| 8, 21 | rejected | Real dashboard behaviour: other tabs and mobile tab strip are not part of this change. |
| 9, 10, 14 | rejected | Real Queue and run-page actions, inert by design in a prototype; marked inert. |
| 12, 13, 15, 16 | rejected | Real dashboard vocabulary outside this feature; tracked as a separate wording pass. |
| 20 | rejected | Tooltips are the browser's own title tooltips. |

## Round 2
| # | Verdict | Note |
|---|---|---|
| 2 | fixed | Failed run page gets a next step: "Open the failing job on GitHub, fix it, then merge again." and per-change rows read "Part of the failed run" instead of "Marked failed". |
| 7 | fixed | Waiting/running rows: "Result is sent to GitHub when this run ends" (no promised outcome). |
| 8 | fixed | Stat tiles with no recorded numbers are left out; only the failed tile shows on a failed run. |
| 10, 13 | fixed | Link text "See which changes"; chip and "changes" carry the definition as a tooltip. |
| 11 | fixed | Failed job name shown once in the card; "shard 2 of 3" everywhere with the id in mono. |
| 1, 3, 9, 15, 16, 17, 21, 23 | rejected | Real dashboard or prototype-only controls; the real log opens on GitHub. |
| 4, 12, 18 | rejected | Real dashboard vocabulary outside this feature; separate wording pass. |
| 5, 19 | rejected | Real run reasons. Covered by the existing "stopped runs are replaced by a newer run" copy of the queue; not part of this view. |
| 6, 20 | rejected | Real recorded times; the snapshot cannot say why a machine was busy. |
| 14, 22, 24 | rejected | Real branch names and tabs; Releases link is only planned. |
