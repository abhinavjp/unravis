const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// UI sweep, package F4 (Blackbox, Unravis Lite, Releases dashboard). Each test is named with the finding it covers.
// Logic is tested through loadTypeScript; where the behaviour needs a browser (focus, layout) the Playwright spec
// tools/e2e/specs/ui-sweep-f4.spec.ts covers it, and the source assertions here only pin the wiring.

const root = path.resolve(__dirname, '..');
const repo = path.resolve(root, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8').replace(/\r\n/g, '\n');
const readRepo = (r) => fs.readFileSync(path.join(repo, r), 'utf8').replace(/\r\n/g, '\n');
const bb = loadTypeScript('src/lib/blackbox.ts');
const copy = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const charts = loadTypeScript('src/lib/copy/blackboxChartsCopy.ts');
const series = loadTypeScript('src/lib/blackboxSeries.ts');
const wiz = loadTypeScript('src/lib/copy/blackboxWizardCopy.ts');
const { BB_TEXT: T } = copy;

const refusal = (reason, error, what) => new bb.BlackboxError({ status: 422, error, reason, what });

test('SWEEP-F4-01: a refused setting names the field, the step that owns it, and says nothing was saved', () => {
  const folder = refusal('invalid-config', 'blackbox.json is not valid: "folder" must be a full path such as D:\\UnravisBlackbox');
  assert.equal(bb.refusedField(folder), 'folder');
  assert.equal(bb.stepOfSetting('folder'), 'folder');
  const text = copy.refusedSetting('folder');
  assert.match(text, /^The server did not accept this\. Type a full folder path/);
  assert.match(text, /Nothing was saved\.$/);
  assert.doesNotMatch(text, /nothing was changed/i);
  // The other fields land on the step that shows them.
  const of = (field) => bb.refusedField(refusal('invalid-config', 'blackbox.json is not valid: "' + field + '" must be 5, 15, 30 or 60'));
  assert.equal(of('intervalSeconds'), 'interval');
  assert.equal(bb.stepOfSetting('interval'), 'record');
  assert.equal(of('keepDays'), 'keep');
  assert.equal(bb.stepOfSetting('keep'), 'keep');
  assert.equal(of('port'), 'port');
  assert.equal(bb.stepOfSetting('port'), 'folder');
  assert.equal(of('name'), 'name');
  assert.equal(bb.stepOfSetting('name'), 'name');
  // Anything else keeps the general sentence, which also says nothing was saved.
  assert.equal(bb.refusedField(refusal('invalid-config', 'something else')), null);
  assert.equal(bb.refusedField(refusal('rate', '"folder" is fine')), null);
  assert.equal(bb.refusedField(new Error('x')), null);
  assert.match(copy.BB_REFUSAL['invalid-config'], /Nothing was saved/);
});

test('SWEEP-F4-01: the Review refusal sits above the cards, is an alert, and the button points at it', () => {
  const w = read('src/components/BlackboxPlaceWizard.svelte');
  const problemAt = w.indexOf('<div class="problem-box"');
  const reviewAt = w.indexOf('<StepReview ');
  assert.ok(problemAt > 0 && reviewAt > 0 && problemAt < reviewAt, 'the problem is drawn before the Review cards');
  assert.match(w, /<div class="problem-box" id=\{reviewProblemId\} role="alert">/);
  assert.match(w, /aria-describedby=\{primaryDescribedBy\}/);
  assert.match(w, /primaryDisabled && reviewProblem && wizard\.step === 'review' \? reviewProblemId/);
  assert.match(w, /W\['review\.fixAt'\]\(W\[\('rail\.' \+ reviewFix\)/);
  assert.equal(wiz.BBW_TEXT['review.fixAt']('Folder'), 'Change folder');
});

test('SWEEP-F4-02: a button turned off for a moment keeps focus inside the pop-up and gets it back', () => {
  const d = read('src/components/blackboxWizard/WizardDialog.svelte');
  assert.match(d, /new MutationObserver/);
  assert.match(d, /attributeFilter: \['disabled'\]/);
  assert.match(d, /shell\.focus\(\{ preventScroll: true \}\)/);
  assert.match(d, /back\.focus\(\{ preventScroll: true \}\)/);
  assert.match(d, /bind:this=\{shell\} tabindex="-1"/);
  // Show and Hide are two buttons, so focus follows the swap.
  const k = read('src/components/BlackboxReadKey.svelte');
  assert.match(k, /void followFocus\(\(\) => hideBtn\)/);
  assert.match(k, /void followFocus\(\(\) => showBtn\)/);
});

test('SWEEP-F4-03: the pop-up keeps one chevron for a disclosure row and the row is not grey caption text', () => {
  const d = read('src/components/blackboxWizard/WizardDialog.svelte');
  assert.match(d, /:global\(dialog\.pop\.pop details > summary\) \{[^}]*color: var\(--text\)/);
  // The drawer's own chevron stays off in here, so the one in app.css is the only one.
  assert.match(d, /:global\(dialog\.pop\.pop details > summary::after\) \{ content: none; display: none; \}/);
  assert.match(read('src/components/blackboxWizard/StepFolder.svelte'), /\.advanced summary \{[^}]*color: var\(--text\)/);
  assert.match(read('src/components/BlackboxHostCard.svelte'), /\.bb-windows summary \{[^}]*font-weight: 500/);
});

test('SWEEP-F4-04: a colour is never used twice inside one chart; a crowded chart goes to dashes', () => {
  const c = read('src/components/BlackboxCharts.svelte');
  assert.match(c, /for \(const s of strips\) s\.keys\.forEach\(\(k, j\) => out\.set\(k, \{ color: p\.series\[j % n\]/);
  assert.match(c, /dash: j < n \? 'solid' : j < 2 \* n \? 'dashed' : 'dotted'/);
  assert.match(c, /type: c\?\.dash \?\? 'solid'/);
  // The swatches in the list, the table and the legend all draw the dash.
  assert.equal((c.match(/class="bbc-swatch \{[^}]*dash/g) ?? []).length, 3);
  assert.match(c, /\.bbc-swatch\.dashed \{ border-top-style: dashed; \}/);
  // A legend row above each chart: four names and "+N more".
  assert.match(c, /shown: names\.slice\(0, 4\)/);
  assert.match(c, /T\.legendMore\(lg\.more\)/);
  assert.equal(charts.BBC_TEXT.legendMore(3), '+3 more');
  // The tooltip rows start with a coloured line mark.
  assert.match(c, /dash === 'solid' \? '━━'/);
  assert.match(c, /position: \(pt: number\[\]\) => \[pt\[0\] \+ 16, 8\]/);
});

test('SWEEP-F4-05: the chart has a text name and can be zoomed with buttons', () => {
  const c = read('src/components/BlackboxCharts.svelte');
  assert.match(c, /class="bbc-canvas-wrap" role="img" aria-label=\{T\.chartLabel\(/);
  for (const how of ['earlier', 'later', 'in', 'out']) assert.match(c, new RegExp("moveZoom\\('" + how + "'\\)"));
  assert.match(c, /chart\.dispatchAction\(\{ type: 'dataZoom', startValue/);
  const label = charts.BBC_TEXT.chartLabel('web-a', 'Processor busy, Memory in use', 2);
  assert.equal(label, 'Line chart for web-a. Lines: Processor busy, Memory in use and 2 more. The table under the chart gives the numbers.');
  assert.doesNotMatch(charts.BBC_TEXT.chartLabel('web-a', 'A'), / and \d+ more/);
  assert.match(charts.BBC_TEXT.zoomHelp, /zoom buttons/);
});

test('SWEEP-F4-06: in a small window the whole charts pop-up scrolls as one and the list goes under the chart', () => {
  const c = read('src/components/BlackboxCharts.svelte');
  assert.match(c, /@media \(max-width: 899px\), \(max-height: 799px\) \{\s*\.bbc \{ display: block; overflow: auto; \}/);
  assert.match(c, /\.bbc-side \{ order: 2;/);
  assert.doesNotMatch(c, /max-height: 40vh/, 'no second small scrolling box for the list');
});

test('SWEEP-F4-07: status words inside sentences are quoted, and the sentences read as sentences', () => {
  const all = Object.values(T).filter((v) => typeof v === 'string').concat(Object.values(T.stateNext), Object.values(copy.BB_REFUSAL));
  for (const text of all) {
    assert.doesNotMatch(text, /(?<!")Cannot test yet(?!")/, 'unquoted status word in: ' + text);
    if (text !== T.changeKey) assert.doesNotMatch(text, /(?<!")Type a new read key(?!")/, 'unquoted button name in: ' + text);
  }
  assert.equal(T.adminNote, 'Windows asks for administrator approval for service changes. If you choose No in that prompt, nothing changes.');
  assert.equal(T.gapsText(1, 0, 300), '1 gap of 5 minutes. In a case, those minutes say "Cannot test yet"');
  assert.match(T.stateNext.incomplete, /makes the minutes inside it "Cannot test yet"\.$/);
  assert.match(copy.stateNextFor({ state: 'unreachable', reason: 'key' }, false), /Press "Type a new read key"/);
});

test('SWEEP-F4-08: the Releases empty state lists the three causes and one next step', () => {
  const js = readRepo('cmd/unravis-releases/ui/app.js');
  assert.match(js, /function emptyState\(s\)/);
  for (const name of ['GitHub list', 'Update feed', 'Release log']) assert.ok(js.includes("row('" + name + "'"), name);
  assert.match(js, /ul\.hidden = !rels\.length; \$\('nolist'\)\.hidden = !!rels\.length; \$\('refresh'\)\.classList\.toggle\('primary', !rels\.length\)/);
  const detail = readRepo('cmd/unravis-releases/ui/detail.js');
  assert.match(detail, /shown\.length \? el\('div', 'empty', 'Select a release\.'\)/);
  assert.match(readRepo('cmd/unravis-releases/ui/index.html'), /<div id="nolist" class="nolist" hidden><\/div>/);
});

test('SWEEP-F4-09: the status line is the biggest, the advice reads as advice, one footnote, one disk-low sentence', () => {
  const p = read('src/components/BlackboxPanel.svelte');
  assert.match(p, /\.bb-block p\.bb-ready \{ font-size: 16px; font-weight: 600;/);
  assert.match(p, /\.bb-block p\.bb-next \{ color: var\(--text\); font-size: 13px;[^}]*border-left: 3px solid var\(--accent\)/);
  assert.match(p, /<p class="bb-note bb-foot">\{footnote\(item\)\}<\/p>/);
  // The advice for a low disk says what to do, not what the sentence above it already said.
  const warn = copy.stateNextFor({ state: 'disk-low', tone: 'warn' }, true);
  const bad = copy.stateNextFor({ state: 'disk-low', tone: 'bad' }, true);
  assert.equal(warn, 'Free space on that drive, or lower the size limit, before it pauses.');
  assert.equal(bad, 'Free space on that drive. Recording resumes by itself.');
  assert.equal(T.hostResultTitle('Partly ready'), 'Host reading: partly ready');
});

test('SWEEP-F4-10: advice follows the kind of address and the state; no Start button is promised to another server', () => {
  assert.match(copy.stateNextFor({ state: 'unreachable' }, false, 'url'), /port is open/);
  assert.doesNotMatch(copy.stateNextFor({ state: 'unreachable' }, false, 'url'), /share/);
  assert.match(copy.stateNextFor({ state: 'unreachable' }, false, 'share'), /share name/);
  assert.equal(copy.stateNextFor({ state: 'unreachable' }, false, 'folder'), T.stateNext.unreachable);
  assert.equal(copy.stateNextFor({ state: 'unreachable' }, false), T.stateNext.unreachable);
  assert.match(copy.stateNextFor({ state: 'stopped' }, true), /Press Start/);
  assert.doesNotMatch(copy.stateNextFor({ state: 'stopped' }, false), /Press Start/);
  assert.match(copy.stateNextFor({ state: 'stopped' }, false), /Services/);
  const p = read('src/components/BlackboxPanel.svelte');
  assert.match(p, /item\.state\.state !== 'not-found' && item\.state\.state !== 'not-placed'\}<p class="bb-note">\{T\.anotherServer\}/);
  assert.match(p, /item\.state\.state !== 'not-found' && item\.state\.state !== 'not-placed' && item\.state\.state !== 'unreachable'\}\{#key item\.id\}<Diagnostics/);
  assert.equal(T.failHeadline, 'Cannot reach this Blackbox');
});

test('SWEEP-F4-11: the Place wizard keeps one height, and leaves at once when nothing was typed', () => {
  const d = read('src/components/blackboxWizard/WizardDialog.svelte');
  assert.match(d, /dialog\.pop\.fixed \.shell \{ height: min\(700px/);
  const w = read('src/components/BlackboxPlaceWizard.svelte');
  assert.match(w, /\n\s+fixedHeight\n/);
  assert.match(w, /finished=\{finished \|\| pristine\}/);
  assert.match(w, /wizard\.step === 'name' && wizard\.where === null && wizard\.maxSeen === 0/);
});

test('SWEEP-F4-12: always-recorded figures show a mark and say so, not a greyed checkbox', () => {
  const f = read('src/components/blackboxWizard/FigureRow.svelte');
  assert.doesNotMatch(f, /type="checkbox"/);
  assert.match(f, /class="mark"/);
  assert.match(f, /W\['figure\.recorded'\]/);
  assert.doesNotMatch(f, /opacity: \.75/);
  const r = read('src/components/blackboxWizard/StepRecord.svelte');
  assert.doesNotMatch(r, /checked disabled/);
  assert.match(r, /class="locked"/);
  assert.equal(wiz.BBW_TEXT['sql.allRecorded'](12), 'All 12 figures are recorded. There is nothing to choose here.');
  assert.match(read('src/components/blackboxWizard/StepSql.svelte'), /BBW_TEXT\['sql\.allRecorded'\]\(total\)/);
});

test('SWEEP-F4-13: a scan that finds only what is listed says so', () => {
  assert.equal(T.scanNothingNew('web-a'), 'web-a on this PC is already in the list. Nothing new was found.');
  const p = read('src/components/BlackboxPanel.svelte');
  assert.match(p, /found\.installed && found\.listed\) scanNote = T\.scanNothingNew\(/);
});

test('SWEEP-F4-14: Apply settings and the wizard use the same words for the same options', () => {
  assert.doesNotMatch(T.recordProcesses, /top 20/);
  assert.match(T.recordProcesses, /top 10 by processor and top 5 by memory/);
  assert.match(T.recordLogs, /^IIS log catalogue/);
  assert.match(T.recordCopyLogs, /^Log copies in a snapshot/);
  assert.equal(T.applyChangedLabels.logs, 'IIS log catalogue');
  assert.equal(T.applyChangedLabels.copyLogs, 'Log copies in a snapshot');
});

test('SWEEP-F4-15: a byte axis gets a round top in binary units', () => {
  const MB = 1024 ** 2, GB = 1024 ** 3;
  assert.equal(series.niceBinaryMax(5.59 * GB), 8 * GB);
  assert.equal(series.niceBinaryMax(1.1 * GB), 2 * GB);
  assert.equal(series.niceBinaryMax(920 * MB), 1 * GB);
  assert.equal(series.niceBinaryMax(3 * MB), 4 * MB);
  assert.equal(series.niceBinaryMax(1), 1);
  assert.equal(series.niceBinaryMax(0), null);
  assert.equal(series.niceBinaryMax(NaN), null);
  // Four steps of the top are round numbers in the same unit: 2 GB gives 512 MB, 1 GB, 1.5 GB, 2 GB.
  const top = series.niceBinaryMax(1.7 * GB);
  assert.deepEqual([1, 2, 3, 4].map((i) => series.formatValue('bytes', top / 4 * i)), ['512 MB', '1 GB', '1.5 GB', '2 GB']);
  assert.equal(series.biggestPlotted([{ points: [[1, 5, 1, 9], [2, 7, 1, 9]] }, { points: [[1, 3, 1, 3]] }]), 7);
  assert.equal(series.biggestPlotted([{ points: [] }]), null);
});

test('SWEEP-F4-16: number columns are right-aligned and the compared bars can be seen', () => {
  const c = read('src/components/BlackboxCharts.svelte');
  assert.match(c, /\.bbc-list-table \.num \{ text-align: right; font-variant-numeric: tabular-nums;/);
  assert.equal((c.match(/class="num"/g) ?? []).length, 10);
  assert.match(c, /\.bbc-bar-track \{[^}]*background: var\(--surface-2\); border: 1px solid var\(--control-line\)/);
  assert.match(c, /\.bbc-bar-mark \{[^}]*width: 6px/);
});

test('SWEEP-F4-17: the scale has a visible name and plain words; the log axis shows minor lines', () => {
  assert.equal(charts.BBC_TEXT.linear, 'Normal');
  assert.equal(charts.BBC_TEXT.log, 'Log (x10 steps)');
  assert.match(charts.BBC_TEXT.logHelp, /ten times the one below it/);
  assert.doesNotMatch(JSON.stringify(charts.BBC_TEXT.logHidden(2)), /Times-10|Even steps/);
  const c = read('src/components/BlackboxCharts.svelte');
  assert.match(c, /<span class="bbc-seg-label" aria-hidden="true" title=\{T\.scaleHint\}>\{T\.scale\}<\/span>/);
  assert.match(c, /minorTick: \{ show: true, splitNumber: 9 \}, minorSplitLine/);
});

test('SWEEP-F4-18: an empty chart says what to do and offers the default set', () => {
  const c = read('src/components/BlackboxCharts.svelte');
  assert.match(c, /<div class="bbc-empty">[\s\S]*T\.pickSomeHelp[\s\S]*T\.useDefault/);
  assert.match(c, /\.bbc-empty \{ display: grid; justify-items: center;/);
  assert.equal(charts.BBC_TEXT.useDefault, 'Use the default set');
});

test('SWEEP-F4-19: the snapshot result offers the path to copy and has one Done, not two closes', () => {
  const p = read('src/components/BlackboxPanel.svelte');
  assert.match(p, /onclick=\{\(\) => copyPath\(view\.kind === 'snapshot' \? view\.answer\.dir : ''\)\}>\{T\.snapshotCopyPath\}/);
  assert.match(p, /\{#if view\.kind === 'status'\}<button type="button" class="secondary-button"[^>]*onclick=\{\(\) => pop\?\.requestLeave\(\)\}>\{T\.popClose\}/);
  assert.match(read('src/components/blackboxWizard/WizardDialog.svelte'), /\.shell-foot:empty \{ display: none; \}/);
});

test('SWEEP-F4-20: Step 9 has one zone, one heading and a result named for the reading', () => {
  const h = read('src/components/BlackboxHostCard.svelte');
  assert.doesNotMatch(h, /\(\{timeZone\.short\}\)<\/legend>/);
  assert.match(h, /<summary>\{T\.hostRead\}<\/summary>/);
  assert.match(h, /grid-template-columns: minmax\(0, 1fr\); gap: var\(--space-3\)/);
  assert.equal(T.hostRead, 'Step 9: choose the windows to read');
});

test('SWEEP-F4-21: the panel intro is two short sentences', () => {
  assert.equal(T.readOnly, 'Unravis reads your servers. It never changes them. On this PC it can start, stop, change or remove its own Blackbox, and Windows asks you first.');
  assert.ok(T.readOnly.length < 170);
});

test('SWEEP-F4-22: Unravis Lite shows when it read, keeps focus on Refresh, and reuses the sticker', () => {
  const l = read('lite/App.svelte');
  assert.match(l, /readAt = formatTime\(Date\.now\(\), \{ zone: timeZone\.zone, precision: 'second', date: false \}\)/);
  assert.match(l, /<span class="read-at" role="status">/);
  assert.match(l, /if \(item\) \{ if \(refreshing\) return; refreshing = true; \} else loading = true;/);
  assert.match(l, /<Sticker info=\{item\.state\} \/>/);
  assert.match(l, /Blackbox on <span class="mono wrap">\{item\.location\}<\/span>/);
  assert.match(l, /T\.gapsText\(item\.facts\.gaps/);
  assert.match(l, /\.note \{[^}]*margin-top: 16px/);
});

test('SWEEP-F4-23: Releases uses one word per state, shows the reason once, gives a next step and an h1', () => {
  const css = readRepo('cmd/unravis-releases/ui/app.css');
  const js = readRepo('cmd/unravis-releases/ui/app.js');
  const detail = readRepo('cmd/unravis-releases/ui/detail.js');
  const html = readRepo('cmd/unravis-releases/ui/index.html');
  assert.match(html, /<h1 class="brand">Unravis Releases<\/h1>/);
  assert.match(js, /if \(r\.withdrawn\) return \['Withdrawn', 'warn'\]/);
  assert.match(js, /return \['Stopped at ' \+ broke\.name, 'bad'\]/);
  assert.match(detail, /f\.stage === 'Verify' \? 'Verify failed' : 'Stopped at ' \+ f\.stage/);
  assert.match(detail, /if \(s\.reason && !\(j\.failure && j\.failure\.stage === s\.name\)\)/);
  assert.match(detail, /run Release\.ps1 -Channel/);
  assert.match(js, /function runLink\(text, version\)/);
  assert.match(detail, /\/\^could not\/i\.test\(why\)/);
  // Eight font sizes became four, and the active tab is no wider than the others.
  const sizes = new Set([...css.matchAll(/font-size:\s*([\d.]+)px/g)].map((m) => m[1]));
  assert.ok(sizes.size <= 5, 'font sizes used: ' + [...sizes].join(', '));
  assert.match(css, /\.tab\{[^}]*font-weight:500\}/);
  assert.doesNotMatch(css, /\.tab\[aria-selected=true\]\{[^}]*font-weight/);
  assert.doesNotMatch(detail, /el\('h1'/);
});

test('SWEEP-F4-24: the chart help words work for a finger, and the list of counters folds on a phone', () => {
  assert.doesNotMatch(charts.BBC_TEXT.intro2, /on the left/);
  assert.doesNotMatch(charts.BBC_TEXT.pickSomeHelp, /on the left/);
  assert.match(charts.BBC_TEXT.intro3, /touch/);
  const c = read('src/components/BlackboxCharts.svelte');
  assert.match(c, /window\.matchMedia\('\(max-width: 639px\)'\)/);
  assert.match(c, /class:folded=\{narrow && !sideOpen\}/);
  assert.equal(charts.BBC_TEXT.sideToggle(4), 'Counters (4)');
});

test('SWEEP-F4-25: Diagnostics reads its words from the copy file, hides Copy when there is nothing to copy', () => {
  const d = read('src/components/BlackboxDiagnostics.svelte');
  assert.match(d, /\{#if answer && !empty\}<button[^>]*>\{T\.diagCopy\}<\/button>\{\/if\}/);
  assert.doesNotMatch(d, /0\.4rem|ui-monospace/);
  assert.doesNotMatch(d, />Diagnostics<|>Read the timing log<|>Copy diagnostics</);
  assert.equal(T.diagRead, 'Read the timing log');
  assert.equal(T.diagCopy, 'Copy diagnostics');
});

test('SWEEP-F4-27: the Use in a case success keeps the single line', () => {
  // The second line says what happens next, so the first is not shown beside it right after pressing the button.
  const c = read('src/components/BlackboxCaseUse.svelte');
  assert.match(c, /\{:else if use\?\.state === 'this'\}\s*\{#if !note\}<p>\{T\.caseUseOn\}<\/p>\{\/if\}/);
});

test('SWEEP-F4-28: the rail does not call SQL skipped before the Where answer is known', () => {
  const w = read('src/components/BlackboxPlaceWizard.svelte');
  assert.match(w, /skipped: wizard\.where !== null && !list\.includes\(id\)/);
});

test('SWEEP-F4-29 / BL-495j: ResourcePanel is mounted on the case page (the sweep found it mounted nowhere), and only there', () => {
  const walk = (dir) => fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => e.isDirectory() ? walk(path.join(dir, e.name)) : [path.join(dir, e.name)]);
  const files = walk(path.join(root, 'src')).filter((f) => /\.(svelte|ts)$/.test(f) && !f.endsWith('ResourcePanel.svelte') && !f.includes(path.sep + 'resource' + path.sep));
  const importers = files.filter((f) => /ResourcePanel/.test(fs.readFileSync(f, 'utf8')));
  assert.deepEqual(importers.map((f) => path.relative(root, f).split(path.sep).join('/')), ['src/lib/components/CaseHome.svelte']);
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /lazyComponent\(\(\) => import\('\.\.\/\.\.\/components\/ResourcePanel\.svelte'\)\)/, 'its code and the chart library are read when the case page is on screen');
  // The real data path: the case, the audit window on screen, the zone, the time the kept files cover, the API P95 of the analysis on screen.
  assert.match(home, /<ResourcePanel \{caseId\} query=\{scope\.query\} zone=\{timeZone\.zone\} span=\{filesSpan\} \{apiWindows\} onGone=\{gone\} \/>/);
  assert.match(home, /const filesSpan = \$derived\(caseSpan\(imports\)\)/);
  assert.match(home, /\{#key caseId \+ ':' \+ keptFiles\.length\}<ResourcePanel/, 'read again when the kept files change');
  // It is drawn only when the case holds counter data, and takes no room otherwise.
  assert.match(home, /\.resource-slot:not\(:has\(> :global\(\.tools\)\)\) \{ display: none; \}/);
  const panel = read('src/components/ResourcePanel.svelte');
  assert.match(panel, /const shown = \$derived\(hosts\.length > 0 \|\| notice != null \|\| period != null \|\| \(resource == null && \(busy \|\| problem !== ''\)\)\);/);
});

test('BL-495j: the resource panel says what it is once (the plain line) and then how to open it', () => {
  const resCopy = loadTypeScript('src/lib/resource.ts');
  const helpCopy = loadTypeScript('src/lib/copy/helpCopy.ts');
  assert.doesNotMatch(resCopy.resourceCopy.intro, /What each host measured/, 'the sentence of the plain help line is not said again');
  assert.match(helpCopy.help.resource.plain, /What each host measured/);
  const panel = read('src/components/ResourcePanel.svelte');
  assert.ok(panel.indexOf('<HelpNote id="resource" />') < panel.indexOf('{resourceCopy.intro}'));
});

// S2 (review 1): run the real load() text from lite/App.svelte with the page state as plain variables.
function liteLoad(opts) {
  const src = read('lite/App.svelte');
  const body = src.slice(src.indexOf('async function load()'), src.indexOf('onMount('));
  const factory = new Function('deps', `
    let { item, problem, loading, refreshing, readAt } = deps.state;
    const { hasSessionToken, NO_SESSION_MESSAGE, blackboxApi, formatTime, timeZone, T } = deps;
    ${body}
    return { load, get: () => ({ item, problem, loading, refreshing, readAt }) };
  `);
  return factory({
    state: { item: opts.item ?? null, problem: '', loading: !opts.item, refreshing: false, readAt: '' },
    hasSessionToken: () => opts.token !== false,
    NO_SESSION_MESSAGE: 'Open Unravis from the link.',
    blackboxApi: { list: opts.list ?? (async () => ({ items: [{ name: 'a' }] })) },
    formatTime: () => '10:00:00',
    timeZone: { zone: 'UTC' },
    T: {}
  });
}

test('SWEEP-F4-30: a Lite refresh without a session token does not leave Refresh stuck, and keeps the page', async () => {
  const h = liteLoad({ item: { name: 'a' }, token: false });
  await h.load();
  let s = h.get();
  assert.equal(s.refreshing, false);
  assert.match(s.problem, /Unravis Lite/);
  assert.ok(s.item, 'the page stays, so the Refresh button the person pressed is still there and keeps focus');
  // Try again works: a second call is not swallowed by the refreshing guard.
  let called = 0;
  const h2 = liteLoad({ item: { name: 'a' }, list: async () => { called++; return { items: [{ name: 'b' }] }; } });
  await h2.load();
  await h2.load();
  assert.equal(called, 2);
  assert.equal(h2.get().refreshing, false);
  assert.equal(h2.get().problem, '');
});

test('SWEEP-F4-30: a failed Lite refresh resets the busy flag and shows the problem above the buttons', async () => {
  const h = liteLoad({ item: { name: 'a' }, list: async () => { throw new Error('Could not read it.'); } });
  await h.load();
  const s = h.get();
  assert.equal(s.refreshing, false);
  assert.equal(s.problem, 'Could not read it.');
  assert.ok(s.item);
  const l = read('lite/App.svelte');
  assert.match(l, /\{:else if problem && !item\}/);
  assert.match(l, /\{#if problem\}<p class="problem" role="alert">\{problem\}<\/p>\{\/if\}/);
  assert.match(l, /\} finally \{\s*loading = false;\s*refreshing = false;/);
});

test('SWEEP-F4-31: a click or a Tab away from a parked button is never undone when the button comes back on (N6)', () => {
  const src = read('src/components/blackboxWizard/WizardDialog.svelte');
  // Tab to another control: focusin on it clears the parked button.
  assert.match(src, /if \(t instanceof HTMLElement && t !== shell\) \{ lastFocus = t; if \(t !== parked\) parked = null; \}/);
  // Pointer choice clears it too, and the listener is removed with the observer.
  assert.match(src, /const chose = \(\) => \{ parked = null; \};/);
  assert.match(src, /addEventListener\('pointerdown', chose\)/);
  assert.match(src, /removeEventListener\('pointerdown', chose\)/);
  // The pull-back needs focus to still be on the frame (or lost to the page).
  assert.match(src, /\(document\.activeElement === shell \|\| gone\(\)\)/);
});

test('SWEEP-F4-32: the wizard body is the containing block of its visually hidden text, so the pop-up reports no hidden overflow', () => {
  const src = read('src/components/blackboxWizard/WizardDialog.svelte');
  assert.match(src, /\.shell-body \{ position: relative;[^}]*overflow: auto/);
  assert.match(read('src/components/blackboxWizard/FigureRow.svelte'), /\.visually-hidden \{ position: absolute;/);
});

test('SWEEP-F4-33: the checkbox in a counter row does not shrink beside a long name, so every name starts at the same place', () => {
  assert.match(read('src/components/BlackboxCharts.svelte'), /\.bbc-pick input \{ flex: 0 0 auto; \}/);
});
