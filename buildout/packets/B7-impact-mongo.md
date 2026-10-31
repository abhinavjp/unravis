# Packet B7: one-line "what this means for users" for the 12 Mongo cards, new file `parts/14-impact-mongo.js`

Do exactly this. Create ONE new file `buildout/wireframes/parts/14-impact-mongo.js`. Read ONLY `buildout/wireframes/parts/14-help-mongo1.js` and `buildout/wireframes/parts/14-help-mongo2.js` (use their `what`, `normal`, `issue` texts so the numbers you quote are the ones already written there; never invent a number or name). ES5 only.

File shape (first line verbatim, then exactly 12 assignments in the order of the table):
```
var EVIMPACT = (typeof EVIMPACT === 'object' && EVIMPACT) || {};
EVIMPACT['MG-BAD-SLOWOPS'] = '...';
```
Each value is ONE plain-English sentence for a beginner, 25 to 140 characters, ending with a full stop, single-quoted, containing none of `<` `>` `"` and no apostrophes (write "does not", not "doesn't"). It says what the card's result means for the people using the system. Tone by verdict: ok = nothing slowed users down; watch = users may start to feel it soon; bad = users likely felt slow or stuck work; none = we cannot say, name what is missing. Every sentence must be different from the others (the three ok cards and the three bad cards each talk about a different thing).

| id | verdict | what the sentence must convey |
|---|---|---|
| MG-BAD-SLOWOPS | bad | many database operations ran slowly, so pages that wait on them likely felt slow |
| MG-BAD-TICKETS | bad | the database ran out of free working slots, so new requests likely had to queue |
| MG-BAD-REPLAG | bad | the backup copy fell behind, so reads from it may show old data and a failover would lose recent writes |
| MG-WATCH-CACHE | watch | the working memory is filling up, so the database may start reading from disk and slow down |
| MG-WATCH-CONNS | watch | many connections are open, so new clients may soon be refused |
| MG-WATCH-GLOCK | watch | the database spent more time locked than usual, so some requests may start to wait |
| MG-OK-SLOWOPS | ok | operations ran at their usual speed, so users were not held up by the database |
| MG-OK-TICKETS | ok | the database always had free working slots, so requests did not queue |
| MG-OK-REPLAG | ok | the backup copy kept up, so reads and failover were safe |
| MG-NONE-COLLSCAN | none | we cannot say whether queries read whole collections because the profiler is off |
| MG-NONE-INDEX | none | we cannot say whether indexes are missing because the profiler is off |
| MG-NONE-OPLOG | none | we cannot say how much recovery history is kept because the oplog cannot be read |

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node --check parts/14-impact-mongo.js && node ../packets/data/check-impact.js parts/14-impact-mongo.js mongo
```

## Output
The full file in one code block headed `### parts/14-impact-mongo.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
