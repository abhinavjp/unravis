#!/usr/bin/env node
'use strict';
// Pass/fail check for guided-analysis-layout.html. Built for use after every
// later wireframe packet. Run from buildout/wireframes: node check-guided.js [--slice 1|2|3|4]
// Prints only FAIL lines and a final RESULT line. Exit 0 on pass, 1 on fail.

var path = require('path');
var execFileSync = require('child_process').execFileSync;
var pathToFileURL = require('url').pathToFileURL;
var chromium = require(path.join(__dirname, '../../tools/screens/node_modules/playwright')).chromium;

var here = __dirname;
var builtFile = path.join(here, 'guided-analysis-layout.html');
var builtUrl = pathToFileURL(builtFile).href;

// ---------- options ----------
var slice = 1;
for (var i = 2; i < process.argv.length; i++) {
  if (process.argv[i] === '--slice' && process.argv[i + 1]) slice = parseInt(process.argv[i + 1], 10);
  else if (/^--slice=/.test(process.argv[i])) slice = parseInt(process.argv[i].split('=')[1], 10);
}
if (!(slice >= 1 && slice <= 4)) slice = 1;

var VIEWPORTS = [
  { label: '1440x900', width: 1440, height: 900 },
  { label: '1280x800', width: 1280, height: 800 },
  { label: '1024x768', width: 1024, height: 768 }
];
var THEMES = ['light', 'dark'];
var VARIANTS = slice === 1 ? ['A'] : ['A', 'B', 'C'];
var STEPS = [];
for (var sN = 0; sN <= 13; sN++) STEPS.push(sN);

var MAX_LINES = 40;

// ---------- build ----------
function build() {
  try {
    execFileSync(process.execPath, [path.join(here, 'build-guided.js')], { cwd: here, stdio: 'pipe' });
    return null;
  } catch (e) {
    var code = (e && typeof e.status === 'number') ? e.status : '?';
    var msg = '';
    try { msg = String((e.stderr || '').toString()).trim().split('\n').pop(); } catch (x) {}
    return 'build-guided.js exited ' + code + (msg ? ' (' + msg + ')' : '');
  }
}

// ---------- in-page assertions ----------
function pageChecks() {
  var out = [];
  function clip(t) { return String(t).replace(/\s+/g, ' ').slice(0, 60); }

  // 2. squashed buttons inside .pane .row
  var rows = document.querySelectorAll('.pane .row');
  for (var r = 0; r < rows.length; r++) {
    var btns = rows[r].querySelectorAll('button');
    for (var b = 0; b < btns.length; b++) {
      var el = btns[b];
      if (el.getClientRects().length === 0) continue; // not rendered
      var box = el.getBoundingClientRect();
      var txt = (el.textContent || '').trim();
      if (txt.length > 6 && box.width < 60) {
        out.push({ n: 2, d: 'button "' + clip(txt) + '" width ' + Math.round(box.width) + 'px < 60' });
      }
      if (box.height > 64) {
        out.push({ n: 2, d: 'button "' + clip(txt) + '" height ' + Math.round(box.height) + 'px > 64' });
      }
    }
  }

  // 3. chart-draw check
  var svgs = document.querySelectorAll('.pane svg');
  for (var k = 0; k < svgs.length; k++) {
    var paths = svgs[k].querySelectorAll('path');
    var lines = [];
    for (var p = 0; p < paths.length; p++) {
      var cls = paths[p].getAttribute('class') || '';
      if (cls.indexOf('line') !== -1) lines.push(paths[p]);
    }
    if (lines.length === 0) continue;
    if (lines.length > 4 || lines.length < 1) {
      out.push({ n: 3, d: 'svg has ' + lines.length + ' line paths (want 1..4)' });
    }
    // a gap in the data legitimately leaves a short run, so only the widest line must be long
    var widest = 0;
    for (var q = 0; q < lines.length; q++) {
      widest = Math.max(widest, lines[q].getBoundingClientRect().width);
    }
    if (!(widest > 100)) {
      out.push({ n: 3, d: 'widest line path is ' + Math.round(widest) + 'px <= 100' });
    }
  }

  // 4. no horizontal page scroll
  if (document.documentElement.scrollWidth > window.innerWidth + 1) {
    out.push({ n: 4, d: 'scrollWidth ' + document.documentElement.scrollWidth + ' > innerWidth ' + window.innerWidth });
  }
  return out;
}

// navigate to one page and return its failures: {n, d}[]
async function auditPage(page, url, errs) {
  errs.length = 0;
  await page.goto(url, { waitUntil: 'load' });
  await page.waitForTimeout(60);
  var dom = await page.evaluate(pageChecks);
  var out = [];
  for (var e = 0; e < errs.length; e++) out.push({ n: 1, d: errs[e] });
  for (var d = 0; d < dom.length; d++) out.push(dom[d]);
  return out;
}

// ---------- runner ----------
async function run() {
  var failures = [];
  var checked = 0;

  function record(vp, theme, variant, label, list) {
    for (var n = 0; n < list.length; n++) {
      failures.push('FAIL ' + vp + ' ' + theme + ' ' + variant + ' ' + label + ' ' + list[n].n + ': ' + list[n].d);
    }
  }

  var buildErr = build();
  if (buildErr) {
    failures.push('FAIL - - - - 0: ' + buildErr);
    return finish(failures, checked);
  }

  var browser = await chromium.launch({ headless: true });
  try {
    for (var vi = 0; vi < VIEWPORTS.length; vi++) {
      var vp = VIEWPORTS[vi];
      var context = await browser.newContext({ viewport: { width: vp.width, height: vp.height } });
      var page = await context.newPage();
      var errs = [];
      page.on('pageerror', function (e) { errs.push('pageerror: ' + (e && e.message ? e.message : String(e))); });
      page.on('console', function (m) { if (m.type() === 'error') errs.push('console: ' + m.text()); });

      var toolIds = [];
      for (var ti = 0; ti < THEMES.length; ti++) {
        var theme = THEMES[ti];
        await page.emulateMedia({ colorScheme: theme });

        for (var xi = 0; xi < VARIANTS.length; xi++) {
          var variant = VARIANTS[xi];

          for (var si = 0; si < STEPS.length; si++) {
            var stepN = STEPS[si];
            var url = builtUrl + '?variant=' + variant + '&mode=guided&step=' + stepN;
            var list = await auditPage(page, url, errs);
            if (!toolIds.length) {
              toolIds = await page.evaluate(function () {
                return (window.TOOLS || []).map(function (t) { return t.id; });
              });
            }
            checked++;
            record(vp.label, theme, variant, 'step ' + stepN, list);
          }

          var targets = [{ label: 'manual home', q: '' }];
          for (var t = 0; t < toolIds.length; t++) {
            targets.push({ label: 'tool ' + toolIds[t], q: '&tool=' + encodeURIComponent(toolIds[t]) });
          }
          for (var mi = 0; mi < targets.length; mi++) {
            var murl = builtUrl + '?variant=' + variant + '&mode=manual' + targets[mi].q;
            var mlist = await auditPage(page, murl, errs);
            checked++;
            record(vp.label, theme, variant, targets[mi].label, mlist);
          }
        }
      }
      await context.close();
    }
  } finally {
    await browser.close();
  }

  return finish(failures, checked);
}

function finish(failures, checked) {
  var shown = failures.slice(0, MAX_LINES);
  for (var i = 0; i < shown.length; i++) console.log(shown[i]);
  if (failures.length > MAX_LINES) console.log('... and ' + (failures.length - MAX_LINES) + ' more');
  if (failures.length) {
    console.log('RESULT FAIL (' + failures.length + ' failures)');
    process.exitCode = 1;
  } else {
    console.log('RESULT PASS (' + checked + ' pages checked)');
    process.exitCode = 0;
  }
}

run().catch(function (e) {
  console.log('FAIL - - - - 0: ' + String(e && e.message ? e.message : e).replace(/\s+/g, ' ').slice(0, 120));
  console.log('RESULT FAIL (1 failures)');
  process.exitCode = 1;
});
