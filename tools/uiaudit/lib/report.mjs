// Turns the data files of a run into report/REPORT.md. Pure functions, no browser: tests/report.test.mjs covers them.
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

export const VARIANTS = ['desktop-dark', 'desktop-light', 'phone-dark', 'phone-light'];

export const CHECK_HELP = {
  'page-overflow-x': 'The page scrolls sideways.',
  'element-past-edge': 'An element sticks out past the right edge of the window.',
  'text-clipped': 'Text is cut off with nothing to show it was cut.',
  'text-truncated': 'Text is shortened with "..." (by design, listed so you can judge it).',
  'text-spill': 'Text runs out of its own button, chip or badge.',
  overlap: 'Two controls or headings sit on top of each other.',
  'small-target': 'A button or link is smaller than 24 x 24 px (or under 44 px on a phone).',
  'low-contrast': 'Text is hard to read against its background (WCAG AA: 4.5:1, big text 3:1).',
  'small-text': 'Text is smaller than 11 px.',
  'misaligned-row': 'Controls in one row do not share a line (off by a few pixels).',
  'misaligned-column': 'A block in a stack sits a few pixels left or right of its neighbours.',
  'broken-image': 'A picture did not load.',
  'bad-text': 'A code word (undefined, NaN, [object Object]) shows instead of a value.',
  'no-name': 'A button or link has no name for a screen reader.',
  'no-label': 'A form field has no label.',
  'duplicate-id': 'Two elements share one id.',
  'page-title': 'The page has no title.',
  'page-lang': 'The page does not say its language.',
  'heading-jump': 'A heading skips a level.',
  'theme-mismatch': 'A big surface is the wrong brightness for the theme.',
  'theme-not-applied': 'The run asked for one theme and the page showed the other.',
  'console-error': 'The browser console printed an error.',
  'page-error': 'A script error was thrown on the page.',
  'request-failed': 'A request did not complete.',
  'http-error': 'The app got an error status from its server.'
};

export function loadResults(dataDir) {
  return readdirSync(dataDir)
    .filter((f) => f.endsWith('.json'))
    .map((f) => JSON.parse(readFileSync(join(dataDir, f), 'utf8')))
    .sort((a, b) => a.id.localeCompare(b.id) || VARIANTS.indexOf(a.variant) - VARIANTS.indexOf(b.variant));
}

/** The key a finding is known by. Same on dark and light, so a baseline entry does not need two lines. */
export function matchKey(result, f) {
  return [result.id, result.size, f.check, f.label].join('|');
}

function glob(pattern, value) {
  if (pattern === undefined || pattern === '*') return true;
  const re = new RegExp('^' + String(pattern).split('*').map((p) => p.replace(/[.+?^${}()|[\]\\]/g, '\\$&')).join('.*') + '$');
  return re.test(value);
}

/** One baseline entry: { screen, size, check, label, why, item } where each of the first four may use * and may be left out. */
export function findKnown(baseline, result, f) {
  for (const e of baseline.entries ?? []) {
    if (glob(e.screen, result.id) && glob(e.size, result.size) && glob(e.check, f.check) && glob(e.label, f.label)) return e;
  }
  return null;
}

export function classify(results, baseline) {
  for (const r of results) {
    if (r.status !== 'checked') {
      const e = (baseline.entries ?? []).find((x) => (x.check === undefined || x.check === '*' || x.check === 'unreachable') && glob(x.screen, r.id) && glob(x.size, r.size));
      r.known = e ?? null;
      continue;
    }
    for (const f of r.findings) f.known = findKnown(baseline, r, f);
  }
  return results;
}

export function tally(results) {
  const t = { screens: new Set(), checked: 0, unreachable: 0, absent: 0, skipped: 0, newError: 0, newWarn: 0, knownError: 0, knownWarn: 0, info: 0, unreachableNew: 0 };
  for (const r of results) {
    t.screens.add(r.id);
    if (r.status === 'unreachable') { t.unreachable++; if (!r.known) t.unreachableNew++; continue; }
    if (r.status === 'skipped') { t.skipped++; continue; }
    if (r.status === 'absent') { t.absent++; continue; }
    t.checked++;
    for (const f of r.findings) {
      if (f.severity === 'info') t.info++;
      else if (f.severity === 'error') f.known ? t.knownError++ : t.newError++;
      else f.known ? t.knownWarn++ : t.newWarn++;
    }
  }
  t.screens = t.screens.size;
  return t;
}

/** Exit rule: any new error, or a screen that cannot be reached and is not on the baseline, fails the run. */
export function verdict(t) {
  return t.newError === 0 && t.unreachableNew === 0 ? 'PASS' : 'FAIL';
}

const esc = (s) => String(s).replace(/\|/g, '\\|').replace(/\n/g, ' ');
const icon = { error: 'ERROR', warn: 'warn', info: 'info' };

function cell(r) {
  if (!r) return '-';
  if (r.status === 'unreachable') return 'not reached';
  if (r.status === 'skipped') return 'skipped';
  if (r.status === 'absent') return 'not on this build';
  const e = r.findings.filter((f) => f.severity === 'error' && !f.known).length;
  const w = r.findings.filter((f) => f.severity === 'warn' && !f.known).length;
  const k = r.findings.filter((f) => f.severity !== 'info' && f.known).length;
  const parts = [];
  if (e) parts.push(e + ' E');
  if (w) parts.push(w + ' W');
  if (k) parts.push(k + ' known');
  return parts.length ? parts.join(', ') : 'clean';
}

export function renderReport({ results, baseline, run }) {
  const t = tally(results);
  const v = verdict(t);
  const ids = [...new Set(results.map((r) => r.id))];
  const by = (id, variant) => results.find((r) => r.id === id && r.variant === variant);
  const L = [];
  L.push('# Unravis UI audit report', '');
  L.push('Run: ' + run.when + ' | Build: ' + run.build + ' | Demo clock: ' + run.demoNow + ' | Browser: ' + run.browser, '');
  L.push('Looks checked: desktop 1440 px and phone 390 px wide, each in dark and light theme. Data: the built-in demo (`--demo`), never real sources.', '');
  L.push('## Verdict: ' + v, '');
  L.push('- Screens: ' + t.screens + '. Screen looks checked: ' + t.checked + '. Not reached: ' + t.unreachable + ' (' + t.unreachableNew + ' new). Skipped: ' + t.skipped + '. Not on this build: ' + t.absent + '.');
  L.push('- New errors: **' + t.newError + '**. New warnings: ' + t.newWarn + '. Known errors: ' + t.knownError + '. Known warnings: ' + t.knownWarn + '. Notes (info): ' + t.info + '.');
  L.push('- PASS means no new errors and no newly unreachable screen. "Known" means listed in `tools/uiaudit/baseline.json` with a reason or backlog item.', '');

  L.push('## How to read this', '');
  L.push('- **ERROR** is something a person would see as broken: sideways scroll, cut-off text, overlapping controls, unreadable text, console errors.');
  L.push('- **warn** is a likely flaw to judge: a tap target under 24 px, text under 11 px, controls a few pixels off line.');
  L.push('- **info** is a note (for example text shortened with "..."). Only counted here; the full list is in `data/*.json`.');
  L.push('- Each screenshot is the whole screen (the window is stretched to the content). Red boxes are errors, orange boxes are warnings; the number is the row number in the table above it.');
  L.push('- Screens come from `tools/screens/screens.json` (the user-guide screens) plus `tools/uiaudit/extra-screens.json`.', '');

  L.push('## Problems by kind', '');
  const kinds = {};
  for (const r of results) for (const f of r.findings ?? []) {
    const k = (kinds[f.check] ??= { error: 0, warn: 0, info: 0, new: 0 });
    k[f.severity]++;
    if (!f.known && f.severity !== 'info') k.new++;
  }
  L.push('| Check | What it means | Errors | Warnings | Notes | New |', '| --- | --- | ---: | ---: | ---: | ---: |');
  for (const [c, k] of Object.entries(kinds).sort((a, b) => b[1].error - a[1].error || b[1].warn - a[1].warn)) {
    L.push('| ' + c + ' | ' + esc(CHECK_HELP[c] ?? '') + ' | ' + k.error + ' | ' + k.warn + ' | ' + k.info + ' | ' + k.new + ' |');
  }
  L.push('');

  // Problems with a picture: every new problem, and every error even when it is known (a known error should still be seen).
  const shown = (f) => f.severity !== 'info' && (!f.known || f.severity === 'error');
  const withShown = ids.filter((id) => results.some((r) => r.id === id && r.status === 'checked' && r.findings.some(shown)));
  L.push('## Problems on screen', '');
  L.push('New problems, and all errors (marked with their backlog item when they are known). Known warnings are counted in the next section.', '');
  if (!withShown.length) L.push('None.', '');
  for (const id of withShown) {
    const rs = results.filter((r) => r.id === id);
    L.push('### ' + id, '');
    if (rs[0].note) L.push(rs[0].note, '');
    for (const r of rs.filter((x) => x.status === 'checked' && x.findings.some(shown))) {
      L.push('#### ' + id + ' - ' + r.variant, '');
      L.push('| # | Level | Check | Where | What | Known |', '| ---: | --- | --- | --- | --- | --- |');
      r.findings.forEach((f, i) => {
        if (!shown(f)) return;
        L.push('| ' + (i + 1) + ' | ' + icon[f.severity] + ' | ' + f.check + ' | ' + esc(f.label || f.selector || '-') + ' | ' + esc(f.message) + ' | ' + (f.known ? esc(f.known.item || 'yes') : 'NEW') + ' |');
      });
      L.push('', '![' + id + ' ' + r.variant + '](' + r.shot + ')', '');
    }
  }

  // Known problems, grouped
  L.push('## Known problems (on the baseline)', '');
  const groups = new Map();
  for (const r of results) for (const f of r.findings ?? []) {
    if (!f.known || f.severity === 'info') continue;
    const key = f.known.why + '|' + f.check + '|' + (f.known.item ?? '');
    const g = groups.get(key) ?? { check: f.check, why: f.known.why, item: f.known.item ?? '', n: 0, screens: new Set() };
    g.n++;
    g.screens.add(r.id);
    groups.set(key, g);
  }
  if (!groups.size) L.push('None.', '');
  else {
    L.push('| Check | Backlog item | Why it is known | Hits | Screens (up to 8) |', '| --- | --- | --- | ---: | --- |');
    for (const g of [...groups.values()].sort((a, b) => b.n - a.n)) L.push('| ' + g.check + ' | ' + esc(g.item || '-') + ' | ' + esc(g.why) + ' | ' + g.n + ' | ' + [...g.screens].slice(0, 8).join(', ') + (g.screens.size > 8 ? ' and ' + (g.screens.size - 8) + ' more' : '') + ' |');
    L.push('');
  }

  // Not reached
  L.push('## Screens that could not be reached', '');
  const nr = results.filter((r) => r.status === 'unreachable' || r.status === 'skipped');
  if (!nr.length) L.push('None.', '');
  else {
    L.push('A screen that is not reached is not checked. This is a gap in the audit, not a pass.', '');
    L.push('| Screen | Look | Status | Reason |', '| --- | --- | --- | --- |');
    for (const r of nr) L.push('| ' + r.id + ' | ' + r.variant + ' | ' + r.status + (r.known ? ' (known: ' + esc(r.known.why) + ')' : '') + ' | ' + esc(r.reason ?? '') + ' |');
    L.push('');
  }

  // Parts of a screen this build does not have (a change that is not merged yet)
  const ab = results.filter((r) => r.status === 'absent');
  if (ab.length) {
    L.push('## Screens not reachable on this build', '');
    L.push('These look at a state that only newer builds have. They are not failures and not passes: they are checked as soon as a build has the state.', '');
    L.push('| Screen | Look | Reason |', '| --- | --- | --- |');
    for (const r of ab) L.push('| ' + r.id + ' | ' + r.variant + ' | ' + esc(r.reason ?? '') + ' |');
    L.push('');
  }

  // Matrix
  L.push('## All screens', '', '"clean" means no errors or warnings. Each cell links to the screenshot.', '');
  L.push('| Screen | ' + VARIANTS.join(' | ') + ' |', '| --- | ' + VARIANTS.map(() => '---').join(' | ') + ' |');
  for (const id of ids) {
    L.push('| ' + id + ' | ' + VARIANTS.map((vv) => { const r = by(id, vv); return r && r.shot ? '[' + cell(r) + '](' + r.shot + ')' : cell(r); }).join(' | ') + ' |');
  }
  L.push('');

  L.push('## What is checked, and what is not', '');
  L.push('Checked on every screen and look: sideways page scroll, elements past the window edge, cut-off or spilling text, overlapping controls and headings, tap targets, text contrast, text size, rows and stacks off line by a few pixels, broken pictures, code words on screen, missing names and labels, duplicate ids, heading jumps, wrong-brightness surfaces, console errors, failed requests and server errors.');
  L.push('', 'Not checked here: text drawn inside charts (canvas), colour behind gradients and pictures (skipped, counted in the data files), moving parts (motion is turned off), real data, and the Windows-only screens a Linux run cannot reach (listed above). Whether a screen is pleasant to use still needs a person; this report finds the mechanical faults.', '');
  L.push('Rerun: `cd tools/uiaudit && npm ci && npx playwright install chromium && node run-audit.mjs` (see README.md).', '');
  return L.join('\n');
}
