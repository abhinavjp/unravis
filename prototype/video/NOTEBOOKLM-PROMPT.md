# NotebookLM: complete prompt and upload list

Copy the prompts in the grey code blocks. Everything else is instructions for you.

## 1. What NotebookLM can and cannot do

- It cannot open a GitHub repository, run the HTML prototype, or use a link that needs a login. It reads **sources you add to a notebook**: uploaded files (Markdown, text, PDF, images), pasted text, public web pages, Google Docs. [Likely; menus may have changed]
- A notebook has a limit on the number of sources (50 on free accounts, more on paid; check your plan). [Likely] This pack is built to stay well under it: about 16 sources.
- You do not control exact visuals or timing of a generated video. Check each video against the checklist in section 5 and regenerate with a correction sentence if something is wrong.
- Nothing here needs an outside video. The notebook is self-contained.

## 2. What to upload (about 16 sources)

Everything lives in the repo `abhinavjp/unravis`, branch `prototype/tracing-paper-e2e`. Download the folder `prototype/video/` and the three files marked "outside the folder".

| # | File | What it gives NotebookLM |
|---|---|---|
| 1 | `prototype/video/NOTEBOOKLM-SOURCE.md` | The story, plain-English glossary, demo facts, ten tour scenes, the 25 suspects, honest limits, words to avoid |
| 2 | `prototype/video/FLOWS-GUIDE.md` | Every flow, step by step: launch and master key, start screen, create a new case (all four ways to add sources), open an existing case, enter by code, restore, case home, add files, analysis, export, Investigation Timeline |
| 3 | `prototype/video/HOW-THINGS-ARE-CALCULATED.md` | How every number is calculated, beginner level, with worked examples (P95 and the rest) |
| 4 | `prototype/video/WHAT-EACH-DATA-SOURCE-BRINGS.md` | What each log or counter tells you, what it cannot tell you, what must be switched on |
| 5 | `prototype/TRACING-SCREEN-EXPLAINER.md` (outside the folder) | The long explainer of the Investigation Timeline screen |
| 6 to 15 | `prototype/video/gallery/GALLERY-01 … GALLERY-10 *.pdf` | 257 screenshots of every state, one per page, each with a caption saying what it shows and how to reach it |
| 16 | `prototype/NAMING-DECISION.md` (outside the folder, optional) | Why it is called Investigation Timeline |

Optional: the ten PNGs in `prototype/video/images/` (the main screen and its parts). The same states are in the galleries, so skip them unless you want clean single images.
Not needed: `FLOW-COVERAGE.md`, `capture-flows.cjs`, the individual PNGs in `images/flows/` (the gallery PDFs already contain them).

Adding sources: open the notebook, choose **Add source** (or the plus sign in the Sources panel), choose **Upload file**, and select the files. Repeat until all are listed.

## 3. Video prompts

NotebookLM makes one video per generation. Because there is no length limit, make a **series**: one video per part, each with the same base prompt plus one focus line. Paste the base prompt into the video customise box, then add one focus line.

### Base prompt (paste first)

```text
You are producing an explainer video for beginners about one product, Unravis, and its Investigation Timeline screen. Audience: support engineers, implementation consultants and junior developers who do not know IIS, P95, Query Store or HTTP.sys. Explain what a thing means first, then give its technical name.

SOURCES: use only the uploaded sources. Do not invent numbers, features, menus or claims. If something is not in the sources, leave it out. Prefer the exact wording of button names and labels from FLOWS-GUIDE.md. Use the screenshot galleries as the visuals: show the image the narration refers to and point at the part being explained.

LENGTH: as long as needed to cover the focus fully. Do not rush or skip steps. Use short chapters with titles.

TONE: calm, plain, short sentences, no hype. Define every technical word in one line the first time it appears.

MUST SAY, near the start and near the end: this is a prototype showing made-up demo data.
MUST SAY when relevant: the prototype's P95 is an estimate; some data used by the prototype's suspects is not yet read by the shipped importers (SQL calls over one second, blocked-process reports, per-file disk latency, Mongo usedDisk and connections.rejected, Mongo top).
NEVER SAY: "tracing", "root cause", "proves", "guaranteed", "AI", "machine learning". Say "suspects" and "evidence". Grey means "not sure", never "fine".
```

### Focus lines (add one per video)

| Video | Focus line to add under the base prompt |
|---|---|
| 1. The big picture | `FOCUS: why the Investigation Timeline exists and a first tour of the whole screen. Use NOTEBOOKLM-SOURCE.md section 4, scenes 1 to 10.` |
| 2. Getting started and cases | `FOCUS: every flow from FLOWS-GUIDE.md about starting the app: launch and the master key (locked, wrong key, damaged, first run), the start screen's three cards, opening an existing case (own key, closed, archived, restore), entering a case by code, and restoring from a case file.` |
| 3. Creating a new case | `FOCUS: creating a new case step by step from FLOWS-GUIDE.md: the Name step, the Sources step in all four ways (This computer, Import files by folder or file, MongoDB, SQL Server including Azure SQL Database), environments, portal and API, the permission scripts, the Review step, the progress screen, a failed source and Open the case anyway, then adding files later.` |
| 4. The case home and analysis | `FOCUS: the case home blocks and the existing analysis screens, from FLOWS-GUIDE.md: Analysis, Investigation Timeline, Databases, Story so far, Theories, Health checks, Files, Case key, archive and reopen; then the analysis pulse, Where is it, nearby activity, record detail and Export.` |
| 5. What each data source tells you | `FOCUS: WHAT-EACH-DATA-SOURCE-BRINGS.md. For every source say what it is, what it tells you, what it cannot tell you, what must be switched on, and which suspect or screen part it feeds. Start with the at-a-glance table.` |
| 6. How every number is calculated | `FOCUS: HOW-THINGS-ARE-CALCULATED.md. Explain P50, P95 and P99 with the 20-number example, then every other figure, threshold and rule, each with its worked example. Say clearly where the prototype differs from the real app.` |
| 7. Reading the Investigation Timeline | `FOCUS: reading the screen: investigation window and comparison period, bars and the bucket ladder, the seven figure cards, the three charts, gaps versus real zeros, the 7-day view, the folded evidence card, the layouts A, B and C, hand-off to Run analysis.` |
| 8. Suspects and the honest limits | `FOCUS: the Suspects panel: red, orange, green, grey; suspects not causes; the top five and See all; Show on chart; the What-if switches; the no-history situation; then the honest limits (P95 estimate, importers that do not read some data yet, 25 of 42 suspects shown, unconfirmed decisions).` |

## 4. Written outputs from the same notebook (chat prompts)

Paste any of these into NotebookLM's chat to get text you can review before making videos.

```text
Write a beginner glossary of every technical term in the sources (P50, P95, P99, 4xx, 5xx, IIS, HTTP.sys, Query Store, extended events, blocked-process report, deadlock, bucket, gap, comparison period, suspect, tier, onset). One plain sentence each, then the technical meaning, then where it appears on screen. Cite the source file.
```

```text
Explain, as if to someone who has never used the product, how to create a new case from the start screen to the finished case home, listing every field, button and message by its exact name from FLOWS-GUIDE.md. Include what goes wrong and what the screen says.
```

```text
For every number on the Investigation Timeline figure cards, explain in plain English how it is calculated, with a small worked example, and say whether the prototype and the real app calculate it the same way. Cite HOW-THINGS-ARE-CALCULATED.md.
```

```text
Make a table of every data source: what it tells you, what it cannot tell you, what must be switched on or granted, and which suspects it feeds. Cite WHAT-EACH-DATA-SOURCE-BRINGS.md.
```

```text
List every statement in the sources that says something is only a prototype choice, an estimate, not yet read by the real importers, or not yet confirmed by the product owner.
```

## 5. Checklist for each finished video

- [ ] It says "prototype" and "made-up data" at the start and at the end.
- [ ] It never says "tracing", "root cause", "proves" or "AI".
- [ ] It says "suspects", explains red, orange, green and grey, and says grey is not a pass.
- [ ] Button and field names match FLOWS-GUIDE.md exactly.
- [ ] Numbers match the sources (for example 4,216 requests; slow-request time 522 ms, 2.4 times the comparison period; 210 web server errors; 2,005 slow database calls, 40%; 15 blocking reports).
- [ ] P95 is described as "estimated" in the prototype, and the nearest-rank method with 20 requests minimum is described for the real app.
- [ ] It says missing data is a gap, not zero; and that logs that exist but contain no events are a real zero.
- [ ] It says the comparison period may not have been healthy.
- [ ] It does not claim the product finds causes, fixes anything, or collects data it does not collect yet.
- [ ] Any screen, label or number not in the sources is flagged and regenerated.
