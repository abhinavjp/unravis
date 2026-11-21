'use strict';
const $ = (id) => document.getElementById(id);
const OWN = 'https://github.com/abhinavjp/unravis/';
function el(tag, cls, text) { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; }
function link(url, text) { if (typeof url !== 'string' || !url.startsWith(OWN)) return el('span', '', text); const a = el('a', '', text); a.href = url; a.target = '_blank'; a.rel = 'noopener noreferrer'; return a; }
function when(s) { if (!s) return ''; const d = new Date(s); return isNaN(d) ? '' : d.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }); }
function pill(text, kind) { return el('span', 'pill ' + (kind || ''), text); }

let timer = 0, busy = false, last = null;
async function load(refresh) {
  if (busy) return; busy = true;
  try {
    const r = await fetch('/api/snapshot' + (refresh ? '?refresh=1' : ''), { cache: 'no-store' });
    if (!r.ok) throw new Error('status ' + r.status);
    last = await r.json(); render(last);
  } catch (e) { $('sub').textContent = 'Read only. Could not read the dashboard server. Is unravis-releases still running?'; }
  finally { busy = false; schedule(); }
}
function schedule() {
  clearTimeout(timer);
  const running = last && last.run && last.run.status === 'running';
  timer = setTimeout(() => { if (!document.hidden) load(false); else schedule(); }, running ? 5000 : 30000);
}
function render(s) {
  $('sub').textContent = 'Read only. Updated ' + when(s.at) + '.';
  const run = $('run'); run.replaceChildren();
  if (s.run) {
    const kind = s.run.status === 'running' ? 'run' : (s.run.status === 'stopped' ? 'bad' : '');
    const b = el('div', 'banner ' + kind);
    const txt = s.run.status === 'running' ? 'Release ' + s.run.version + ' is running. Last step finished: ' + (s.run.stage || 'none') + '.'
      : s.run.status === 'stopped' ? 'Last run of ' + s.run.version + ' stopped after ' + (s.run.stage || 'the start') + (s.run.reason ? ': ' + s.run.reason : '.')
      : 'Last run: ' + s.run.version + ' finished.';
    b.textContent = txt; run.append(b);
  }
  const feeds = $('feeds'); feeds.replaceChildren();
  for (const f of s.feeds || []) {
    const c = el('div', 'card'); c.append(el('b', '', f.channel === 'rc' ? 'rc channel' : 'stable channel'));
    if (f.status === 'ok') {
      c.append(el('div', '', 'Offers ' + f.version));
      c.append(el('div', 'mut', 'Signed ' + when(f.published) + '. Expires ' + when(f.expires) + '.'));
    } else if (f.status === 'unreachable') c.append(el('div', 'mut', 'Nothing published here yet, or the feed could not be reached.'));
    else if (f.status === 'rejected') c.append(pill('Failed the signature or trust check', 'bad'));
    else c.append(el('div', 'mut', 'Nothing on this channel.'));
    const bits = [];
    if (f.paused) bits.push('Paused'); if (f.rollout) bits.push('Rollout ' + f.rollout);
    if (f.blocked && f.blocked.length) bits.push('Withdrawn: ' + f.blocked.join(', '));
    if (f.detail) bits.push(f.detail); if (f.note) bits.push(f.note);
    for (const t of bits) c.append(el('div', 'mut', t));
    feeds.append(c);
  }
  const rows = $('rows'); rows.replaceChildren();
  if (!(s.releases || []).length) { const tr = el('tr'); const td = el('td', 'mut', 'No releases found yet.'); td.colSpan = 5; tr.append(td); rows.append(tr); }
  for (const r of s.releases || []) {
    const tr = el('tr', r.withdrawn ? 'wd' : '');
    const v = el('td'); v.append(link(r.url, r.version)); tr.append(v);
    tr.append(el('td', '', r.channel));
    const st = el('td');
    if (r.withdrawn) st.append(pill('Withdrawn', 'bad'), ' ');
    if (r.onFeed) st.append(pill('On feed', 'ok'), ' ');
    if (r.verify === 'passed') st.append(pill('Verified', 'ok'));
    else if (r.verify === 'failed') st.append(pill('Verify failed', 'bad'));
    else if (r.verify === 'pending') st.append(pill('Verify pending', 'warn'));
    if (r.runUrl) { st.append(' '); st.append(link(r.runUrl, 'run')); }
    if (r.reason) st.append(el('div', 'mut', r.reason));
    tr.append(st);
    const sc = el('td'); const steps = el('div', 'steps');
    for (const x of r.steps) { const e = el('span', 's ' + x.state, x.name); e.title = x.state + (x.at ? ' ' + when(x.at) : '') + (x.reason ? ': ' + x.reason : ''); steps.append(e); }
    sc.append(steps); tr.append(sc);
    tr.append(el('td', 'mut', when(r.published)));
    rows.append(tr);
  }
  const srcs = [];
  srcs.push('GitHub list: ' + (s.github.ok ? 'ok, read ' + when(s.github.at) : (s.github.detail || 'not read')));
  srcs.push('Feed: ' + (s.feed.ok ? 'ok' : s.feed.detail));
  srcs.push('Release log: ' + (s.local.ok ? 'found' : s.local.detail));
  $('src').textContent = srcs.join('  |  ');
}
$('refresh').addEventListener('click', () => load(true));
document.addEventListener('visibilitychange', () => { if (!document.hidden) load(false); });
load(false);
