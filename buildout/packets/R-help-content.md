# Review packet R-help-content: is the beginner help correct, clear and honest?

You are an independent reviewer. Do not edit files. Read only: `buildout/packets/data/help-style.md` (the style rules), `buildout/wireframes/parts/14-help-sql1.js`, `14-help-sql2.js`, `14-help-mongo1.js`, `14-help-mongo2.js`, `14-help-host1.js`, `14-help-host2.js` (the help text, 35 cards), `buildout/packets/data/sql-cards.json`, `mongo-cards.json`, `host-cards.json` (each card's colour rule, table and chart type), and `buildout/wireframes/parts/12-evidence-f.js` (the chart caption).

Audience: a beginner who has never used SQL Server, MongoDB or host monitoring. They look at ONE card (colour chip, chart, table) plus its help text and must be able to decide alone whether it is a real problem.

## Check and answer with evidence (card id, field, quote under 15 words)
1. Facts: any statement about SQL Server, MongoDB or host metrics that is wrong or misleading (for example a wait type, a WiredTiger ticket, replication lag, run queue, swap meaning). List each with the correction.
2. Contradiction: any card whose `normal`, `issue` or `check` text disagrees with that card's `rule` or `verdict` in the data file (for example help says "problem" on a green card, or a threshold that differs from the rule).
3. ELI10 clarity: any sentence a 10 year old could not follow, or any jargon word used without being explained in that card's `terms` or its own text. List card id and the word.
4. Self-check ability: for each card, can a beginner follow `check` alone, using only what the card shows (chart, table) or something free to run? List cards where `check` needs data the card does not show or a tool the beginner has not been told about.
5. Gray ("not collected") cards: they must not imply a problem or a clean bill of health. List any that do.
6. Charts: from the data files and `12-evidence-f.js`, say whether a beginner can read each chart type (line, stackedArea, bars): scale, unit, which side is the incident. List the top 3 readability gaps (the charts have no y-axis numbers or time labels; the stacked chart has no incident marker).

## Output
A bullet list per item 1 to 6 (write "none" if clean), then `Verdict: ACCEPT | FIX` and the fixes as `card id | field | replacement text` lines. Be terse; the reply is capped near 16,000 output tokens.
