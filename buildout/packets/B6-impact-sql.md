# Packet B6: one-line "what this means for users" for the 11 SQL cards, new file `parts/14-impact-sql.js`

Do exactly this. Create ONE new file `buildout/wireframes/parts/14-impact-sql.js`. Read ONLY `buildout/wireframes/parts/14-help-sql1.js` and `buildout/wireframes/parts/14-help-sql2.js` (use their `what`, `normal`, `issue` texts so the numbers you quote are the ones already written there; never invent a number or name). ES5 only.

File shape (first line verbatim, then exactly 11 assignments in the order of the table):
```
var EVIMPACT = (typeof EVIMPACT === 'object' && EVIMPACT) || {};
EVIMPACT['sql-waits-benign'] = '...';
```
Each value is ONE plain-English sentence for a beginner, 25 to 140 characters, ending with a full stop, single-quoted, containing none of `<` `>` `"` and no apostrophes (write "does not", not "doesn't"). It says what the card's result means for the people using the system. Tone by verdict: ok = nothing slowed users down; watch = users may start to feel it soon; bad = users likely felt slow or stuck work; none = we cannot say, name what is missing. Every sentence must be different from the others.

| id | verdict | what the sentence must convey |
|---|---|---|
| sql-waits-benign | ok | the server only waited on normal background things, so users were not held up |
| sql-topqueries-orders | ok | the busiest queries ran at their usual speed, so order requests were not slowed |
| sql-log-growth | ok | the transaction log grew normally, so writes were not at risk of stalling |
| sql-lock-waits-rising | watch | queries are waiting on each other more than usual, so some requests may start to feel slower |
| sql-memory-grants | watch | some queries waited for memory to start, so a heavy report could delay others |
| sql-index-maintenance | watch | index upkeep ran at a busy time and may have slowed work while it ran |
| sql-waits-bottleneck | bad | the server spent its time waiting on one bottleneck, so users likely saw slow responses |
| sql-blocking-chain | bad | one stuck query held up a line of others, so many requests likely hung together |
| sql-tempdb-contention | bad | work queued up for the shared scratch area, so many requests were likely slowed at once |
| sql-plan-regression | none | we cannot tell if a query got slower because the query history is not available |
| sql-deadlocks | none | we cannot say whether queries were killed by deadlocks because live capture is stopped |

## Check (run from `buildout/wireframes`; must print `PASS`)
```
node --check parts/14-impact-sql.js && node ../packets/data/check-impact.js parts/14-impact-sql.js sql
```

## Output
The full file in one code block headed `### parts/14-impact-sql.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line). LF line endings.
