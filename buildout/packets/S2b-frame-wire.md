# Packet S2b: wire the Step 0 helpers into `parts/30-steps-0-4.js`

Do exactly this. Do not explore the repo. Read ONLY `buildout/wireframes/parts/30-steps-0-4.js` lines 1 to 90. The helpers (`frameBanner`, `frameDst`, `frameEstimateLine`, `frameReleaseHint`, `frameReleaseLine`, `frameKeepTouching`) are being created in another file; do not define them. Output SEARCH/REPLACE blocks only, in the format below, copying each SEARCH text VERBATIM from the file (each must occur exactly once). Make these 6 edits, nothing else.

1. In `frameProblems()` delete the line that pushes `'The two windows overlap. Pick a normal period that does not share any time with the incident.'` (the whole `if (out.length === 0 && p(f.incidentStart) < p(f.referenceEnd) ...` line). Overlap is now a non-blocking banner.
2. In `quickPick(name)` replace the final `  render();` of that function (the one directly before `function frameForm(){`) with two lines: `  frameKeepTouching();` then `  render();`. Use the last `if (name === 'yesterday')` line plus the `render();` line plus `}` plus `function frameForm(){` as the SEARCH so it is unique.
3. In `frameForm()` change the change-row string so that the row's closing `Remove</button></div>';` becomes `Remove</button></div>' + frameReleaseLine(r);` (the `.map(function(r, i){ return ...; })` still returns one string).
4. In the `Changes you know about (optional)` fieldset line, insert `+ frameReleaseHint()` between the `<legend>...</legend>` string and the `<p class="note">Releases or changes with a time in UTC...` string. Concretely: the string `'<fieldset class="wf"><legend>Changes you know about (optional)</legend><p class="note">Releases or changes with a time in UTC. Step 5 sets them beside the onset.</p>'` becomes `'<fieldset class="wf"><legend>Changes you know about (optional)</legend>' + frameReleaseHint() + '<p class="note">Releases or changes with a time in UTC. Step 5 sets them beside the onset.</p>'`.
5. After the line `+ '<p class="note">The normal period to compare with. It must not share time with the incident.</p></fieldset>'` append a new line `+ frameBanner() + frameDst()` (so the banner and the daylight-saving notice sit under the reference window).
6. Before the final line `+ '<p class="note">These windows are kept in one place. Changing them marks any step already run as out of date.</p>';` insert a line `+ frameEstimateLine()`.

Also in `window.stepNotes0`, directly after the `dt('Out of date', 'The form always edits the live windows; editing them is what marks steps 1 to 10 out of date.')` line, insert a new line:
`    + dt('Guidance', 'Under the windows: a banner when the reference window overlaps, touches or is much longer than the incident (Keep anyway dismisses it), a daylight-saving notice, and an estimate line of rows and time. None of them blocks Run. A change inside the incident window gets its own line. All of it is computed in <code>30-frame-helpers.js</code>.')`.

## Block format (one block per edit, edits in file order)
```
### parts/30-steps-0-4.js
<<<<<<< SEARCH
old text (verbatim, as short as possible but unique)
=======
new text
>>>>>>> REPLACE
```

## Check (run from `buildout/wireframes` AFTER you imagine the blocks applied; the owner applies them, you only verify anchors)
Verify each SEARCH occurs exactly once with: `grep -c -F "<first line of SEARCH>" parts/30-steps-0-4.js` (must print 1 each time).

## Output
The blocks in one plain text reply (no code fence around them), then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <each anchor count>`, `Concerns: none` (or one line).
