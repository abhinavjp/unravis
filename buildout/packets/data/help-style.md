# Help text style guide (read fully; it is the only guide)

Reader: a beginner who has never used SQL Server, MongoDB or server performance counters. Write so a bright 10-year-old could follow. Short sentences. Everyday words. No unexplained acronym or product jargon anywhere in your text: if you use a term, either say it in plain words or put it in `terms`.
Accuracy first: use only (a) facts in the card data you are given and (b) standard, well-known facts about the product. Never invent numbers or tool names. When you state a threshold, copy the number and unit from the card's `rule` exactly.

## The seven fields (all required, all non-empty, plain text, no HTML, no double quotes inside the text)
- `look` (max 140 chars): one instruction starting with "Look at" or "Compare": which line, band or number to watch and which direction means trouble.
- `what` (max 260): what is measured and why anyone cares. You may use one everyday comparison.
- `normal` (max 200): what a healthy version looks like on this card, with numbers from the card.
- `issue` (max 260): how to tell it is a real problem: the rule's thresholds in plain words, and what that looks like on the chart or table. For a gray (not collected) card: say there is nothing to judge yet and why it matters.
- `check` (max 260): how the reader can confirm for themselves using only this card, for example "Compare the two numbers in the table: if the incident number is more than 30% bigger and 100 ms slower, it is real." For a gray card: the one thing that must be switched on, taken from the card's rule or why.
- `chart` (max 220): how to read this chart. Left half is the reference hour (normal day), right half is the incident hour. A dashed vertical line marks when the incident starts. Say what each line, band or bar is and its unit. For a gray card write exactly: "No chart: nothing was collected."
- `terms`: array of 1 to 4 objects `{ t: "term", d: "plain meaning" }` for the jargon that appears in the card label, table, rule or why (wait type names, p95, tickets, oplog, run queue, and so on). `d` max 130 chars. Every acronym you use anywhere must be explained in `terms` or in plain words.

## File format
ES5 only: `var`, no arrows, no template literals, no `let`/`const`. Use double-quoted strings. Each file starts with exactly this line:
`var EVHELP = (typeof EVHELP === 'object' && EVHELP) || {};`
then one assignment per card: `EVHELP["<card id>"] = { look: "...", what: "...", normal: "...", issue: "...", check: "...", chart: "...", terms: [ { t: "...", d: "..." } ] };`
Keep the card order given. LF line endings. No other code.
