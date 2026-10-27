# Manual tools

The manual tools are the same facts as the guided steps, one tool at a time, in any order. Use them when you already know what you want to look at, or when you want to try a window before you run all eleven steps.

## Two ways of working

Open a case, choose **Open guided analysis**, then open **Case actions** and choose **Switch to Manual tools**. The same menu switches back to **Guided analysis**. The choice is remembered for each case in this browser.

Switching never loses anything. Your notes, the checklist, the saved views and the last run are kept. A closed case can switch too, because switching only changes what is shown.

## The tools

Screen ids `p6-tools-1`, `p6-tools-2`, `p6-tools-3`.

The tools page is a grid of cards. Each card has the tool name, one line about it, the sources it reads and a status sticker, and one outlined button to open it. There are thirteen tools.

| Tool | Reads | Same as |
| --- | --- | --- |
| Response time over time | Audit | Step 2 |
| Slowest APIs | Audit | |
| Slowest calls | Audit | |
| Time split A C P O | Audit and IIS | Step 3 |
| Calls in flight | Audit | Step 4 |
| Nearby activity | Audit, IIS, SQL Server, MongoDB, Host | |
| Changes timeline | Audit and your change list | Step 5 |
| SQL blocking and waits | SQL Server | Step 7 |
| MongoDB health | MongoDB | Step 8 |
| Host CPU, memory and disk | Blackbox | Step 9 |
| SQL host disk, memory and network | Blackbox on the SQL host | Step 9 |
| After the fix | Audit | |
| Suspect summary | Audit and any other source in the case | Step 10 |

A tool that shows a step takes the status word of that step from the last run. A tool that has not run yet reads Waiting.

### A tool whose source is missing

A tool never shows an empty chart and never says it passed. When a source it needs is not in the case, its card reads **Cannot test yet** and the tool page says which source is missing and offers **Add by location**. That button opens the same drawer as the case home. The time split runs on audit alone and says what IIS would add.

### Inside a tool

Each tool opens with a **Back to tools** button, the incident and reference windows (folded away once they are set), the **Run the steps** bar, the tool itself, a place to write a note, saved views, and **Run Guided analysis instead**. The note is added to the checklist under What we learned.

**Slowest APIs** lists every API of the last run, ranked by its slow end in the incident window. Two filters narrow it: the fewest calls an API must have, and part of its name. An API with too few calls to have a slow end goes last, never first. **Slowest calls** lists the individual calls that took longest in a window. It shows the API, the start and the time of each call, and nothing about who sent it.

## Saved views

A saved view keeps a name, a tool, the two windows, the API focus and the tool's own filters. It does not keep a result, a request body, a URL or any secret. Opening a view puts the windows and filters back and opens the tool. It does not run anything.

- A name is plain text of up to 60 characters.
- A case holds up to 200 views. At the limit the page says so and asks you to delete one.
- On a closed case views can be opened but not saved or deleted.
- A damaged view opens with whatever is good in it.

## Export

Screen ids `p6-export-1`, `p6-export-2`. The export is the last tab of the guided analysis and is described with the other screens in [guided-analysis.md](guided-analysis.md#export-screens). It works from either way of working.
