# Packet CH-mongo-2: plain-language help for MG-OK-SLOWOPS, MG-OK-TICKETS, MG-OK-REPLAG, MG-NONE-COLLSCAN, MG-NONE-INDEX, MG-NONE-OPLOG

Do exactly this. Read only these two files: `buildout/packets/data/help-style.md` (the style guide, follow it exactly) and `buildout/packets/data/mongo-cards.json` (the cards). Do not explore anything else and do not edit any existing file.

Create ONE new file `buildout/wireframes/parts/14-help-mongo2.js` with one entry for each of these card ids, in this order: MG-OK-SLOWOPS, MG-OK-TICKETS, MG-OK-REPLAG, MG-NONE-COLLSCAN, MG-NONE-INDEX, MG-NONE-OPLOG.

For each id, find the card with that id in `mongo-cards.json` and write the seven fields from the style guide using that card's `label`, `rule`, `table`, `why`, `chart` and `seriesNames`. A card with verdict `none` is a gray card (nothing collected); follow the gray-card wording in the guide.

## Check (run from the repository root; it must print `PASS`)
```
node buildout/packets/data/check-help.js buildout/wireframes/parts/14-help-mongo2.js MG-OK-SLOWOPS,MG-OK-TICKETS,MG-OK-REPLAG,MG-NONE-COLLSCAN,MG-NONE-INDEX,MG-NONE-OPLOG
```
If it prints FAIL, fix the file and re-run until it prints PASS.

## Output
The full file in one code block headed `### parts/14-help-mongo2.js`, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <the printed output>`, `Concerns: none` (or one line).
