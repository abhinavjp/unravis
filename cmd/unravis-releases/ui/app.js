'use strict';
const $ = (id) => document.getElementById(id);
const OWN = 'https://github.com/abhinavjp/unravis/';
const GLYPH = { done: '✓', failed: '✗', running: '▶', pending: '·', unknown: '?' };
const WORD = { done: 'Done', failed: 'Failed', running: 'Running', pending: 'Waiting', unknown: 'Not recorded here' };
const STEP_KIND = { done: 'ok', failed: 'bad', running: 'run', pending: 'mut', unknown: 'mut' };

function el(tag, cls, text) { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; }
function link(url, text) { if (typeof url !== 'string' || !url.startsWith(OWN)) return null; const a = el('a', '', text); a.href = url; a.target = '_blank'; a.rel = 'noopener noreferrer'; return a; }
function when(s, withTime) {
  if (!s) return ''; const d = new Date(s); if (isNaN(d)) return '';
  return d.toLocaleString([], withTime === false ? { dateStyle: 'medium' } : { dateStyle: 'medium', timeStyle: 'short' });
}
function ago(s) {
  const d = new Date(s); if (isNaN(d)) return ''; const m = Math.round((Date.now() - d) / 60000);
  if (m < 1) return 'just now'; if (m < 60) return m + ' min ago'; const h = Math.round(m / 60); if (h < 48) return h + ' h ago';
  return Math.round(h / 24) + ' days ago';
}
// One word for where a release stands, plus the colour family. The word always carries the meaning; colour only helps.
function status(r) {
  // Withdrawn is a decision someone made, so it is amber and not the red of a release that broke.
  if (r.withdrawn) return ['Withdrawn', 'warn'];
  if (r.verify === 'failed') return ['Verify failed', 'bad'];
  const broke = r.steps.find((s) => s.state === 'failed');
  if (broke) return ['Stopped at ' + broke.name, 'bad'];
  if (r.steps.some((s) => s.state === 'running')) return ['Running', 'run'];
  if (r.verify === 'passed') return [r.onFeed ? 'Live, verified' : 'Verified', 'ok'];
  if (r.verify === 'pending') return ['Verifying', 'warn'];
  if (r.onFeed) return ['On feed', 'ok'];
  if (r.onGitHub) return ['Published', 'mut'];
  return ['', 'mut'];
}

const STEPS = ['Preflight', 'Tag', 'Build', 'Sign', 'Publish', 'Feed', 'Verify'];
const NEXT = 'next'; // list key of the release that has no version yet (the script is still waiting for CI)
let shown = [], snap = null, selected = '', timer = 0, busy = false, failed = false, liveKey = '';
try { selected = sessionStorage.getItem('rd.sel') || ''; } catch (e) { /* the choice just is not kept */ }

async function load(refresh) {
  if (busy) return; busy = true;
  try {
    const r = await fetch('/api/snapshot' + (refresh ? '?refresh=1' : ''), { cache: 'no-store' });
    if (!r.ok) throw new Error('status ' + r.status);
    snap = await r.json(); failed = false; render();
  } catch (e) {
    $('foot').replaceChildren(el('span', '', 'Lost the connection to the program. Trying again...')); failed = true; return;
  } finally { busy = false; schedule(); }
}
function schedule() {
  clearTimeout(timer);
  const running = snap && ((snap.live && !snap.live.gone) || (snap.run && snap.run.status === 'running'));
  timer = setTimeout(() => { if (!document.hidden) load(false); else schedule(); }, running || failed ? 5000 : 30000);
}

// The running banner names a release, so it is a link to that release's page (it looked like one and did nothing).
function runLink(text, version) {
  const a = el('a', 'on', text); a.href = '#';
  a.addEventListener('click', (e) => { e.preventDefault(); if (shown.some((r) => r.version === version)) pick(version); });
  return a;
}

// Nothing was found: say why, one line for each place the list comes from, and what to do about it (BLACKBOX-08).
function emptyState(s) {
  const box = $('nolist'); box.replaceChildren();
  box.append(el('p', 'nl-title', 'No releases found yet.'));
  const ul = el('ul', 'nl-causes');
  const row = (name, ok, word) => { const li = el('li', ok ? 'ok' : 'bad'); li.append(el('span', 'nl-name', name), el('span', 'nl-word', word)); ul.append(li); };
  row('GitHub list', s.github.ok, s.github.ok ? 'read ' + ago(s.github.at) : (s.github.detail || 'not read'));
  row('Update feed', s.feed.ok, s.feed.ok ? 'read' : (s.feed.detail || 'not read'));
  row('Release log', s.local.ok, s.local.ok ? 'found on this PC' : 'not on this PC');
  box.append(ul, el('p', 'nl-next', 'Check the network, or run Release.ps1 on the release PC. Then press Refresh.'));
}

function channelBox(id, name, f) {
  const box = $(id); box.replaceChildren(el('div', 'k', name));
  if (!f || f.status === 'none') { box.append(el('div', 'v', 'No release yet'), el('div', 's', 'Nothing published here, or not reachable.')); return; }
  if (f.status === 'unreachable') { box.append(el('div', 'v', 'No release yet'), el('div', 's', 'Nothing published here, or the feed could not be read.')); return; }
  if (f.status === 'rejected') { box.append(el('div', 'v', 'Check failed'), el('div', 's', 'The feed answer failed its signature check.')); return; }
  box.append(el('div', 'v', f.version));
  const bits = ['Signed ' + ago(f.published)];
  if (f.paused) bits.unshift('Paused');
  if (f.rollout) bits.push('Rollout ' + f.rollout);
  if (f.blocked && f.blocked.length) bits.push(f.blocked.length + ' withdrawn');
  box.append(el('div', 's', bits.join(' · ')));
}

function render() {
  const s = snap;
  const feed = (c) => (s.feeds || []).find((f) => f.channel === c);
  channelBox('chan-rc', 'rc', feed('rc'));
  channelBox('chan-stable', 'stable', feed('stable'));
  const run = $('run'); run.replaceChildren();
  const lv = s.live;
  if (lv) {
    const who = lv.version ? 'Release ' + lv.version : 'A release';
    if (lv.gone) run.append(el('span', 'off', who + ' stopped without saying why.'), ' ' + lv.label + '. Started ' + ago(lv.started) + '. Run Release.ps1 -Status on the release PC.');
    else run.append(runLink(who + ' is running.', lv.version || NEXT), ' ' + lv.label + '. Started ' + ago(lv.started) + '.');
  } else if (s.run) {
    if (s.run.status === 'running') run.append(runLink('Release ' + s.run.version + ' is running.', s.run.version), ' Last step finished: ' + (s.run.stage || 'none') + '.');
    else if (s.run.status === 'stopped') run.append(el('span', 'off', 'Release ' + s.run.version + ' stopped'), ' after ' + (s.run.stage || 'the start') + (s.run.reason ? ': ' + s.run.reason : '.'));
  }
  const rels = (s.releases || []).slice();
  if (lv && !lv.version) rels.unshift({ version: NEXT, label: 'Next release', channel: 'rc or stable', withdrawn: false, verify: 'unknown', onFeed: false, onGitHub: false, recorded: true, steps: STEPS.map((n) => ({ name: n, state: n === lv.step ? 'running' : 'pending' })) });
  shown = rels;
  const key = lv ? (lv.version || NEXT) : '';
  if (key && key !== liveKey) selected = key; // a release just started: show it
  liveKey = key;
  if (!rels.some((r) => r.version === selected)) selected = rels.length ? rels[0].version : '';
  const ul = $('list'); ul.replaceChildren();
  ul.hidden = !rels.length; $('nolist').hidden = !!rels.length; $('refresh').classList.toggle('primary', !rels.length);
  if (!rels.length) emptyState(s);
  for (const r of rels) {
    const li = el('li', r.withdrawn ? 'wd' : ''); li.id = 'r-' + r.version.replace(/[^A-Za-z0-9_.-]/g, '_'); li.setAttribute('role', 'option');
    li.setAttribute('aria-selected', String(r.version === selected)); li.dataset.v = r.version;
    li.append(el('span', 'ver', r.label || r.version));
    const [word, kind] = status(r); li.append(el('span', 'tag ' + kind, word));
    li.append(el('span', 'when', (r.published ? when(r.published) : (r.label ? 'Waiting to start' : 'Not published')) + ' · ' + r.channel));
    ul.append(li);
  }
  const sel = ul.querySelector('[aria-selected=true]'); if (sel) { ul.setAttribute('aria-activedescendant', sel.id); }
  renderDetail(rels.find((r) => r.version === selected));
  const foot = $('foot'); foot.replaceChildren();
  foot.append(el('span', '', 'Read only'),
    el('span', '', 'GitHub list: ' + (s.github.ok ? 'read ' + ago(s.github.at) : (s.github.detail || 'not read'))),
    el('span', '', 'Feed: ' + (s.feed.ok ? 'ok' : s.feed.detail)),
    el('span', '', 'Release log: ' + (s.local.ok ? 'found on this PC' : 'not on this PC')),
    el('span', '', 'Updated ' + ago(s.at)));
}

function pick(v) {
  selected = v; try { sessionStorage.setItem('rd.sel', v); } catch (e) { /* not kept */ }
  if (snap) render();
  const li = document.getElementById('r-' + v); if (li) li.scrollIntoView({ block: 'nearest' });
}
$('list').addEventListener('click', (e) => { const li = e.target.closest('li[data-v]'); if (li) pick(li.dataset.v); });
document.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target; // typing in a search box, or scrolling a log, must not change the release or refresh
  if (t && t.closest && t.closest('input, textarea, select, .vbody, .tabs')) return;
  if (e.key === 'r' || e.key === 'R') { load(true); return; }
  const list = shown; // the same rows the page draws, including the Next release row
  const i = list.findIndex((r) => r.version === selected);
  if (i < 0) return;
  let n = -1;
  if (e.key === 'ArrowDown' || e.key === 'j') n = Math.min(i + 1, list.length - 1);
  else if (e.key === 'ArrowUp' || e.key === 'k') n = Math.max(i - 1, 0);
  else if (e.key === 'Home') n = 0; else if (e.key === 'End') n = list.length - 1;
  if (n >= 0) { e.preventDefault(); pick(list[n].version); }
});
$('refresh').addEventListener('click', () => load(true));
document.addEventListener('visibilitychange', () => { if (!document.hidden) load(false); });
load(false);
window.addEventListener('pagehide', () => { try { fetch('/api/bye', { keepalive: true, cache: 'no-store' }); } catch (e) { /* closing anyway */ } });
setInterval(() => { fetch('/api/ping', { cache: 'no-store' }).catch(() => {}); }, 20000);
