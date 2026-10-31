# Packet STALE: fix seven help strings that still say no marker line is drawn

Do exactly this. Do not explore the repo. There are NO new files: apply the 7 SEARCH/REPLACE blocks at the bottom of this file, each to the file named in its `###` line, and change nothing else in any file. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). No new globals; every edit changes only words inside one string literal of one `EVHELP` entry.

## Why these edits (the contract)

Packet AXIS made every evidence chart that has incident points draw one vertical dashed marker line at the incident start: line charts (`tlChart` in `parts/11-charts-a.js`), stacked-area charts (`stackedArea`) and bar charts (`evBars` in `parts/12-evidence-b.js`) all draw it now, at the first minute whose points have `win === 'incident'` (computed by `axIncidentT` in `parts/11-charts-axis.js`, wired through `parts/12-evidence-c.js`). Every fixture card that draws a chart has such points (they start at minute 60), so the marker is drawn on every chart in the product, stacked-area charts included.

Seven `chart` help strings still say the marker is NOT drawn, one per card below. Each edit replaces only the false clause with the true one ("a dashed vertical line marks the incident start", capital "A" where the clause starts a sentence) and leaves every other word byte-identical. No number or threshold changes. Everything else in these files stays exactly as it is: all line-chart copy about the dashed line is still true and must not be touched, the caption builder in `parts/12-evidence-f.js` is still true (every line-chart fixture has incident points) and must not be touched, and `parts/14-help-mongo2.js`, `parts/13-why-a.js`, `parts/13-why-b.js` and `parts/20-shell.js` contain no stale marker or axis copy and must not be touched at all.

The seven cards, with the full old and new value of each `chart` field (the only change is the clause about the marker line):

1. `parts/14-help-host1.js`, `EVHELP["GS-01-host-cpu-ok"].chart`
   old: `Stacked area in percent. Left half is the reference hour, right half the incident hour; no marker line is drawn. The band is CPU used (user+sys); taller means busier.`
   new: `Stacked area in percent. Left half is the reference hour, right half the incident hour; a dashed vertical line marks the incident start. The band is CPU used (user+sys); taller means busier.`
2. `parts/14-help-host1.js`, `EVHELP["GS-04-host-cpu-watch"].chart`
   old: `Stacked area in percent. Left is the reference hour, right is the incident hour; no marker line is drawn. The band is CPU used (user+sys); taller means busier.`
   new: `Stacked area in percent. Left is the reference hour, right is the incident hour; a dashed vertical line marks the incident start. The band is CPU used (user+sys); taller means busier.`
3. `parts/14-help-host2.js`, `EVHELP["GS-07-host-cpu-red"].chart`
   old: `Left half is the reference hour (a normal day), right half is the incident hour; no marker line is drawn. The shaded area is CPU used (user+sys), in percent of host CPU; taller means busier.`
   new: `Left half is the reference hour (a normal day), right half is the incident hour; a dashed vertical line marks the incident start. The shaded area is CPU used (user+sys), in percent of host CPU; taller means busier.`
4. `parts/14-help-mongo1.js`, `EVHELP["MG-WATCH-GLOCK"].chart`
   old: `Two stacked bands: readers waiting below, writers on top, in average operations. Left half is the normal hour, right half the incident hour. No marker line is drawn; look for the writer band getting thicker.`
   new: `Two stacked bands: readers waiting below, writers on top, in average operations. Left half is the normal hour, right half the incident hour. A dashed vertical line marks the incident start; look for the writer band getting thicker.`
5. `parts/14-help-sql1.js`, `EVHELP["sql-waits-benign"].chart`
   old: `Left half is the normal reference hour, right half the incident hour; no marker line is drawn here. Each band is one wait type: SLEEP_TASK, LAZYWRITER, PAGEIOLATCH_SH. A thicker band means a bigger share.`
   new: `Left half is the normal reference hour, right half the incident hour; a dashed vertical line marks the incident start. Each band is one wait type: SLEEP_TASK, LAZYWRITER, PAGEIOLATCH_SH. A thicker band means a bigger share.`
6. `parts/14-help-sql1.js`, `EVHELP["sql-lock-waits-rising"].chart`
   old: `Left half is the reference hour, right half the incident hour; no marker line is drawn here. Each band is one lock wait type (LCK_M_S, LCK_M_X, LCK_M_IX). A thicker band means more wait time.`
   new: `Left half is the reference hour, right half the incident hour; a dashed vertical line marks the incident start. Each band is one lock wait type (LCK_M_S, LCK_M_X, LCK_M_IX). A thicker band means more wait time.`
7. `parts/14-help-sql2.js`, `EVHELP["sql-waits-bottleneck"].chart`
   old: `Left half is the reference hour, right half the incident hour; no marker line is drawn here. Each band is one wait type, sized by its share of wait time in percent. A thicker band means a bigger share.`
   new: `Left half is the reference hour, right half the incident hour; a dashed vertical line marks the incident start. Each band is one wait type, sized by its share of wait time in percent. A thicker band means a bigger share.`

## Blocks

Apply each block to the file named in its `###` line. Each SEARCH fragment is 30 to 80 characters and occurs exactly once in its file (verified with `grep -c -F` before this packet was written). Edits are in file order; the two blocks in `parts/14-help-host1.js` and the two in `parts/14-help-sql1.js` are independent of each other.

````
### parts/14-help-host1.js
<<<<<<< SEARCH
right half the incident hour; no marker line is drawn. The band
=======
right half the incident hour; a dashed vertical line marks the incident start. The band
>>>>>>> REPLACE
### parts/14-help-host1.js
<<<<<<< SEARCH
right is the incident hour; no marker line is drawn. The band
=======
right is the incident hour; a dashed vertical line marks the incident start. The band
>>>>>>> REPLACE
### parts/14-help-host2.js
<<<<<<< SEARCH
no marker line is drawn. The shaded area is CPU used
=======
a dashed vertical line marks the incident start. The shaded area is CPU used
>>>>>>> REPLACE
### parts/14-help-mongo1.js
<<<<<<< SEARCH
No marker line is drawn; look for the writer band getting thicker.
=======
A dashed vertical line marks the incident start; look for the writer band getting thicker.
>>>>>>> REPLACE
### parts/14-help-sql1.js
<<<<<<< SEARCH
no marker line is drawn here. Each band is one wait type: SLEEP_TASK
=======
a dashed vertical line marks the incident start. Each band is one wait type: SLEEP_TASK
>>>>>>> REPLACE
### parts/14-help-sql1.js
<<<<<<< SEARCH
no marker line is drawn here. Each band is one lock wait type
=======
a dashed vertical line marks the incident start. Each band is one lock wait type
>>>>>>> REPLACE
### parts/14-help-sql2.js
<<<<<<< SEARCH
no marker line is drawn here. Each band is one wait type, sized
=======
a dashed vertical line marks the incident start. Each band is one wait type, sized
>>>>>>> REPLACE
````

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/14-help-host1.js && node --check parts/14-help-host2.js && node --check parts/14-help-mongo1.js && node --check parts/14-help-sql1.js && node --check parts/14-help-sql2.js && node ../packets/data/chk-stale.js
```
Expected: no syntax errors, then a single line `PASS` (the script vm-loads all six help files, asserts each of the 7 old clauses appears zero times and each new clause exactly once, checks the corrected copy inside the loaded `EVHELP` entries, confirms no help file anywhere still says "no marker line", confirms with `parts/11-charts-axis.js` and `parts/14-scenarios.js` that all seven cards really are stacked-area charts that draw the marker now, spot-checks two untouched neighbours for byte-identity, and runs `new Function(source)` plus an ES5 scan on every edited file).

## Block format
Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). There are no new-file blocks in this packet.

## Output
The applied blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
