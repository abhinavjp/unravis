# Prompts for the NotebookLM explainer video

## What NotebookLM can and cannot do

- It cannot open a GitHub repository, run the HTML prototype or browse pages that need a login. It reads sources you add to a notebook: uploaded files (Markdown, text, PDF, images), pasted text, public web pages and Google Docs. [Likely; check the current NotebookLM help if the menus differ]
- So the repository content is delivered as a **source pack**: `NOTEBOOKLM-SOURCE.md` plus ten images, all in this folder.
- The video is generated from the sources and the instructions below. You do not control exact visuals or timing, so check the result against the checklist at the end.

## Steps

1. Download from the repo (`abhinavjp/unravis`, branch `prototype/tracing-paper-e2e`) the folder `prototype/video/`.
2. In NotebookLM create a notebook called "Investigation Timeline".
3. Add as sources: `NOTEBOOKLM-SOURCE.md`, then all ten PNG files in `images/`. Optional for extra detail: `prototype/TRACING-SCREEN-EXPLAINER.md`.
4. Open the Studio panel, choose **Video Overview**, open its customise option, and paste one of the prompts below.
5. Generate, watch it, and run the checklist. If something is wrong, adjust the prompt (add the correction as a sentence) and generate again.

## Short prompt (use this if the box is small)

```text
Make a 6-minute explainer video for beginners (support engineers, junior developers) about the Investigation Timeline screen in Unravis. Follow the scene order in NOTEBOOKLM-SOURCE.md section 4 (scenes 1 to 10) and use the ten images in order. Explain meaning first, jargon second, in plain short sentences and a calm tone. Use only facts and numbers from the sources. Say near the start and the end that it is a prototype with made-up data. Never say "tracing", "root cause", "proves" or "AI". Say "suspects", not causes.
```

## Full prompt (use as the customise text, or paste into chat and add as a note)

```text
You are producing a 6-minute explainer video for beginners about one screen of a product called Unravis: the "Investigation Timeline".

AUDIENCE: support engineers, implementation consultants and junior developers. They do not know IIS, P95, Query Store or HTTP.sys. Explain what a thing means first, then give its technical name.

SOURCES: Use only NOTEBOOKLM-SOURCE.md and the ten images. Do not invent numbers, features or claims. If a detail is not in the sources, leave it out.

STRUCTURE (follow section 4 of NOTEBOOKLM-SOURCE.md, one scene each, in order):
1 The problem. 2 Choose the period (investigation window and comparison period). 3 It runs by itself. 4 What Unravis sees (three plain tiles). 5 The seven figure cards. 6 Reading the three charts on one time axis. 7 Gaps: seven days, missing data shown as missing. 8 Suspects: red, orange, green, grey. 9 Where does the evidence point? 10 Close.
Show the matching image for each scene (01 to 10 in the image index) and point at the part being explained.

KEY POINTS TO MAKE CLEARLY:
- Three sources on one time axis: the application's request log, the web server (IIS) and the database.
- The comparison period is only a comparison; it may not have been healthy.
- Missing data is shown as a labelled gap, never as zero. Logs that exist but contain no events are a real zero, not a gap.
- Suspects, not causes. Red = most likely (its part of the system clearly moved first). Orange = likely. Green = ruled out by the data we have. Grey = not sure, could not be tested; grey is never a pass. All rules are fixed and written down. The same data always gives the same answer.
- The panel shows the top five by default, with "See all".
- State the demo numbers exactly as written in the sources (for example 4,216 requests, slow-request time 522 ms which is 2.4 times the comparison period, 210 web server errors, 2,005 slow database calls, 15 blocking reports).

TONE: calm, plain, short sentences, no hype, no jargon without a one-line explanation.

MUST SAY: near the start and near the end, that this is a prototype showing made-up demo data.
NEVER SAY: "tracing", "root cause", "proves", "guaranteed", "AI", "machine learning". Say "suspects" and "evidence" instead.
END with one sentence: the Investigation Timeline reads all three sources on one time axis, shows what is missing, and lists suspects with the evidence for and against.
```

## Checklist for the finished video

- [ ] It says "prototype" and "made-up data" at the start and at the end.
- [ ] It never says "tracing", "root cause", "proves" or "AI".
- [ ] It uses the term "suspects" and explains red, orange, green and grey, including "grey is not a pass".
- [ ] The numbers match the sources (4,216 requests; 522 ms, 2.4 times; 210 web server errors; 2,005 slow database calls, 40%; 15 blocking reports).
- [ ] It says missing data is a gap, not zero.
- [ ] It says the comparison period may not have been healthy.
- [ ] It does not claim the product finds causes or fixes anything.
- [ ] Any visual that shows other numbers or labels not in the sources is flagged and regenerated.
