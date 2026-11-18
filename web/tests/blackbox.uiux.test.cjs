const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// UX-16, 17, 18, 23, 24, 27, 28: the Apply diff, the Remove screen, Step 9 default windows, sticker contrast,
// the shared Blackbox stylesheet block and live regions that do not double up.

const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8').replace(/\r\n/g, '\n');
const lib = loadTypeScript('src/lib/blackbox.ts');
const copy = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { BB_TEXT: T } = copy;
const comps = ['Add', 'CaseUse', 'HostCard', 'Panel', 'ReadKey', 'Remove', 'Settings', 'TargetPicker', 'PlaceWizard'];
const src = Object.fromEntries(comps.map((c) => [c, read('src/components/Blackbox' + c + '.svelte')]));

const settings = () => lib.defaultSettings();

test('UX-16: the diff lists only changed settings, old to new, and ignores the locked ones', () => {
  const a = settings();
  const b = JSON.parse(JSON.stringify(a));
  assert.deepEqual(lib.settingsChanges(a, b), []);
  b.intervalSeconds = a.intervalSeconds === 30 ? 60 : 30;
  b.folder = 'E:\\New';
  b.port = a.port + 1;
  b.bind = '0.0.0.0';
  b.name = 'other';
  const fields = lib.settingsChanges(a, b).map((c) => c.field);
  assert.deepEqual(fields, ['interval', 'folder']);
  const parts = lib.settingsChanges(a, b).map(copy.applyChangePart);
  assert.match(parts[0], /^Interval \d+ to \d+ seconds$/);
  assert.match(parts[1], /^Folder .* to E:\\New$/);
  assert.match(T.applyChangedLine(parts), /^You changed: Interval/);
});

test('UX-16: Apply shows the diff and the folder warning before the button, and locks port and address', () => {
  const panel = src.Panel;
  assert.ok(panel.indexOf('bb-apply-diff') < panel.indexOf('onclick={() => apply(current)}'));
  assert.match(panel, /lockReached/);
  assert.match(src.Settings, /disabled=\{lockReached\}/);
  assert.match(T.applyFolderWarn, /Old recordings stay in the old folder/);
  assert.match(T.applyLocked, /cannot be changed here/);
  const ports = src.Settings.match(/<input id="\{idPrefix\}-f-(port|bind)"[^>]*>/g);
  assert.equal(ports.length, 2);
  for (const tag of ports) assert.match(tag, /disabled=\{lockReached\}/);
});

test('UX-17/18: Remove focuses the name box, labels the button, hints a mismatch and merges the lines', () => {
  const r = src.Remove;
  assert.match(r, /nameInput\?\.focus\(\)/);
  assert.match(r, /onchange=\{chooseDelete\}/);
  assert.match(r, /deleteRecordings \? T\.removeButtonDelete : T\.removeButton/);
  assert.match(r, /aria-describedby=\{mismatch \? 'bb-remove-mismatch' : undefined\}/);
  assert.match(r, /id="bb-remove-mismatch"/);
  assert.match(r, /T\.removeFailedHint/);
  assert.doesNotMatch(r, /removeCasesNote|removeThisPc|T\.adminNote/);
  assert.equal(T.removeNameMismatch, 'The name does not match yet.');
  assert.equal(T.removeButtonDelete, 'Remove and delete recordings');
  assert.match(T.removeFailedHint, /Scan this PC/);
  assert.match(T.removeStopsLive, /Recording ends now/);
  assert.doesNotMatch(T.removeStops, /Recording ends now/);
  assert.doesNotMatch(r, /<legend class="bb-sr">\{T\.removeTitle/);
});

test('UX-23: default Step 9 windows stay inside first..last sample', () => {
  const M = 60000;
  const t = (s) => Date.parse(s);
  const long = lib.defaultHostWindows('2026-11-10T00:00:00Z', '2026-11-17T09:05:30Z');
  assert.equal(long.incEnd, t('2026-11-17T09:05:00Z'));
  assert.equal(long.incStart, long.incEnd - 30 * M);
  assert.equal(long.refStart, long.incEnd - 60 * M);
  assert.equal(long.refEnd, long.incStart);
  const short = lib.defaultHostWindows('2026-11-17T08:50:00Z', '2026-11-17T09:05:00Z');
  assert.equal(short.incStart, t('2026-11-17T08:50:00Z'));
  assert.equal(short.refStart, short.refEnd, 'no earlier reference');
  const mid = lib.defaultHostWindows('2026-11-17T08:20:00Z', '2026-11-17T09:05:00Z');
  assert.equal(mid.refStart, t('2026-11-17T08:20:00Z'));
  assert.equal(mid.refEnd, t('2026-11-17T08:35:00Z'));
  assert.equal(lib.defaultHostWindows(undefined, undefined), null);
  assert.match(T.hostNoEarlier('X'), /no earlier time to compare with/);
  assert.match(src.HostCard, /defaultHostWindows\(/);
  assert.match(src.HostCard, /T\.hostNoEarlier\(/);
  assert.ok(copy.blackboxCopyEntries().some((e) => e.id === 'blackbox.hostNoEarlier'));
});

test('UX-24: sticker text clears 4.5:1 on its 10% tint in both themes', () => {
  const css = [read('src/brand/tokens.css'), read('src/app.css')].join('\n');
  const body = (opener) => {
    const i = css.indexOf(opener);
    assert.ok(i >= 0, opener);
    let depth = 0;
    for (let j = css.indexOf('{', i); j < css.length; j++) {
      if (css[j] === '{') depth++;
      if (css[j] === '}' && --depth === 0) return css.slice(css.indexOf('{', i), j);
    }
    throw new Error('open block');
  };
  const tok = (text) => Object.fromEntries([...text.matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)].map(([, n, v]) => [n, v.toLowerCase()]));
  const sets = { dark: tok(body(':root {')), light: tok(body(':root[data-theme="light"] {')) };
  const ch = (c) => { c /= 255; return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
  const rgb = (h) => { const n = parseInt(h.slice(1), 16); return [n >> 16, (n >> 8) & 255, n & 255]; };
  const lum = (rgbv) => 0.2126 * ch(rgbv[0]) + 0.7152 * ch(rgbv[1]) + 0.0722 * ch(rgbv[2]);
  const ratio = (a, b) => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
  for (const [theme, set] of Object.entries(sets)) {
    for (const token of ['--tone-ok', '--warn', '--bad']) {
      assert.ok(set[token], theme + ' ' + token);
      const tint = rgb(set['--surface']).map((v, i) => v * 0.9 + rgb(set[token])[i] * 0.1);
      const value = ratio(rgb(set[token]), tint);
      assert.ok(value >= 4.5, theme + ' ' + token + ' on its tint is ' + value.toFixed(2));
    }
  }
});

test('UX-27: the shared .bb rules live in app.css once, and no Blackbox component uses !important', () => {
  const css = read('src/app.css');
  for (const rule of ['.bb-note', '.bb-ok', '.bb-sr', '.bb-actions']) {
    assert.equal(css.split('\n').filter((l) => l.startsWith(rule + ' {')).length, 1, rule);
    for (const c of comps) assert.equal(src[c].split('\n').filter((l) => l.trim().startsWith(rule + ' {')).length, 0, c + ' ' + rule);
  }
  for (const c of comps) assert.doesNotMatch(src[c], /!important/, c);
});

test('UX-28: no aria-live wrapper around children that carry a role', () => {
  for (const c of comps) {
    for (const m of src[c].matchAll(/<div[^>]*aria-live="polite"[^>]*>([\s\S]*?)\n\s*<\/div>/g)) {
      assert.doesNotMatch(m[1], /role="(status|alert)"/, c);
    }
  }
  assert.doesNotMatch(src.Panel + src.Remove + src.Add + src.CaseUse + src.HostCard + src.ReadKey + src.PlaceWizard, /<div[^>]*aria-live=/);
});
