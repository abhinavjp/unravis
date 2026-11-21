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
  if (r.withdrawn) return ['Withdrawn', 'bad'];
  if (r.verify === 'failed') return ['Verify failed', 'bad'];
  if (r.steps.some((s) => s.state === 'failed')) return ['Stopped', 'bad'];
  if (r.steps.some((s) => s.state === 'running')) return ['Running', 'run'];
  if (r.verify === 'passed') return [r.onFeed ? 'Live, verified' : 'Verified', 'ok'];
  if (r.verify === 'pending') return ['Verifying', 'warn'];
  if (r.onFeed) return ['On feed', 'ok'];
  if (r.onGitHub) return ['Published', 'mut'];
  return ['', 'mut'];
}

let snap = null, selected = '', timer = 0, busy = false, failed = false;
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
  const running = snap && snap.run && snap.run.status === 'running';
  timer = setTimeout(() => { if (!document.hidden) load(false); else schedule(); }, running || failed ? 5000 : 30000);
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
  if (s.run) {
    if (s.run.status === 'running') run.append(el('span', 'on', 'Release ' + s.run.version + ' is running.'), ' Last step finished: ' + (s.run.stage || 'none') + '.');
    else if (s.run.status === 'stopped') run.append(el('span', 'off', 'Release ' + s.run.version + ' stopped'), ' after ' + (s.run.stage || 'the start') + (s.run.reason ? ': ' + s.run.reason : '.'));
  }
  const rels = s.releases || [];
  if (!rels.some((r) => r.version === selected)) selected = rels.length ? rels[0].version : '';
  const ul = $('list'); ul.replaceChildren();
  if (!rels.length) ul.append(el('li', 'empty', 'No releases found yet.'));
  for (const r of rels) {
    const li = el('li', r.withdrawn ? 'wd' : ''); li.id = 'r-' + r.version; li.setAttribute('role', 'option');
    li.setAttribute('aria-selected', String(r.version === selected)); li.dataset.v = r.version;
    li.append(el('span', 'ver', r.version));
    const [word, kind] = status(r); li.append(el('span', 'tag ' + kind, word));
    li.append(el('span', 'when', (r.published ? when(r.published) : 'Not published') + ' · ' + r.channel));
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

function renderDetail(r) {
  const d = $('detail'); d.replaceChildren();
  if (!r) { d.append(el('div', 'empty', 'Select a release.')); return; }
  d.append(el('h1', '', r.version));
  const [word, kind] = status(r);
  const meta = el('div', 'meta'); meta.append(r.channel + ' channel · ');
  const t = el('span', 'tag ' + kind, word || 'Not published'); meta.append(t); d.append(meta);

  d.append(el('h2', '', 'Release steps'));
  const ol = el('ol', 'steps');
  for (const x of r.steps) {
    const li = el('li'); const g = el('span', 'g tag ' + STEP_KIND[x.state], GLYPH[x.state]); g.setAttribute('aria-hidden', 'true');
    li.append(g, el('span', '', x.name));
    const right = el('span', 'at', x.state === 'done' && x.at ? when(x.at) : WORD[x.state]);
    right.title = WORD[x.state]; li.append(right); ol.append(li);
    if (x.reason) { const sub = el('li', 'sub'); sub.append(el('span', '', ''), el('span', '', x.reason)); ol.append(sub); }
  }
  d.append(ol);
  if (!r.recorded) d.append(el('p', 'note', 'This PC has no release log for this version, so only Publish and Feed are worked out (from GitHub and the signed feed).'));

  d.append(el('h2', '', 'Facts'));
  const f = el('dl', 'facts');
  const add = (k, v) => { f.append(el('dt', '', k)); const dd = el('dd'); if (v instanceof Node) dd.append(v); else dd.textContent = v; f.append(dd); };
  add('Published', r.published ? when(r.published) : 'Not on GitHub');
  add('On the signed feed', r.onFeed ? 'Yes, offered now' : 'No');
  add('Withdrawn', r.withdrawn ? 'Yes, never offered' : 'No');
  add('Verify', { passed: 'Passed on a clean machine', failed: 'Failed', pending: 'Not finished', unknown: 'Not recorded here' }[r.verify] || '');
  const links = el('span'); const a = link(r.url, 'Release page'); const b = link(r.runUrl, 'CI run');
  if (a) links.append(a); if (a && b) links.append('  ·  '); if (b) links.append(b);
  if (links.childNodes.length) add('Links', links);
  d.append(f);
  if (r.reason) d.append(el('p', 'note', 'Last stop reason: ' + r.reason));
}

function pick(v) {
  selected = v; try { sessionStorage.setItem('rd.sel', v); } catch (e) { /* not kept */ }
  if (snap) render();
  const li = document.getElementById('r-' + v); if (li) li.scrollIntoView({ block: 'nearest' });
}
$('list').addEventListener('click', (e) => { const li = e.target.closest('li[data-v]'); if (li) pick(li.dataset.v); });
document.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  if (e.key === 'r' || e.key === 'R') { load(true); return; }
  const i = snap ? snap.releases.findIndex((r) => r.version === selected) : -1;
  if (i < 0) return;
  let n = -1;
  if (e.key === 'ArrowDown' || e.key === 'j') n = Math.min(i + 1, snap.releases.length - 1);
  else if (e.key === 'ArrowUp' || e.key === 'k') n = Math.max(i - 1, 0);
  else if (e.key === 'Home') n = 0; else if (e.key === 'End') n = snap.releases.length - 1;
  if (n >= 0) { e.preventDefault(); pick(snap.releases[n].version); }
});
$('refresh').addEventListener('click', () => load(true));
document.addEventListener('visibilitychange', () => { if (!document.hidden) load(false); });
load(false);
window.addEventListener('pagehide', () => { try { fetch('/api/bye', { keepalive: true, cache: 'no-store' }); } catch (e) { /* closing anyway */ } });
setInterval(() => { fetch('/api/ping', { cache: 'no-store' }).catch(() => {}); }, 20000);
