# Packet CH-sql-2: plain-language help for sql-waits-bottleneck, sql-blocking-chain, sql-tempdb-contention, sql-plan-regression, sql-deadlocks

Do exactly this. Read only these two files: `buildout/packets/data/help-style.md` (the style guide, follow it exactly) and `buildout/packets/data/sql-cards.json` (the cards). Do not explore anything else and do not edit any existing file.

Create ONE new file `buildout/wireframes/parts/14-help-sql2.js` with one entry for each of these card ids, in this order: sql-waits-bottleneck, sql-blocking-chain, sql-tempdb-contention, sql-plan-regression, sql-deadlocks.

For each id, find the card with that id in `sql-cards.json` and write the seven fields from the style guide using that card's `label`, `rule`, `table`, `why`, `chart` and `seriesNames`. A card with verdict `none` is a gray card (nothing collected); follow the gray-card wording in the guide.

## Check (run from the repository root; it must print `PASS`)
```
node buildout/packets/data/check-help.js buildout/wireframes/parts/14-help-sql2.js sql-waits-bottleneck,sql-blocking-chain,sql-tempdb-contention,sql-plan-regression,sql-deadlocks
```
If it prints FAIL, fix the file and re-run until it prints PASS.

## Output
The full file in one code block headed `### parts/14-help-sql2.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line).
