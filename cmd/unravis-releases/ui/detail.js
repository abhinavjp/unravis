'use strict';
// The release page: steps with their logs, tests, CI checks, files and changes. Everything is read from the program's own
// /api/ routes (read only). Text is always put on the page with textContent, never as HTML.

const TABS = [['steps', 'Steps'], ['tests', 'Tests'], ['checks', 'CI checks'], ['files', 'Files'], ['changes', 'Changes'], ['logs', 'All logs']];
const STEP_HELP = {
  Preflight: 'Checks this PC and the code are ready',
  Tag: 'Marks this exact version of the code',
  Build: 'Waits for the build and tests on GitHub',
  Sign: 'Signs the files so installed apps trust them',
  Publish: 'Puts the files on the release page',
  Feed: 'Tells installed apps a new version exists',
  Verify: 'Installs it on a clean machine to prove it works',
};
const KIND_WORD = { ci: 'Tests run by CI on the release commit', release: 'Tests run by the release build', verify: 'Clean-machine install check' };
const LOG_WORD = { run: 'What the release script printed', ci: 'CI run (build and tests)', release: 'Release build run', verify: 'Clean-machine check run' };
const UNIT_WORD = { tests: 'tests', packages: 'packages', checks: 'checks' };

const ui = { tab: 'steps', data: {}, sig: {}, open: new Set(), busy: {}, ver: '' };
try { ui.tab = sessionStorage.getItem('rd.tab') || 'steps'; } catch (e) { /* the choice just is not kept */ }
if (!TABS.some((t) => t[0] === ui.tab)) ui.tab = 'steps';

function dur(s) {
  if (s == null || s < 0) return '';
  if (s < 1) return 'under 1 s';
  if (s < 60) return s + ' s';
  const m = Math.floor(s / 60);
  if (m < 60) return m + ' min ' + String(s % 60).padStart(2, '0') + ' s';
  return Math.floor(m / 60) + ' h ' + String(m % 60).padStart(2, '0') + ' min';
}
function clock(s) { const d = new Date(s); return isNaN(d) ? '' : d.toLocaleTimeString([], { hour12: false }); }
function bytes(n) { if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB'; if (n >= 1024) return Math.round(n / 1024) + ' KB'; return n + ' B'; }
function count(n) { return Number(n).toLocaleString(); }
function chip(kind, text) { return el('span', 'tag ' + kind, text); }

async function getJSON(url) {
  const r = await fetch(url, { cache: 'no-store' });
  let b = null; try { b = await r.json(); } catch (e) { /* not JSON */ }
  if (!r.ok || (b && b.error)) throw new Error((b && b.error) || 'The program answered with status ' + r.status + '.');
  return b;
}

async function copyText(text, btn) {
  const was = btn.textContent;
  try { await navigator.clipboard.writeText(text); btn.textContent = 'Copied'; }
  catch (e) { btn.textContent = 'Could not copy'; }
  setTimeout(() => { btn.textContent = was; }, 1500);
}

// ---------- log viewer ----------
// One viewer per log (and step range). The viewer keeps its own lines, search and scroll, so the page can rebuild around it.
const viewers = new Map();
const PAGE = 5000, WINDOW = 1500;
function viewer(file, o) {
  const key = file + '|' + (o.from || 0);
  let v = viewers.get(key);
  if (!v) {
    if (viewers.size > 14) { for (const [k, x] of viewers) { if (!x.root.isConnected) { viewers.delete(k); break; } } }
    v = makeViewer(file, o.from || 0); viewers.set(key, v);
  }
  v.configure(o);
  return v;
}

const BAD = /\b(error|failed|failure|fatal|panic|stopped at|exception|denied|refused)\b/i;
const WARN = /\b(warn|warning|retry|retrying|timed out|timeout)\b/i;
const OKLINE = /(\bok \d+\b|--- PASS|# Subtest:|\bPASS\b|\bpassed\b|\b0 (failed|errors?|failures?)\b)/; // a passing test may still say "failure" in its name
const isBad = (s) => BAD.test(s) && !OKLINE.test(s);

function makeViewer(file, from) {
  const v = { file, from, to: null, live: false, mark: 0, lines: [], total: 0, cut: false, q: '', cap: WINDOW, timer: 0, busy: false, err: '' };
  const root = el('div', 'viewer');
  const bar = el('div', 'vbar');
  const q = el('input'); q.type = 'search'; q.placeholder = 'Search this log'; q.setAttribute('aria-label', 'Search this log');
  const info = el('span', 'vinfo'); info.setAttribute('aria-live', 'polite');
  const copy = el('button', '', 'Copy'); copy.type = 'button'; copy.title = 'Copy the lines shown to the clipboard';
  const end = el('button', '', 'Jump to end'); end.type = 'button';
  bar.append(q, info, copy, end);
  const body = el('div', 'vbody'); body.tabIndex = 0; body.setAttribute('role', 'log'); body.setAttribute('aria-label', 'Log text');
  const more = el('div', 'vmore');
  root.append(bar, body, more);
  v.root = root;

  const slice = () => {
    const hi = v.to == null ? v.lines.length : Math.min(v.to, v.lines.length);
    const out = [];
    const needle = v.q.toLowerCase();
    for (let i = v.from; i < hi; i++) { if (!needle || v.lines[i].x.toLowerCase().includes(needle)) out.push(i); }
    return { idx: out, hi };
  };
  const lineText = (i) => (v.lines[i].t ? clock(v.lines[i].t) + '  ' : '') + v.lines[i].x;

  v.render = () => {
    const atEnd = body.scrollTop + body.clientHeight >= body.scrollHeight - 24;
    const { idx, hi } = slice();
    body.replaceChildren();
    const tail = v.live && !v.q; // a live log shows the newest lines; a finished one starts at the top
    let shownIdx = idx;
    if (idx.length > v.cap) shownIdx = tail ? idx.slice(idx.length - v.cap) : idx.slice(0, v.cap);
    if (v.mark > 0 && !shownIdx.includes(v.mark - 1) && idx.includes(v.mark - 1)) { // the failing line is always shown
      const at = idx.indexOf(v.mark - 1);
      shownIdx = idx.slice(Math.max(0, at - 40), at + v.cap - 40);
    }
    const frag = document.createDocumentFragment();
    let markEl = null;
    for (const i of shownIdx) {
      const L = v.lines[i];
      const row = el('div', 'ln' + (i === v.mark - 1 ? ' mark' : isBad(L.x) ? ' bad' : WARN.test(L.x) ? ' warn' : stepHdr(L.x) ? ' hd' : ''));
      if (L.t) row.append(el('span', 't', clock(L.t)));
      row.append(el('span', 'x', L.x));
      if (i === v.mark - 1) markEl = row;
      frag.append(row);
    }
    if (!shownIdx.length) frag.append(el('div', 'vnone', v.err || (v.busy ? 'Reading the log...' : v.q ? 'No line has that text.' : 'This log is empty so far.')));
    body.append(frag);
    const n = Math.max(0, hi - v.from);
    info.textContent = v.q ? count(idx.length) + ' of ' + count(n) + ' lines match' : count(n) + ' lines';
    more.replaceChildren();
    if (idx.length > shownIdx.length) {
      const b = el('button', '', 'Show ' + count(Math.min(WINDOW, idx.length - shownIdx.length)) + ' more lines (' + count(idx.length - shownIdx.length) + ' hidden)');
      b.type = 'button'; b.addEventListener('click', () => { v.cap += WINDOW; v.render(); }); more.append(b);
    }
    if (v.to == null && v.lines.length < v.total) {
      const b = el('button', '', 'Load the next ' + count(Math.min(PAGE, v.total - v.lines.length)) + ' lines');
      b.type = 'button'; b.addEventListener('click', () => v.fetch(true)); more.append(b);
    }
    if (v.cut) more.append(el('span', 'vcut', 'This log is very long. Only its newest part is shown.'));
    end.hidden = !v.live;
    v.markEl = markEl;
    if (root.isConnected) {
      if (v.live && atEnd && !markEl) body.scrollTop = body.scrollHeight;
      v.applyMark();
    }
  };
  // The viewer is taken off the page and put back whenever the panel is redrawn; this puts its scroll position back.
  v.applyMark = () => {
    if (v.markEl && !v.markSeen) { v.markSeen = true; body.scrollTop = Math.max(0, v.markEl.offsetTop - body.clientHeight / 3); }
  };
  v.restore = () => {
    v.restoring = true;
    body.scrollTop = v.live && v.stick ? body.scrollHeight : v.sTop || 0;
    v.applyMark();
    requestAnimationFrame(() => { v.restoring = false; });
  };
  v.sTop = 0; v.stick = true; v.restoring = false;
  body.addEventListener('scroll', () => {
    if (v.restoring) return;
    v.sTop = body.scrollTop; v.stick = body.scrollTop + body.clientHeight >= body.scrollHeight - 24;
  });

  // Reads pages from the program until the wanted lines are in. A live log asks again every 2 seconds for the new lines only.
  v.fetch = async (extra) => {
    if (v.busy) return; v.busy = true;
    try {
      const want = v.to != null ? v.to : (extra || v.live ? Infinity : v.from + PAGE);
      let guard = 0;
      while (v.lines.length < want && guard++ < 12) {
        const j = await getJSON('/api/log?file=' + encodeURIComponent(file) + '&from=' + v.lines.length + '&n=' + PAGE);
        if (j.total < v.lines.length) { v.lines = []; v.markSeen = false; v.rev = j.rev; continue; } // the log was started again
        // A running log folds its older status blocks as new ones arrive, which rewrites lines already here: read it again from the top.
        if (v.lines.length && j.rev !== v.rev) { v.lines = []; v.markSeen = false; v.rev = j.rev; continue; }
        v.rev = j.rev;
        v.total = j.total; v.cut = !!j.cut; v.err = '';
        v.lines.push(...j.lines);
        if (!j.lines.length || v.lines.length >= j.total) break;
        if (extra) break; // one more page was asked for
      }
    } catch (e) { v.err = e.message || 'The log could not be read.'; }
    v.busy = false; v.render(); v.poll();
  };
  v.poll = () => {
    clearTimeout(v.timer);
    if (!v.live || !root.isConnected) return;
    v.timer = setTimeout(() => { if (!root.isConnected) return; if (document.hidden) { v.poll(); return; } v.fetch(false); }, 2000);
  };
  v.configure = (o) => {
    const wasLive = v.live;
    v.to = o.to != null ? o.to : null; v.live = !!o.live; v.mark = o.mark || 0;
    if (o.mark !== v.markFor) { v.markFor = o.mark; v.markSeen = false; }
    v.render();
    if (!v.lines.length || (v.live && !wasLive) || (v.to != null && v.lines.length < v.to)) v.fetch(false); else v.poll();
  };
  let deb = 0;
  q.addEventListener('input', () => { clearTimeout(deb); deb = setTimeout(() => { v.q = q.value.trim(); v.cap = WINDOW; v.render(); }, 150); });
  q.addEventListener('keydown', (e) => { if (e.key === 'Escape') { q.value = ''; v.q = ''; v.render(); } e.stopPropagation(); });
  copy.addEventListener('click', () => { const { idx } = slice(); copyText(idx.map(lineText).join('\n'), copy); });
  end.addEventListener('click', () => { body.scrollTop = body.scrollHeight; });
  return v;
}
function stepHdr(s) { return /^\[\d\/7\] /.test(s); }
function mountViewer(holder, v) { holder.replaceChildren(v.root); v.restore(); }

// ---------- page skeleton ----------
function renderDetail(r) {
  const d = $('detail');
  // No release at all: the list pane already says why, so this pane stays empty and does not ask for a choice that cannot be made.
  if (!r) { d.replaceChildren(shown.length ? el('div', 'empty', 'Select a release.') : document.createDocumentFragment()); d.dataset.v = ''; ui.ver = ''; return; }
  if (ui.ver !== r.version) { // a different release: build the frame once
    ui.ver = r.version; ui.data = {}; ui.sig = {}; ui.busy = {}; ui.open = new Set(); ui.seen = false; ui.seenT = false; ui.failed = null;
    d.replaceChildren();
    const head = el('div'); head.id = 'dhead';
    const tabs = el('div', 'tabs'); tabs.id = 'dtabs'; tabs.setAttribute('role', 'tablist'); tabs.setAttribute('aria-label', 'Release details');
    for (const [id, name] of TABS) {
      const b = el('button', 'tab', name); b.type = 'button'; b.id = 'tab-' + id; b.setAttribute('role', 'tab'); b.dataset.tab = id;
      b.setAttribute('aria-controls', 'dpanel'); tabs.append(b);
    }
    const panel = el('div', 'panel'); panel.id = 'dpanel'; panel.setAttribute('role', 'tabpanel');
    d.append(head, tabs, panel);
    tabs.addEventListener('click', (e) => { const b = e.target.closest('button[data-tab]'); if (b) setTab(b.dataset.tab); });
    tabs.addEventListener('keydown', (e) => {
      const i = TABS.findIndex((t) => t[0] === ui.tab); let n = -1;
      if (e.key === 'ArrowRight') n = (i + 1) % TABS.length; else if (e.key === 'ArrowLeft') n = (i + TABS.length - 1) % TABS.length;
      if (n >= 0) { e.preventDefault(); e.stopPropagation(); setTab(TABS[n][0]); $('tab-' + TABS[n][0]).focus(); }
    });
  }
  drawHead(r);
  const noDetail = r.version === NEXT; // the release that has no version yet has nothing to read
  $('dtabs').hidden = noDetail;
  if (noDetail) { const p = $('dpanel'); p.replaceChildren(); stepsSimple(p, r); ui.sig = {}; return; }
  markTabs();
  loadTab(false);
}

function drawHead(r) {
  const h = $('dhead'); h.replaceChildren();
  h.append(el('h2', 'dtitle', r.label || r.version));
  const [word, kind] = status(r);
  const meta = el('div', 'meta'); meta.append(r.channel + ' channel · ', chip(kind, word || 'Not published'));
  h.append(meta);
  const lv = snap && snap.live;
  if (lv && (lv.version || NEXT) === r.version) h.append(el('p', lv.gone ? 'note bad' : 'note', lv.label + (lv.gone ? '' : '. This page reads again every 5 seconds.')));
}

function markTabs() { for (const [id] of TABS) { const b = $('tab-' + id); b.setAttribute('aria-selected', String(id === ui.tab)); b.tabIndex = id === ui.tab ? 0 : -1; } }
function setTab(t) {
  ui.tab = t; try { sessionStorage.setItem('rd.tab', t); } catch (e) { /* not kept */ }
  markTabs(); const p = $('dpanel'); p.setAttribute('aria-labelledby', 'tab-' + t);
  ui.sig[ROUTE[t]] = ''; drawPanel(); loadTab(true);
}

const ROUTE = { steps: '/api/release', logs: '/api/release', tests: '/api/release/tests', checks: '/api/release/checks', files: '/api/release/artifacts', changes: '/api/release/changes' };
function isRunning(r) { const lv = snap && snap.live; return !!(lv && !lv.gone && (lv.version || NEXT) === r.version); }
function currentRelease() { return shown.find((x) => x.version === ui.ver); }

// Reads the tab's data. Steps, tests and logs come from this PC (cheap), so they are read again each time the page refreshes;
// GitHub tabs are read once and again only while the release is still being made.
async function loadTab(force) {
  const r = currentRelease(); if (!r) return;
  const tab = ui.tab, key = ROUTE[tab], v = ui.ver;
  const local = tab === 'steps' || tab === 'logs' || tab === 'tests';
  if (!force && ui.data[key] && !isRunning(r) && (!local || tab !== 'steps')) {
    const s2 = ui.sig[key + '|r'] !== JSON.stringify(r); // the release list changed under the page
    if (s2) { ui.sig[key + '|r'] = JSON.stringify(r); drawPanel(); }
    return;
  }
  if (ui.busy[key]) return;
  ui.busy[key] = true;
  if (!ui.data[key]) drawPanel(); // shows "Reading..."
  try {
    const j = await getJSON(key + '?v=' + encodeURIComponent(v));
    if (ui.ver !== v) return;
    ui.err = '';
    const rs = JSON.stringify(r);
    const sig = JSON.stringify(j) + rs;
    const changed = sig !== ui.sig[key]; ui.sig[key] = sig; ui.sig[key + '|r'] = rs; ui.data[key] = j; ui.failed = null;
    if (changed || force) drawPanel();
  } catch (e) {
    if (ui.ver !== v) return;
    ui.failed = { key, msg: e.message };
    if (!ui.data[key]) drawPanel();
  } finally { ui.busy[key] = false; }
}

function drawPanel() {
  const r = currentRelease(), p = $('dpanel'); if (!r || !p) return;
  const key = ROUTE[ui.tab], data = ui.data[key];
  p.replaceChildren();
  if (!data) {
    if (ui.failed && ui.failed.key === key) {
      // The program's own reason often starts "could not ..." already; say it once.
      const why = String(ui.failed.msg || '').trim();
      p.append(el('p', 'note bad', /^could not/i.test(why) ? why.charAt(0).toUpperCase() + why.slice(1) + (/[.!?]$/.test(why) ? '' : '.') : 'Could not read this: ' + why));
      const b = el('button', '', 'Try again'); b.type = 'button'; b.addEventListener('click', () => loadTab(true)); p.append(b);
    } else p.append(el('p', 'note', 'Reading...'));
    return;
  }
  ({ steps: panelSteps, logs: panelLogs, tests: panelTests, checks: panelChecks, files: panelFiles, changes: panelChanges })[ui.tab](p, data, r);
}

function staleNote(p, j) { if (j && j.stale) p.append(el('p', 'note warn', 'GitHub could not be reached just now, so this is the last answer it gave.')); }

// ---------- steps ----------
function stepsSimple(p, r) { // no version yet, or no saved detail: the plain list
  p.append(el('h3', '', 'Release steps'));
  const ol = el('ol', 'steps simple');
  for (const x of r.steps) {
    const li = el('li'); const g = el('span', 'g tag ' + STEP_KIND[x.state], GLYPH[x.state]); g.setAttribute('aria-hidden', 'true');
    li.append(g, el('span', 'nm', x.name), el('span', 'at', x.state === 'done' && x.at ? when(x.at) : WORD[x.state])); ol.append(li);
  }
  p.append(ol);
}

function bindOpen(det, id) {
  det.open = ui.open.has(id);
  det.addEventListener('toggle', () => { if (det.open) ui.open.add(id); else ui.open.delete(id); });
}

function panelSteps(p, j, r) {
  if (j.failure) {
    const f = j.failure, box = el('div', 'fail'); box.setAttribute('role', 'alert');
    box.append(el('div', 'ft', f.stage === 'Verify' ? 'Verify failed' : 'Stopped at ' + f.stage), el('div', 'fm', f.message));
    if (f.context && f.context.length) { const pre = el('div', 'fctx'); for (const c of f.context) pre.append(el('div', '', c)); pre.append(el('div', 'last', 'STOPPED at ' + f.stage + ' : ' + f.message)); box.append(pre); }
    if (f.log) {
      const b = el('button', '', 'Show this in the step log'); b.type = 'button';
      b.addEventListener('click', () => { ui.open.add('step-' + f.stage); drawPanel(); const t = document.getElementById('step-' + f.stage); if (t) t.scrollIntoView({ block: 'nearest' }); });
      box.append(b);
    }
    // What to do next, so a stopped release is not a dead end. This page only reads; the script does the work.
    box.append(el('p', 'fnext', 'To go on, fix the cause above, then run Release.ps1 -Channel ' + (r.channel === 'stable' ? 'stable' : 'rc') + ' on the release PC.'));
    p.append(box);
  }
  if (j.note) p.append(el('p', 'note', j.note));
  p.append(el('h3', '', 'Release steps'));
  const list = el('div', 'stepl');
  let anyLog = false;
  for (const s of j.steps) {
    const hasLog = !!s.log; anyLog = anyLog || hasLog;
    const id = 'step-' + s.name;
    const row = el(hasLog ? 'details' : 'div', 'step ' + STEP_KIND[s.state]); row.id = id;
    const sum = el(hasLog ? 'summary' : 'div', 'srow');
    const g = el('span', 'g tag ' + STEP_KIND[s.state], GLYPH[s.state]); g.setAttribute('aria-hidden', 'true');
    const nm = el('span', 'nm'); nm.append(el('span', 'n', s.name), el('span', 'help', STEP_HELP[s.name] || ''));
    const right = el('span', 'at');
    const bits = [];
    if (s.state === 'running') bits.push('Running' + (s.seconds ? ' for ' + dur(s.seconds) : ''));
    else if (s.state === 'done' || s.state === 'failed') { bits.push(s.state === 'failed' ? 'Failed' : 'Done'); if (s.seconds != null && s.end) bits.push(dur(s.seconds || 0)); }
    else bits.push(WORD[s.state]);
    right.textContent = bits.filter(Boolean).join(' · ');
    if (s.end) right.title = 'Finished ' + when(s.end);
    sum.append(g, nm, right); row.append(sum);
    // The red box above already says why the release stopped; the same words are not repeated under the step.
    if (s.reason && !(j.failure && j.failure.stage === s.name)) row.append(el('div', 'reason', s.reason));
    if (hasLog) {
      bindOpen(row, id);
      if (s.state === 'failed' && !ui.seen) ui.open.add(id);
      row.open = ui.open.has(id);
      const holder = el('div', 'vhold'); row.append(holder);
      const mount = () => {
        if (!row.open) return;
        const mark = j.failure && j.failure.log === s.log && j.failure.stage === s.name ? j.failure.line : 0;
        const live = j.running && s.state === 'running';
        mountViewer(holder, viewer(s.log, { from: s.from, to: live ? null : s.to, live, mark }));
      };
      row.addEventListener('toggle', mount); mount();
    }
    list.append(row);
  }
  ui.seen = true;
  p.append(list);
  if (!anyLog && j.recorded) p.append(el('p', 'note', 'No log was saved for these steps. Releases made before logs were saved only keep the time each step finished.'));
  if (!j.recorded) p.append(el('p', 'note', 'This PC has no release log for this version, so only Publish and Feed are worked out (from GitHub and the signed feed).'));

  p.append(el('h3', '', 'Facts'));
  const f = el('dl', 'facts');
  const add = (k, val) => { f.append(el('dt', '', k)); const dd = el('dd'); if (val instanceof Node) dd.append(val); else dd.textContent = val; f.append(dd); };
  add('Published', r.published ? when(r.published) : 'Not on GitHub');
  add('On the signed feed', r.onFeed ? 'Yes, offered now' : 'No');
  add('Withdrawn', r.withdrawn ? 'Yes, never offered' : 'No');
  add('Verify', { passed: 'Passed on a clean machine', failed: 'Failed', pending: 'Not finished', unknown: 'Not recorded here' }[r.verify] || '');
  if (j.commit) add('Code version', j.commit.slice(0, 12));
  if (j.keyId) add('Signing key', j.keyId);
  const links = el('span'); const a = link(r.url, 'Release page'); const b = link(r.runUrl, 'CI run');
  if (a) links.append(a); if (a && b) links.append('  ·  '); if (b) links.append(b);
  if (links.childNodes.length) add('Links', links);
  p.append(f);
  if (r.reason && !j.failure) p.append(el('p', 'note', 'Last stop reason: ' + r.reason));
}

// ---------- all logs ----------
function panelLogs(p, j) {
  p.append(el('h3', '', 'Saved logs for this release'));
  if (!j.logs.length) { p.append(el('p', 'note', 'No log was saved for this release. The release manager saves its own printout and the CI run logs when it runs, so releases made before that have none.')); return; }
  for (const l of j.logs) {
    const id = 'log-' + l.name;
    const det = el('details', 'logd'); det.id = id;
    const sum = el('summary', 'srow');
    sum.append(el('span', 'nm', LOG_WORD[l.kind] || l.kind), el('span', 'at', (l.kind === 'run' ? when(l.start) : 'run ' + l.run) + ' · ' + bytes(l.bytes)));
    det.append(sum); bindOpen(det, id);
    const holder = el('div', 'vhold'); det.append(holder);
    const mount = () => { if (det.open) mountViewer(holder, viewer(l.name, { from: 0, to: null, live: j.running && l.kind === 'run' && l.name === (j.logs.find((x) => x.kind === 'run') || {}).name })); };
    det.addEventListener('toggle', mount); mount();
    p.append(det);
  }
}

// ---------- tests ----------
function panelTests(p, rep) {
  if (!rep.runs.length) { p.append(el('p', 'note', rep.note || 'No test results were saved for this release.')); return; }
  for (const run of rep.runs) {
    const res = run.results, sec = el('section', 'trun');
    const h = el('h3', '', KIND_WORD[run.kind] || run.kind);
    sec.append(h);
    const sum = el('div', 'tsum');
    const bad = res.failed > 0;
    const reused = !res.suites.length && !res.passed && !res.failed && res.reusedCi;
    sum.append(chip(bad ? 'bad' : res.passed > 0 ? 'ok' : 'mut', bad ? 'Failed' : res.passed > 0 ? 'Passed' : reused ? 'Skipped on purpose' : 'Nothing found'));
    sum.append(el('span', 'big', count(res.passed) + ' passed'), el('span', bad ? 'big bad' : 'big', count(res.failed) + ' failed'), el('span', 'big', count(res.skipped) + ' skipped'));
    const a = link(run.url, 'Open run ' + run.run + ' on GitHub'); if (a) sum.append(a);
    sec.append(sum);
    if (reused) sec.append(el('p', 'note', 'The release build did not run the tests again. CI run ' + res.reusedCi + ' had already passed them on this exact commit, so they were skipped on purpose. Its results are in the CI section of this tab.'));
    else if (!res.suites.length) sec.append(el('p', 'note', 'No test output was found in this run log.'));
    for (const s of res.suites) {
      const id = 'suite-' + run.kind + '-' + s.job + '-' + s.step;
      const det = el('details', 'suite' + (s.failed && !s.reportOnly ? ' bad' : '')); bindOpen(det, id);
      if (s.failed && !s.reportOnly && !ui.seenT) ui.open.add(id), det.open = true;
      const sm = el('summary', 'srow');
      const state = s.reportOnly ? ['mut', 'Report only'] : s.failed ? ['bad', 'Failed'] : ['ok', 'Passed'];
      sm.append(chip(state[0], state[1]));
      const nm = el('span', 'nm'); nm.append(el('span', 'n', s.step), el('span', 'help', s.job));
      sm.append(nm);
      const parts = [count(s.passed) + ' ' + (UNIT_WORD[s.unit] || 'tests') + ' passed'];
      if (s.failed) parts.push(count(s.failed) + ' failed'); if (s.skipped) parts.push(count(s.skipped) + ' skipped');
      sm.append(el('span', 'at', parts.join(' · ')));
      det.append(sm);
      const body = el('div', 'sbody');
      if (s.reportOnly) body.append(el('p', 'note', 'This step only reports. It never stops a release.'));
      for (const f of s.failures || []) {
        const fb = el('div', 'tf'); fb.append(el('div', 'tfn', f.name));
        if (f.output && f.output.length) { const pre = el('pre', 'tfo'); pre.textContent = f.output.join('\n'); fb.append(pre); }
        body.append(fb);
      }
      if (s.cases && s.cases.length) {
        const cd = el('details', 'cases'); cd.append(el('summary', '', 'All ' + count(s.cases.length) + (s.casesCut ? '+' : '') + ' ' + (UNIT_WORD[s.unit] || 'tests') + ' in this step'));
        const ul = el('ul');
        for (const c of s.cases) { const li = el('li'); li.append(chip(c.status === 'passed' ? 'ok' : c.status === 'failed' ? 'bad' : 'mut', c.status === 'passed' ? '✓' : c.status === 'failed' ? '✗' : '–'), el('span', 'cn', c.name)); li.title = c.status; ul.append(li); }
        cd.append(ul); if (s.casesCut) cd.append(el('p', 'note', 'Only the first ' + s.cases.length + ' are listed.')); body.append(cd);
      } else if (!s.failures || !s.failures.length) body.append(el('p', 'note', 'This step prints only a summary, so single tests are not listed.'));
      det.append(body); sec.append(det);
    }
    p.append(sec);
  }
  ui.seenT = true;
}

// ---------- CI checks ----------
function checkState(c) {
  if (c.status !== 'completed') return c.status === 'queued' ? ['mut', 'Waiting'] : ['run', 'Running'];
  switch (c.conclusion) {
    case 'success': return ['ok', 'Passed'];
    case 'skipped': case 'neutral': return ['mut', 'Skipped'];
    case 'cancelled': return ['bad', 'Cancelled'];
    case 'timed_out': return ['bad', 'Timed out'];
    case 'action_required': return ['warn', 'Needs action'];
    default: return ['bad', 'Failed'];
  }
}
function panelChecks(p, j) {
  const list = j.data || []; staleNote(p, j);
  if (!list.length) { p.append(el('p', 'note', 'GitHub has no CI checks for this release yet. They show once the release commit has been built.')); return; }
  const n = { ok: 0, bad: 0, run: 0, mut: 0, warn: 0 };
  for (const c of list) n[checkState(c)[0]]++;
  const sum = el('div', 'tsum');
  sum.append(chip(n.bad ? 'bad' : n.run ? 'run' : 'ok', n.bad ? 'Failed' : n.run ? 'Running' : 'Passed'), el('span', 'big', n.ok + ' passed'), el('span', n.bad ? 'big bad' : 'big', n.bad + ' failed'));
  if (n.run) sum.append(el('span', 'big', n.run + ' running')); if (n.mut) sum.append(el('span', 'big', n.mut + ' skipped or waiting'));
  p.append(sum);
  const ul = el('ul', 'rows');
  for (const c of list.slice().sort((a, b) => (checkState(a)[0] === 'bad' ? 0 : 1) - (checkState(b)[0] === 'bad' ? 0 : 1))) {
    const [k, w] = checkState(c);
    const li = el('li'); li.append(chip(k, w), el('span', 'nm', c.name), el('span', 'at', c.seconds ? dur(c.seconds) : ''));
    const a = link(c.url, 'Open'); li.append(a || el('span'));
    ul.append(li);
  }
  p.append(ul);
}

// ---------- files ----------
function panelFiles(p, info) {
  if (info.note) p.append(el('p', 'note', info.note));
  if (info.assets.length) {
    const t = el('table', 'files');
    const head = el('tr'); for (const h of ['File', 'Size', 'SHA-256 (as GitHub recorded it)', '']) head.append(el('th', '', h)); t.append(head);
    for (const a of info.assets) {
      const tr = el('tr'); const dg = (a.digest || '').replace(/^sha256:/, '');
      const dl = link(a.browser_download_url, a.name);
      const c1 = el('td', 'fn'); c1.append(dl || a.name);
      const c3 = el('td', 'hash', dg || 'GitHub did not record one');
      const c4 = el('td');
      if (dg) { const b = el('button', '', 'Copy'); b.type = 'button'; b.title = 'Copy the SHA-256'; b.addEventListener('click', () => copyText(dg, b)); c4.append(b); }
      tr.append(c1, el('td', '', bytes(a.size)), c3, c4); t.append(tr);
    }
    p.append(t);
  }
  const f = el('dl', 'facts');
  if (info.keyId) { f.append(el('dt', '', 'Signing key id'), el('dd', 'mono', info.keyId)); }
  if (info.manifestSha) { f.append(el('dt', '', 'Signed feed file'), el('dd', 'mono', info.manifestSha)); }
  if (f.childNodes.length) { p.append(el('h3', '', 'Signing')); p.append(f); p.append(el('p', 'note', 'The key id says which signing key made the release. The feed file hash is the SHA-256 of the signed answer installed apps read.')); }
}

// ---------- changes ----------
function panelChanges(p, j) {
  const c = j.data; staleNote(p, j);
  if (!c || !c.previous) { p.append(el('p', 'note', 'There is no earlier release in this channel to compare with.')); return; }
  p.append(el('p', 'note', 'Changes since ' + c.previous + ': ' + count(c.total) + (c.total === 1 ? ' commit' : ' commits') + (c.more ? ' (the first ' + c.commits.length + ' are listed)' : '') + '.'));
  const ul = el('ul', 'rows chg');
  for (const x of c.commits) {
    const li = el('li'); const a = link(x.url, x.sha); li.append(a || el('span', 'mono', x.sha), el('span', 'nm', x.title));
    if (x.pr) { const pr = link(OWN + 'pull/' + x.pr, 'PR ' + x.pr); li.append(pr || el('span')); } else li.append(el('span'));
    ul.append(li);
  }
  p.append(ul);
}
