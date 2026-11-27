'use strict';
// Homestretch: the page. Overview, Runs, one run with its live log, Releases and Machines.
// Every view is a function that returns HTML from D (data.js) and is painted with innerHTML; all text goes through esc().
// Sizes that depend on data are written as data-h / data-w / data-flex and applied by applyDyn (the page's CSP forbids style="").

const P = () => D.projects.find((p) => p.id === D.pid) || {};
const REPO = () => P().repo || '';
const ghUrl = (path) => `https://github.com/${REPO()}${path}`;
const ts = (v) => (typeof v === 'number' ? v : Date.parse(v));

// ---------- write actions that are not built yet ----------
const LATER = (why) => toast('Later, not in the first release. ' + why);
const WHY = {
  cancel: 'Cancel needs write actions built first, and it only works for runs on this PC.',
  rerun: 'Re-run needs write actions built first.',
  rerunjob: 'Re-running only the failed job needs write actions and a change to the runner.',
  pause: 'Pause needs write actions built first, and it only works for this PC.',
};
const laterBtn = (cls, icon, label, why, extra) => `<button type="button" class="${cls} later" data-later="${esc(why)}" ${extra || ''}>${icon}${label}<span class="chip">Later</span></button>`;

// ---------- reading the runner's words ----------
// "q-20261125-101500-..." is the request id; the run's wait is the time between the request and the start.
function waitMs(r) {
  const m = /^q-(\d{4})(\d\d)(\d\d)-(\d\d)(\d\d)(\d\d)/.exec(r.request || '');
  if (!m || !r.start) return null;
  const q = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]);
  const w = Date.parse(r.start) - q;
  return w >= 0 && w < 864e5 ? w : null;
}
const parseWhy = (reason) => { const m = /^job ([^,]+), step (\d+) (.+)$/.exec(reason || ''); return m ? { job: m[1], n: +m[2], name: m[3] } : null; };
function whereHtml(r) {
  const s = norm(r.status);
  if (s === 'passed') return '<span class="where none">All steps passed</span>';
  if (s === 'running') return '<span class="where none">Running…</span>';
  const w = parseWhy(r.reason);
  if (w) return `<span class="where"><span class="j">${esc(w.job)} › ${w.n}</span> ${esc(w.name)}</span>`;
  if (s === 'stopped' || s === 'waiting' || !r.reason) return `<span class="where none">${esc(r.reason || (s === 'waiting' ? 'Waiting for a machine' : 'Stopped before it finished'))}</span>`;
  return `<span class="where">${esc(r.reason)}</span>`;
}

// What GitHub's own run said, from the shadow comparison ("pr 294 d87cff2e8e fast-build: local fail, GitHub pass").
let DIS = [];
function loadDis() {
  const a = (D.overview && D.overview.shadow && D.overview.shadow.disagreeing) || [];
  DIS = a.map((x) => { const m = /^pr (\d+) ([0-9a-f]+) ([^:]+): (.+)$/.exec(x); return m && { pr: +m[1], sha: m[2], ctx: m[3], txt: m[4] }; }).filter(Boolean);
}
const disFor = (r) => (r.pr ? DIS.find((d) => d.pr === r.pr && (r.headSha || r.sha || '').startsWith(d.sha)) || null : null);

// How often the same step failed in the last 12 finished runs of this workflow (counts only).
function stepRate(r) {
  const w = parseWhy(r.reason); if (!w) return null;
  const rs = D.runs.filter((x) => x.workflow === r.workflow && ['failed', 'passed'].includes(norm(x.status))).slice(0, 12);
  const same = rs.filter((x) => { const y = parseWhy(x.reason); return y && y.job === w.job && y.n === w.n; }).length;
  return { same, of: rs.length };
}
// How long a branch has been red: walk the pushes from the newest back to the last pass.
function redSince(branch, wf) {
  const rs = D.runs.filter((x) => x.branch === branch && x.workflow === wf && !x.pr && ['failed', 'passed'].includes(norm(x.status)));
  let since = null; let sawPass = false;
  for (const x of rs) { if (norm(x.status) === 'failed') since = x.start; else { sawPass = true; break; } }
  return since ? { since, sure: sawPass, n: rs.length } : null;
}

// ---------- state ----------
const S = { open: {}, fold: {}, step: null, logQ: '', logHit: 0, logFilter: 'all', wrap: true, follow: true, full: false, rel: null, menu: null, pin: null, busy: {}, lineTop: 0, runErr: '' };

// ---------- routing ----------
const route = () => {
  const raw = location.hash.replace(/^#\/?/, ''); const [path, qs] = raw.split('?'); const h = path.split('/');
  return { name: h[0] || 'overview', id: h[1] ? decodeURIComponent(h[1]) : null, q: new URLSearchParams(qs || '') };
};
const go = (p) => { location.hash = p; };
function setQuery(upd) {
  const raw = location.hash.replace(/^#\/?/, ''); const [base, qs] = raw.split('?'); const q = new URLSearchParams(qs || '');
  for (const [k, v] of Object.entries(upd)) { if (v === '' || v == null || v === false) q.delete(k); else q.set(k, v === true ? '1' : String(v)); }
  const s = q.toString(); history.replaceState(null, '', '#/' + base + (s ? '?' + s : ''));
}
let lastPath = '';

// ---------- header ----------
function worst() {
  const o = D.overview; if (!o) return 'passed';
  if ((o.branches || []).some((b) => b.run && norm(b.run.status) === 'failed')) return 'failed';
  if ((o.running || []).length) return 'running';
  return 'passed';
}
const FAV = { passed: 'favicon-passed.svg', failed: 'favicon-failed.svg', running: 'favicon-running.svg' };

function paintChrome() {
  const w = worst(); const R = route(); const o = D.overview;
  $('#sw').innerHTML = `<button type="button" class="btn-q" id="swb" aria-haspopup="true" aria-expanded="${S.menu === 'proj'}"><span class="dot ${w}"></span><b class="fw6">${esc(P().name || 'No project')}</b>${ico('chev')}</button>
    <div class="menu" ${S.menu === 'proj' ? '' : 'hidden'} role="menu"><div class="hd">Projects</div>
    <button type="button" class="mi" role="menuitem" disabled><span class="dot dot-open"></span><span><b>All projects</b><span class="sub">One view of every project. Shows once there are two or more.</span></span></button>
    ${D.projects.map((p) => `<button type="button" class="mi" role="menuitemradio" aria-checked="${p.id === D.pid}" data-proj="${esc(p.id)}"><span class="dot ${p.id === D.pid ? w : ''}"></span><span><b>${esc(p.name)}</b><span class="sub mono">${esc(p.repo || '')}</span>${p.channels && p.channels.length ? `<span class="sub">· ${esc(p.channels.join(' and '))} channels</span>` : ''}</span></button>`).join('')}
    <div class="note">${ico('plus')} To add a project, list it in the projects file and restart. Each project keeps its own runs, releases and machines.</div></div>`;
  const nR = D.runsTotal || D.runs.length; const nRel = D.snapshot && D.snapshot.releases ? D.snapshot.releases.length : '';
  const nM = D.machines && D.machines.machines ? D.machines.machines.length : '';
  const cur = R.name === 'run' ? 'runs' : R.name;
  $('#tabs').innerHTML = [['overview', 'Overview', ''], ['runs', 'Runs', nR], ['releases', 'Releases', nRel], ['machines', 'Machines', nM]]
    .map(([k, l, n]) => `<a class="tab" href="#/${k}" ${cur === k ? 'aria-current="page"' : ''}>${l}${n !== '' ? `<span class="n">${esc(n)}</span>` : ''}</a>`).join('');
  const run = o ? (o.running || []).length : 0; const wait = o ? (o.waiting || []).length : 0;
  $('#now').innerHTML = run ? `<span class="c-running sic-wrap">${ico('running', 'spin')}</span><b>${run} running</b> · ${wait} waiting` : `<span class="dot"></span>Nothing running · ${wait} waiting`;
  const fc = Fresh.chip(Date.now()); // the older of the two read times; the warning style after 3 missed polls of either
  const f = $('#fresh'); f.className = 'fresh' + (fc.stale ? ' stale' : ''); f.title = fc.title;
  f.innerHTML = fc.text ? (fc.stale ? ico('clock') : '') + esc(fc.text) : '';
  $('#themesw').innerHTML = Appearance.html(S.menu === 'theme');
  Appearance.bind($('#themesw'), { toggle: () => { S.menu = S.menu === 'theme' ? null : 'theme'; paintChrome(); }, changed: () => paintChrome(), toast });
  $('#swb').onclick = (e) => { e.stopPropagation(); S.menu = S.menu === 'proj' ? null : 'proj'; paintChrome(); };
  $$('[data-proj]').forEach((b) => { b.onclick = () => { S.menu = null; if (b.dataset.proj !== D.pid) { Data.pick(b.dataset.proj); go('/overview'); render(); Data.tick(true); } else paintChrome(); }; });
  $('#fav').href = FAV[w]; // the tab shows the state at a glance
}

// ---------- small pieces ----------
const bindRows = () => $$('tr.row[data-href]').forEach((tr) => {
  const open = (e) => { if (e.target.closest('a,button,.rowmenu')) return; go(tr.dataset.href.slice(1)); };
  tr.onclick = open; tr.onkeydown = (e) => { if (e.key === 'Enter') open(e); };
});
function branchHist(branch) {
  const rs = D.runs.filter((r) => r.branch === branch && !r.pr && r.event !== 'pull_request').slice(0, 16).reverse();
  if (!rs.length) return '<span class="muted">–</span>';
  const max = Math.max(1, ...rs.map((r) => r.ms || 0));
  const bad = rs.filter((r) => norm(r.status) === 'failed').length;
  return `<div class="hist" role="img" aria-label="Last ${rs.length} pushes on ${esc(branch)}: ${bad} failed">${rs.map((r) => `<i class="${esc(norm(r.status))}" data-h="${Math.max(5, Math.round(5 + 17 * (r.ms || 0) / max))}" title="${esc(wfName(r.workflow))} · ${esc(WORD[norm(r.status)])} · ${esc(exact(r.start))} · took ${esc(dur(r.ms))}"></i>`).join('')}</div>`;
}
const loadingHtml = '<div class="empty empty-top"><b>Loading…</b> Reading this PC’s check records.</div>';
function noStore(what) {
  return `<div class="empty empty-top"><b>${esc(what || 'No check records found for this project.')}</b> ${D.err ? esc(D.err) : 'Homestretch reads the runs saved by <span class="mono">unravis-ci</span> on this PC. Start the agent once, and they show up here.'}</div>`;
}

// ---------- Overview ----------
function viewOverview() {
  const o = D.overview;
  if (!o) return D.err ? noStore('The overview could not be read.') : loadingHtml;
  if (!o.ciFound) return noStore('No check records found for this project.');
  const bs = o.branches || []; const failing = bs.filter((b) => b.run && norm(b.run.status) === 'failed');
  const reds = failing.map((b) => redSince(b.branch, b.run.workflow)).filter(Boolean);
  const oldest = reds.length ? reds.reduce((a, b) => (Date.parse(a.since) < Date.parse(b.since) ? a : b)) : null;
  let head;
  if (failing.length && failing.length === bs.length && bs.length > 1) head = `${bs.map((b) => b.branch).join(' and ')} are both failing.`;
  else if (failing.length) head = `${failing.map((b) => b.branch).join(' and ')} ${failing.length > 1 ? 'are' : 'is'} failing.`;
  else head = bs.some((b) => b.run) ? 'Everything is passing.' : 'No check has run on the main branches yet.';
  const hs = failing.length ? 'failed' : 'passed';
  let sub;
  if (oldest) {
    const w = parseWhy(failing[0].run.reason);
    sub = `Red for ${oldest.sure ? '' : 'at least '}${dur(Date.now() - Date.parse(oldest.since))}. ` + (failing.length > 1 && new Set(failing.map((b) => b.run.reason)).size === 1 ? `Both stop at the same step: ${w ? w.name : failing[0].run.reason}.` : '');
  } else sub = failing.length ? 'Open the failed run to see where it stopped.' : 'The last full check on every main branch passed.';
  const snap = D.snapshot; const rl = snap && snap.releases && snap.releases[0];
  const feeds = (snap && snap.feeds) || []; const rc = feeds.find((f) => f.channel === 'rc'); const stb = feeds.find((f) => f.channel === 'stable');
  const ag = o.agents || []; const online = ag.filter((a) => a.fresh).length;
  const free = ag.reduce((n, a) => n + Math.max(0, (a.slots || 0) - (a.jobs || []).length), 0);
  const pulls = (o.pulls || []).slice().sort((a, b) => (norm(b.status) === 'failed') - (norm(a.status) === 'failed'));
  const sh = o.shadow || {};
  const lane = (f, name) => {
    if (!f) return '';
    if (f.status === 'ok') { const v = rl && rl.version === f.version && rl.verify === 'passed'; return `<div class="lane"><div class="ch">${esc(name)}</div><div class="v">${esc(f.version)}</div><div class="s">${st(v ? 'passed' : 'unknown', v ? 'Verified' : 'On feed')} <span class="muted">${when(f.published)}</span></div></div>`; }
    return `<div class="lane"><div class="ch">${esc(name)}</div><div class="v muted">unknown</div><div class="s c-waiting">Feed not reachable from this PC</div></div>`;
  };
  const branchRow = (b) => {
    const r = b.run;
    if (!r) return `<tr><td colspan="6" class="muted">${esc(b.branch)} has no recorded run yet.</td></tr>`;
    const red = norm(r.status) === 'failed' ? redSince(b.branch, r.workflow) : null;
    return `<tr class="row" tabindex="0" data-href="#/run/${encodeURIComponent(r.id)}"><td class="w-st">${st(r.status)}</td>
      <td><a class="rl title" href="#/run/${encodeURIComponent(r.id)}">${esc(b.branch)}</a> <span class="mono muted">${esc(short(r.headSha || r.sha))}</span>${r.release ? ` <span class="chip mono">${ico('tag')}${esc(r.release)}</span>` : ''}<small>${esc(wfName(r.workflow))} · took ${esc(dur(r.ms))}</small></td>
      <td class="clip"><div>${whereHtml(r)}</div></td>
      <td class="hide-s since">${red ? `<b>${red.sure ? '' : '≥ '}${esc(dur(Date.now() - Date.parse(red.since)))}</b>` : '<span class="muted">–</span>'}</td>
      <td class="hide-s">${branchHist(b.branch)}</td><td class="num">${when(r.start)}</td></tr>`;
  };
  const pullRow = (r) => { const d = disFor(r); return `<tr class="row" tabindex="0" data-href="#/run/${encodeURIComponent(r.id)}"><td class="w-st">${st(r.status)}</td>
      <td><a class="rl title" href="#/run/${encodeURIComponent(r.id)}">PR ${esc(r.pr)}</a><small class="mono" title="${esc(r.branch)}">${esc(r.branch)}</small></td><td class="clip"><div>${whereHtml(r)}</div></td>
      <td class="hide-s">${d ? '<span class="chip warn">No: GitHub passed</span>' : '<span class="muted" title="The run on GitHub is compared about every 10 minutes. Only differences are listed.">No difference found</span>'}</td><td class="num">${when(r.start)}</td></tr>`; };
  return `<div class="lead">${sic(hs, 'lg')}<div><h1>${esc(head)}</h1><p>${esc(sub)}</p></div></div>
  <div class="nowline"><span>${sic('waiting')}<b>${(o.waiting || []).length} waiting</b></span>
    <span>${ico('pc')} <b>${online} of ${ag.length}</b> machine${ag.length === 1 ? '' : 's'} online, ${free} free slot${free === 1 ? '' : 's'}</span>
    ${rl ? `<span>${ico('tag')} Latest release <b class="mono">${esc(rl.version)}</b> ${rl.verify === 'passed' ? 'verified' : rl.verify === 'failed' ? 'failed verify' : 'not verified yet'} ${when(rl.published)}</span>` : ''}</div>
  <div class="grid2"><div>
    <section class="sec"><div class="sh"><h2>Main branches</h2><span class="muted">last full check on each</span></div>
      <table class="tbl"><thead><tr><th class="w-st">Result</th><th>Branch</th><th>Where it failed</th><th class="hide-s">Red for</th><th class="hide-s">Last 16 pushes</th><th class="num">Started</th></tr></thead><tbody>${bs.map(branchRow).join('') || '<tr><td colspan="6" class="muted">This project lists no main branches.</td></tr>'}</tbody></table></section>
    <section class="sec"><div class="sh"><h2>Pull requests</h2><span class="muted">latest run for each, failing first</span><a href="#/runs?ev=pr">All pull request runs</a></div>
      ${pulls.length ? `<table class="tbl"><thead><tr><th class="w-st">Result</th><th>Pull request</th><th>Where it failed</th><th class="hide-s">GitHub agrees?</th><th class="num">Started</th></tr></thead><tbody>${pulls.map(pullRow).join('')}</tbody></table>` : '<p class="empty">No pull request has run on this PC yet.</p>'}</section>
  </div>
  <aside class="panel">
    <div class="card"><h3>Releases</h3><a href="#/releases">All releases</a>
      <div class="lanes">${lane(rc, 'rc') || '<div class="lane"><div class="ch">rc</div><div class="v muted">unknown</div></div>'}<div class="arrow" aria-hidden="true">→</div>${lane(stb, 'stable') || '<div class="lane"><div class="ch">stable</div><div class="v muted">unknown</div></div>'}</div>
      <p class="muted">Releases move from rc to stable.${stb && stb.status !== 'ok' ? ' The stable feed cannot be read from this PC, so promotion state is unknown.' : ''}${!snap ? ' ' + esc(D.snapshotErr || 'Release data is still loading.') : ''}</p></div>
    <div class="card"><h3>Switch to local checks</h3>${sh.disagree ? `<a href="#/runs?dis=1">See ${sh.disagree} disagreement${sh.disagree === 1 ? '' : 's'}</a>` : ''}
      ${sh.available === false ? '<p class="muted">The comparison with GitHub has not been generated yet.</p>' : `<div class="streak">${Array.from({ length: sh.gate || 20 }, (_, i) => `<i class="${i < (sh.streak || 0) ? 'on' : ''}"></i>`).join('')}</div>
      <p><b>${sh.streak || 0} of ${sh.gate || 20}</b> pull requests in a row agree with GitHub</p>
      <dl class="kv"><dt>Agree</dt><dd>${sh.agree || 0}</dd><dt>Disagree</dt><dd class="${sh.disagree ? 'c-failed' : ''}">${sh.disagree || 0}</dd><dt>Only on GitHub</dt><dd>${sh.githubOnly || 0}</dd><dt>Only on this PC</dt><dd>${sh.localOnly || 0}</dd></dl>`}
      <p class="muted">GitHub still decides merges. After ${sh.gate || 20} agreeing pull requests in a row, the local result can take over.</p></div>
    <div class="card"><h3>Machines</h3><a href="#/machines">All machines</a>
      ${ag.length ? ag.map((a) => `<div class="mrow">${ico('pc')}<b>${esc(a.name)}</b>${a.fresh ? '<span class="chip ok">Online</span>' : '<span class="chip warn">Not heard from</span>'}<span class="muted">${(a.jobs || []).length ? (a.jobs || []).length + ' busy' : 'idle'} · ${a.slots || 0} slot${a.slots === 1 ? '' : 's'}</span></div><p class="muted">Heard from ${when(a.lastSeen)}. ${a.posted || 0} result${a.posted === 1 ? '' : 's'} sent to GitHub, ${a.postFailed || 0} failed.</p>`).join('') : '<p class="muted">No machine has reported yet.</p>'}</div>
  </aside></div>`;
}

// ---------- Runs ----------
function rowMenu(r) {
  const s = norm(r.status); const url = location.origin + location.pathname + '#/run/' + encodeURIComponent(r.id);
  return `<div class="rowmenu"><button type="button" class="btn ghost icon kebab" data-m aria-haspopup="true" aria-expanded="false" aria-label="Actions for this run">${ico('more')}</button>
  <div class="menu right" hidden role="menu">
    ${s === 'running' ? laterBtn('mi', ico('stop'), 'Cancel run', WHY.cancel) : ''}
    ${s === 'failed' ? laterBtn('mi', ico('rerun'), 'Re-run failed job', WHY.rerunjob) : ''}
    ${laterBtn('mi', ico('rerun'), 'Re-run all jobs', WHY.rerun)}
    ${r.pr ? `<a class="mi" href="${esc(ghUrl('/pull/' + r.pr))}" target="_blank" rel="noopener noreferrer">${ico('out')} Open PR ${esc(r.pr)} on GitHub</a>` : ''}
    <button type="button" class="mi" data-copy="${esc(url)}">${ico('copy')} Copy link to this run</button></div></div>`;
}
function bindMenus() {
  $$('.kebab').forEach((b) => { b.onclick = (e) => {
    e.stopPropagation(); const m = b.parentElement.querySelector('.menu'); const open = m.hidden;
    $$('.rowmenu .menu').forEach((x) => { x.hidden = true; }); $$('.kebab').forEach((x) => x.setAttribute('aria-expanded', 'false'));
    m.hidden = !open; b.setAttribute('aria-expanded', String(open)); S.menu = open ? 'row' : null;
  }; });
  $$('[data-later]').forEach((b) => { b.onclick = () => { LATER(b.dataset.later); $$('.rowmenu .menu').forEach((x) => { x.hidden = true; }); }; });
  $$('[data-copy]').forEach((b) => { b.onclick = () => { navigator.clipboard.writeText(b.dataset.copy).then(() => toast(b.closest('.cmd') ? 'Command copied.' : 'Link copied.'), () => toast('Could not copy.')); $$('.rowmenu .menu').forEach((x) => { x.hidden = true; }); }; });
}
function viewRuns() {
  if (!D.overview && !D.runs.length) return D.err ? noStore('The runs could not be read.') : loadingHtml;
  if (D.overview && !D.overview.ciFound) return noStore('No check records found for this project.');
  const q = route().q; const s = q.get('s') || 'all'; const wf = q.get('wf') || ''; const text = (q.get('q') || '').toLowerCase();
  const latest = q.get('latest') === '1'; const ev = q.get('ev') || ''; const dsh = q.get('dis') === '1';
  let rs = D.runs.slice();
  if (ev === 'pr') rs = rs.filter((r) => r.event === 'pull_request');
  if (dsh) rs = rs.filter(disFor);
  if (latest) { const seen = new Set(); rs = rs.filter((r) => { const k = r.workflow + '|' + (r.pr ? 'pr' + r.pr : r.branch); if (seen.has(k)) return false; seen.add(k); return true; }); }
  const cnt = (k) => (k === 'all' ? rs.length : rs.filter((r) => norm(r.status) === k).length);
  const wfs = [...new Set(D.runs.map((r) => r.workflow))];
  let list = rs;
  if (s !== 'all') list = list.filter((r) => norm(r.status) === s);
  if (wf) list = list.filter((r) => r.workflow === wf);
  if (text) list = list.filter((r) => [r.pr ? 'pr ' + r.pr : '', r.branch, r.author, wfName(r.workflow), r.reason, short(r.headSha || r.sha)].join(' ').toLowerCase().includes(text));
  const waits = list.map(waitMs).filter((x) => x != null).sort((a, b) => a - b);
  const med = waits.length ? dur(waits[Math.floor(waits.length / 2)]) : '';
  const row = (r) => { const d = disFor(r); const w = waitMs(r); return `<tr class="row" tabindex="0" data-href="#/run/${encodeURIComponent(r.id)}"><td class="w-st">${st(r.status)}</td>
    <td><a class="rl" href="#/run/${encodeURIComponent(r.id)}"><span class="title">${esc(wfName(r.workflow))}</span> <span class="muted">·</span> ${esc(subject(r))}</a>${d ? ' <span class="chip warn" title="GitHub’s own run passed">GitHub disagrees</span>' : ''}<small class="mono">${esc([r.event === 'pull_request' ? r.branch : '', r.author].filter(Boolean).join(' · ')) || '&nbsp;'}</small></td>
    <td class="clip"><div>${whereHtml(r)}</div></td><td class="hide-s mono muted">${esc(short(r.headSha || r.sha))}</td>
    <td class="num hide-s ${w != null && w > 3e5 ? 'c-waiting' : 'muted'}">${w == null ? '–' : esc(dur(w))}</td><td class="num">${norm(r.status) === 'running' ? 'running' : esc(dur(r.ms))}</td><td class="num">${when(r.start)}</td><td>${rowMenu(r)}</td></tr>`; };
  return `<div class="ph"><h1>Runs</h1><p>Every check this project ran. Each failed row shows where it failed.</p></div>
  <div class="tools"><div class="seg" role="group" aria-label="Filter by result">${['all', 'failed', 'passed', 'stopped'].map((k) => `<button type="button" data-f="${k}" aria-pressed="${s === k}">${k === 'all' ? 'All' : WORD[k]}<span class="n">${cnt(k)}</span></button>`).join('')}</div>
    <select class="sel" id="wfs" aria-label="Workflow"><option value="">All workflows</option>${wfs.map((w) => `<option value="${esc(w)}" ${w === wf ? 'selected' : ''}>${esc(wfName(w))}</option>`).join('')}</select>
    <label class="field">${ico('search')}<input id="q" type="search" placeholder="PR, branch, step, commit" value="${esc(q.get('q') || '')}" aria-label="Search runs"><kbd>/</kbd></label>
    <label class="tog" title="Hide older runs of the same pull request or branch"><input type="checkbox" id="latest" ${latest ? 'checked' : ''}> Latest per PR or branch</label>
    ${ev === 'pr' ? '<span class="chip">Pull requests only <a href="#" data-clear="ev" aria-label="Clear">×</a></span>' : ''}${dsh ? '<span class="chip warn">GitHub disagrees <a href="#" data-clear="dis" aria-label="Clear">×</a></span>' : ''}
    <span class="count">${list.length} of ${D.runs.length} loaded${D.runsTotal > D.runs.length ? ` (${D.runsTotal} in all)` : ''}${med ? ` · waited ${esc(med)} median for a machine` : ''}</span></div>
  ${list.length ? `<table class="tbl"><thead><tr><th class="w-st">Result</th><th>Run</th><th>Where it failed</th><th class="hide-s">Commit</th><th class="num hide-s" title="Time from asking for the run to a machine starting it">Waited</th><th class="num">Took</th><th class="num">Started</th><th class="w40"><span class="sr">Actions</span></th></tr></thead><tbody>${list.map(row).join('')}</tbody></table>` : '<div class="empty"><b>No run matches.</b> Clear the search or pick All to see every run again.</div>'}`;
}
function bindRuns() {
  $$('[data-f]').forEach((b) => { b.onclick = () => { setQuery({ s: b.dataset.f === 'all' ? '' : b.dataset.f }); render(); }; });
  $('#wfs').onchange = (e) => { setQuery({ wf: e.target.value }); render(); };
  $('#latest').onchange = (e) => { setQuery({ latest: e.target.checked }); render(); };
  const q = $('#q'); q.oninput = () => { const pos = q.selectionStart; setQuery({ q: q.value }); render(); const n = $('#q'); n.focus(); n.setSelectionRange(pos, pos); };
  $$('[data-clear]').forEach((a) => { a.onclick = (e) => { e.preventDefault(); setQuery({ [a.dataset.clear]: '' }); render(); }; });
  bindRows(); bindMenus();
}

// ---------- one run ----------
const splitStep = (k) => { const i = String(k).lastIndexOf(':'); return [k.slice(0, i), +k.slice(i + 1)]; };
const detOf = (id) => D.run[id] || null;
const jobsOf = (id) => (detOf(id) && detOf(id).jobs) || [];
const logOf = (id, jk, idx) => D.logs[Data.logKey(id, jk, idx)];
const failedStepOf = (j) => (j.steps || []).find((s) => norm(s.status) === 'failed');
function firstFailed(jobs) {
  const fj = jobs.find((j) => norm(j.status) === 'failed' && (j.steps || []).some((s) => norm(s.status) === 'failed' && s.hasLog)) || jobs.find((j) => norm(j.status) === 'failed');
  return fj ? { job: fj, step: failedStepOf(fj) } : { job: null, step: null };
}
const ERR_RE = /NEW FAILURE|^--- FAIL|panic:|::error::/;

// A very small Markdown reader for the run summary: headings, bullets, tables, bold, code. Everything is escaped first.
function mdx(src) {
  const inl = (s) => esc(s).replace(/`([^`]+)`/g, '<code>$1</code>').replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>');
  const out = []; let list = false; let tab = [];
  const flushT = () => { if (!tab.length) return; out.push('<table>' + tab.map((cells, i) => '<tr>' + cells.map((c) => `<${i ? 'td' : 'th'}>${inl(c)}</${i ? 'td' : 'th'}>`).join('') + '</tr>').join('') + '</table>'); tab = []; };
  for (const line of String(src || '').split(/\r?\n/)) {
    if (/^\s*\|/.test(line)) { const cells = line.trim().replace(/^\||\|$/g, '').split('|').map((c) => c.trim()); if (!cells.every((c) => /^:?-+:?$/.test(c))) tab.push(cells); continue; }
    flushT();
    const b = /^\s*[-*]\s+(.*)$/.exec(line); const h = /^(#{1,4})\s+(.*)$/.exec(line);
    if (b) { if (!list) { out.push('<ul>'); list = true; } out.push(`<li>${inl(b[1])}</li>`); continue; }
    if (list) { out.push('</ul>'); list = false; }
    if (h) out.push(`<h4>${inl(h[2])}</h4>`); else if (line.trim()) out.push(`<p>${inl(line)}</p>`);
  }
  flushT(); if (list) out.push('</ul>'); return out.join('');
}

function jobHtml(id, j) {
  const js = norm(j.status); const open = S.open[j.key] ?? (js !== 'passed');
  const total = Math.max(1, ...(j.steps || []).map((s) => s.ms || 0));
  const li = (s) => { const k = j.key + ':' + s.index; const ss = norm(s.status);
    return `<li class="step ${ss}" data-s="${esc(k)}" ${S.step === k ? 'aria-current="true"' : ''} tabindex="0" title="${s.index}. ${esc(s.name)}">${sic(ss)}<span class="nm">${s.index}. ${esc(s.name)}</span>${s.ms ? `<span class="tb" aria-hidden="true"><i data-w="${Math.max(4, Math.round(100 * s.ms / total))}"></i></span>` : ''}<span class="d">${ss === 'skipped' ? 'skip' : s.ms ? esc(dur(s.ms)) : ''}</span></li>`; };
  let body = '';
  if (open) {
    const steps = j.steps || []; const fold = js !== 'passed' && S.fold[j.key] !== false;
    if (!fold) body = steps.map(li).join('');
    else {
      const keep = (s) => ['failed', 'running', 'waiting'].includes(norm(s.status)) || S.step === j.key + ':' + s.index;
      for (let i = 0; i < steps.length;) {
        if (keep(steps[i])) { body += li(steps[i]); i++; continue; }
        let e = i; while (e < steps.length && !keep(steps[e])) e++;
        const run = steps.slice(i, e);
        if (run.length > 2) body += `<li><button type="button" class="fold" data-fold="${esc(j.key)}">${ico('chev')} ${run.filter((x) => norm(x.status) === 'passed').length} passed${run.some((x) => norm(x.status) === 'skipped') ? `, ${run.filter((x) => norm(x.status) === 'skipped').length} skipped` : ''} step${run.length === 1 ? '' : 's'} <span class="mono ml-a">${esc(dur(run.reduce((n, x) => n + (x.ms || 0), 0)))}</span></button></li>`;
        else body += run.map(li).join('');
        i = e;
      }
    }
  }
  return `<div class="job" data-open="${open}"><button type="button" class="jhead" data-j="${esc(j.key)}" aria-expanded="${open}">${sic(js)}<span>${esc(j.label || j.id)}${j.pseudo ? ' <span class="chip">runner stage</span>' : ''}</span><span class="d">${esc(dur(j.ms))}</span>${ico('chev', 'ch')}</button>${open ? `<ul class="steps">${body}</ul>` : ''}</div>`;
}

function viewRun(id) {
  const det = detOf(id); const fromList = D.runs.find((x) => x.id === id);
  if (!det && !fromList) {
    if (S.runErr) return `<div class="crumb"><a href="#/runs">Runs</a></div><div class="empty"><b>That run is not on this PC.</b> ${esc(S.runErr)} <a href="#/runs">Back to all runs</a>.</div>`;
    return `<div class="crumb"><a href="#/runs">Runs</a></div>${loadingHtml}`;
  }
  const r = (det && det.run) || fromList; const s = norm(r.status); const jobs = jobsOf(id);
  const ff = firstFailed(jobs); const failJobs = jobs.filter((j) => norm(j.status) === 'failed');
  if (!S.step) {
    if (ff.job && ff.step) S.step = ff.job.key + ':' + ff.step.index;
    else if (jobs[0]) { const f = (jobs[0].steps || []).find((x) => x.hasLog) || (jobs[0].steps || [])[0]; if (f) S.step = jobs[0].key + ':' + f.index; }
    else if (det && det.checkout) S.step = '_checkout:0';
  }
  const pinL = route().q.get('L'); if (pinL) S.pin = +pinL;
  const posted = (det && det.posted) || []; const mine = posted.find((p) => /fast-build/.test(p.context || '')) || posted[0];
  const dsh = disFor(r); const rate = s === 'failed' ? stepRate(r) : null; const wt = waitMs(r);
  const summary = (det && det.summary) || '';
  let ferr = null;
  if (ff.job && ff.step) { const lg = logOf(id, ff.job.key, ff.step.index); if (lg) { const i = lg.lines.findIndex((l) => ERR_RE.test(l.x)); if (i >= 0) ferr = { line: lg.from + i + 1, text: lg.lines[i].x }; } }
  const failEntry = s === 'failed' && (ff.step || r.reason);
  const wh = parseWhy(r.reason);
  const subj = subject(r);
  const hasLogs = jobs.length || (det && det.checkout);
  let body;
  if (hasLogs) {
    const chk = det && det.checkout ? `<div class="job" data-open="true"><button type="button" class="jhead" data-j="_checkout" aria-expanded="true">${sic('passed')}<span>Getting the code</span><span class="d"></span>${ico('chev', 'ch')}</button><ul class="steps"><li class="step passed" data-s="_checkout:0" ${S.step === '_checkout:0' ? 'aria-current="true"' : ''} tabindex="0"><span class="nm">Checkout log</span></li></ul></div>` : '';
    const planned = det && det.planned && det.planned > jobs.length && s === 'running' ? `<p class="muted fs-xs mt8">${jobs.length} of ${det.planned} jobs started so far.</p>` : '';
    body = `<div class="rbody"><div class="panel jobs" id="jobs">${chk}${jobs.map((j) => jobHtml(id, j)).join('')}${planned}</div><div id="logpane"></div></div>
      ${summary ? `<details class="panel summary"><summary>${ico('chev', 'ch')} Summary written by the run</summary><div class="md">${mdx(summary)}</div>${det.summaryCut ? '<p class="muted fs-xs">The summary was cut because it is long.</p>' : ''}</details>` : ''}`;
  } else {
    body = `<div class="empty empty-top"><b>${s === 'waiting' ? 'This run is waiting for a machine.' : s === 'stopped' ? 'This run was stopped before it finished.' : 'No step records for this run on this PC.'}</b> ${esc(r.reason || r.note || '')}</div>`;
  }
  const meta = [
    r.event === 'pull_request' && r.pr ? `<span>${ico('pr')} <a href="${esc(ghUrl('/pull/' + r.pr))}" target="_blank" rel="noopener noreferrer">PR ${esc(r.pr)}</a></span>` : '',
    r.branch ? `<span>${ico('branch')} <span class="mono">${esc(r.branch)}</span>${r.event === 'pull_request' && r.base ? ` into <span class="mono">${esc(r.base)}</span>` : ''}</span>` : '',
    r.author ? `<span>${ico('user')} ${esc(r.author)}</span>` : '',
    (r.headSha || r.sha) ? `<span>${ico('commit')} <span class="mono" title="${esc(r.headSha || r.sha)}">${esc(short(r.headSha || r.sha))}</span></span>` : '',
    `<span>${ico('clock')} started ${when(r.start)}${s !== 'running' && r.ms != null ? `, took ${esc(dur(r.ms))}` : ''}${wt != null ? `, waited ${esc(dur(wt))} for a machine` : ''}</span>`,
    r.agent ? `<span>${ico('pc')} <span class="mono">${esc(r.agent)}</span></span>` : '',
    `<span class="mono">${esc(r.workflow)}</span>`,
  ].filter(Boolean).join('');
  return `<div class="crumb"><a href="#/runs">Runs</a> / ${esc(wfName(r.workflow))} · ${esc(subj)}</div>
  <div class="rh">${sic(s, 'lg')}<div class="minw0"><h1>${esc(wfName(r.workflow))} on ${esc(subj)} ${st(s)}</h1><div class="meta">${meta}</div></div>
    <div class="acts">${s === 'running' ? laterBtn('btn', ico('stop'), 'Cancel', WHY.cancel) : ''}${s === 'failed' ? laterBtn('btn', ico('rerun'), 'Re-run failed job', WHY.rerunjob) : ''}${s !== 'running' ? laterBtn('btn', ico('rerun'), 'Re-run all', WHY.rerun) : ''}${r.pr ? `<a class="btn icon" href="${esc(ghUrl('/pull/' + r.pr))}" target="_blank" rel="noopener noreferrer" title="Open on GitHub" aria-label="Open on GitHub">${ico('out')}</a>` : ''}</div></div>
  ${failEntry ? `<div class="fail" role="region" aria-label="Why it failed">${sic('failed', 'lg')}<div><b>Failed in ${ff.job ? esc(ff.job.label || ff.job.id) : wh ? esc(wh.job) : 'a step'} › step ${ff.step ? ff.step.index : wh ? wh.n : '?'}: ${esc(ff.step ? ff.step.name : wh ? wh.name : r.reason)}</b></div>
    ${ferr ? `<div class="err" title="${esc(ferr.text)}">${esc(ferr.text)}</div>` : ff.step && ff.step.err ? `<div class="err" title="${esc(ff.step.err)}">${esc(ff.step.err)}</div>` : ''}
    <div class="hint">${rate && rate.of > 1 ? `This step failed in <b class="strong">${rate.same} of the last ${rate.of}</b> finished ${esc(wfName(r.workflow))} runs on this PC. Counts only; it does not decide whether the failure is real.` : 'There are too few finished runs of this workflow to compare.'}${ferr ? ` First error is on line ${ferr.line} of the step log.` : ''}</div>
    <div class="go">${ferr ? '<button type="button" class="btn sm" id="goerr">Go to error <kbd>e</kbd></button>' : ''}${failJobs.length > 1 ? '<button type="button" class="btn sm ghost" id="nextf">Next failed job <kbd>f</kbd></button>' : ''}</div></div>` : ''}
  ${mine ? `<div class="cmp"><span>Reported to GitHub as <span class="mono">${esc(mine.context)}</span>: ${st(mine.status || mine.state)}</span>${dsh ? '<span class="chip warn">GitHub’s own run passed: they disagree</span>' : s !== 'running' && r.pr ? '<span class="chip">No difference found with GitHub</span>' : ''}<span>Shadow mode: GitHub’s own run still decides merges.</span></div>` : ''}
  ${body}`;
}

function ensureLog(id, jk, idx) {
  const k = Data.logKey(id, jk, idx); if (S.busy[k]) return; S.busy[k] = 1;
  const done = () => { delete S.busy[k]; const R = route(); if (R.name === 'run' && R.id === id && S.step === jk + ':' + idx) paintLog(); };
  Data.loadLog(id, jk, idx).then(done, (e) => { D.logs[k] = { lines: [], from: 0, total: 0, live: false, err: e.message }; done(); });
}
function paintLog() {
  const R = route(); const id = R.id; const pane = $('#logpane'); if (!pane || !S.step) return;
  const det = detOf(id); if (!det) return;
  const [jk, idx] = splitStep(S.step); const r = det.run;
  const job = jobsOf(id).find((j) => j.key === jk); const step = job && (job.steps || []).find((x) => x.index === idx);
  const title = jk === '_checkout' ? 'Getting the code' : `${job ? (job.label || job.id) : jk} › ${idx}. ${step ? step.name : ''}`;
  const ss = step ? norm(step.status) : 'passed';
  const head = `<div class="lt"><h3>${sic(ss)}<span>${esc(title)}</span></h3></div>`;
  if (step && !step.hasLog) { pane.innerHTML = `<div class="log">${head}<div class="empty pad-log">${ss === 'skipped' ? 'This step did not run because its condition was not met.' : ss === 'waiting' ? 'This step has not started yet.' : 'This step has no log saved on this PC.'}</div></div>`; return; }
  const lg = logOf(id, jk, idx);
  if (!lg) { pane.innerHTML = `<div class="log">${head}<div class="empty pad-log">Loading the log…</div></div>`; ensureLog(id, jk, idx); return; }
  if (lg.err) { pane.innerHTML = `<div class="log">${head}<div class="empty pad-log"><b>The log could not be read.</b> ${esc(lg.err)}</div></div>`; return; }
  const cls = (x) => (/NEW FAILURE|^--- FAIL|panic:|FAIL\t|::error::|exit code [1-9]/.test(x) ? 'err' : /::notice::|::warning::/.test(x) ? 'note' : /^\s+\|/.test(x) ? 'dim' : '');
  const all = lg.lines.map((l, i) => [lg.from + i + 1, l.x, cls(l.x)]);
  const nErr = all.filter((l) => l[2] === 'err' || l[2] === 'note').length;
  const shown = S.logFilter === 'err' ? all.filter((l) => l[2] === 'err' || l[2] === 'note') : all;
  const q = S.logQ.trim().toLowerCase(); const hits = q ? shown.map((l, i) => (l[1].toLowerCase().includes(q) ? i : -1)).filter((i) => i >= 0) : [];
  if (S.logHit >= hits.length) S.logHit = 0;
  const re = q ? new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi') : null;
  const hl = (x, i) => { if (!re) return esc(x); let n = 0; const isCur = hits[S.logHit] === i; return esc(x).replace(new RegExp(esc(q).replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi'), (m) => `<mark${isCur && n++ === 0 ? ' class="cur"' : ''}>${m}</mark>`); };
  const live = !!lg.live;
  pane.innerHTML = `<div class="log${S.full ? ' full' : ''}"><div class="lt"><h3>${sic(ss)}<span>${esc(title)}</span></h3>
    <label class="field" id="lqf">${ico('search')}<input id="lq" type="search" placeholder="Search this log" value="${esc(S.logQ)}" aria-label="Search this log"></label>${q ? `<span class="hits">${hits.length ? S.logHit + 1 + ' of ' + hits.length : 'no match'}</span><button type="button" class="btn sm ghost icon" id="hp" aria-label="Previous match">${ico('up')}</button><button type="button" class="btn sm ghost icon" id="hn" aria-label="Next match">${ico('down')}</button>` : ''}
    <div class="seg"><button type="button" data-lf="all" aria-pressed="${S.logFilter === 'all'}">All</button><button type="button" data-lf="err" aria-pressed="${S.logFilter === 'err'}">Errors and notes<span class="n">${nErr}</span></button></div>
    <label class="tog"><input type="checkbox" id="wrap" ${S.wrap ? 'checked' : ''}> Wrap</label>${live ? `<label class="tog"><input type="checkbox" id="fol" ${S.follow ? 'checked' : ''}> Follow</label>` : ''}
    <button type="button" class="btn ghost icon" id="lcopy" title="Copy the log" aria-label="Copy the log">${ico('copy')}</button><button type="button" class="btn ghost icon" id="ldl" title="Download the log" aria-label="Download the log">${ico('dl')}</button><button type="button" class="btn ghost icon" id="lfull" title="Full screen" aria-label="Full screen">${ico('full')}</button></div>
    <div class="lines${S.wrap ? '' : ' nowrap'}" id="lines" tabindex="0">${shown.map((l, i) => `<div class="ln ${l[2]} ${S.pin === l[0] ? 'pin' : ''}" id="L${l[0]}"><a class="g" href="#/run/${encodeURIComponent(id)}?L=${l[0]}" data-l="${l[0]}" title="Link to line ${l[0]}">${l[0]}</a><span>${hl(l[1], i)}</span></div>`).join('') || '<div class="empty pad-log">This log is empty so far.</div>'}</div>
    <div class="lf"><span>${lg.total} line${lg.total === 1 ? '' : 's'}${lg.cut ? ', earlier lines cut' : ''}${live ? ' · streaming' : ''}</span><span class="ml-a muted">Click a line number to link to it</span></div></div>`;
  const ln = $('#lines');
  const cur = ln.querySelectorAll('mark.cur')[0];
  if (live && S.follow) ln.scrollTop = ln.scrollHeight;
  else if (cur) cur.scrollIntoView({ block: 'center' });
  else if (S.pin && $('#L' + S.pin)) ln.scrollTop = Math.max(0, $('#L' + S.pin).offsetTop - 60);
  else ln.scrollTop = S.lineTop || 0;
  ln.onscroll = () => { S.lineTop = ln.scrollTop; if (live && S.follow && ln.scrollHeight - ln.scrollTop - ln.clientHeight > 40) { S.follow = false; const f = $('#fol'); if (f) f.checked = false; } };
  const lq = $('#lq');
  lq.oninput = () => { const pos = lq.selectionStart; S.logQ = lq.value; S.logHit = 0; S.lineTop = 0; paintLog(); const n = $('#lq'); n.focus(); n.setSelectionRange(pos, pos); };
  lq.onkeydown = (e) => { if (e.key !== 'Enter' || !hits.length) return; e.preventDefault(); S.logHit = (S.logHit + (e.shiftKey ? -1 : 1) + hits.length) % hits.length; paintLog(); $('#lq').focus(); };
  const hop = (d) => { if (!hits.length) return; S.logHit = (S.logHit + d + hits.length) % hits.length; paintLog(); };
  if ($('#hp')) { $('#hp').onclick = () => hop(-1); $('#hn').onclick = () => hop(1); }
  $$('[data-lf]', pane).forEach((b) => { b.onclick = () => { S.logFilter = b.dataset.lf; S.lineTop = 0; paintLog(); }; });
  $('#wrap').onchange = (e) => { S.wrap = e.target.checked; paintLog(); };
  if ($('#fol')) $('#fol').onchange = (e) => { S.follow = e.target.checked; paintLog(); };
  const text = () => all.map((l) => l[1]).join('\n');
  $('#lcopy').onclick = () => navigator.clipboard.writeText(text()).then(() => toast('Log copied.'), () => toast('Could not copy.'));
  $('#ldl').onclick = () => { const a = document.createElement('a'); const u = URL.createObjectURL(new Blob([text()], { type: 'text/plain' })); a.href = u; a.download = `${r.id}-${jk}-step${idx}.log`; a.click(); setTimeout(() => URL.revokeObjectURL(u), 1000); };
  $('#lfull').onclick = () => { S.full = !S.full; paintLog(); };
  $$('.ln .g', pane).forEach((a) => { a.onclick = (e) => { e.preventDefault(); S.pin = +a.dataset.l; setQuery({ L: S.pin }); const u = location.href; navigator.clipboard.writeText(u).then(() => toast(`Link to line ${S.pin} copied.`), () => {}); paintLog(); }; });
}
function gotoError() {
  const id = route().id; const ff = firstFailed(jobsOf(id)); if (!ff.job || !ff.step) return;
  const lg = logOf(id, ff.job.key, ff.step.index); if (!lg) return;
  const i = lg.lines.findIndex((l) => ERR_RE.test(l.x)); if (i < 0) return;
  S.step = ff.job.key + ':' + ff.step.index; S.pin = lg.from + i + 1; S.logFilter = 'all'; S.logQ = ''; setQuery({ L: S.pin }); render();
  const p = $('#logpane'); if (p) p.scrollIntoView({ behavior: 'smooth', block: 'start' });
}
function nextFailedJob() {
  const id = route().id; const fj = jobsOf(id).filter((j) => norm(j.status) === 'failed'); if (fj.length < 2) return;
  const cur = splitStep(S.step || ':0')[0]; const i = fj.findIndex((j) => j.key === cur); const nj = fj[(i + 1) % fj.length];
  const fs = failedStepOf(nj) || (nj.steps || [])[0]; S.step = nj.key + ':' + (fs ? fs.index : 1); S.open[nj.key] = true; S.logQ = ''; S.pin = null; render();
}
function bindRun() {
  paintLog();
  $$('.jhead').forEach((b) => { b.onclick = () => { const y = scrollY; const j = jobsOf(route().id).find((x) => x.key === b.dataset.j); const cur = S.open[b.dataset.j] ?? (j ? norm(j.status) !== 'passed' : true); S.open[b.dataset.j] = !cur; render(); scrollTo(0, y); }; });
  $$('.fold').forEach((b) => { b.onclick = () => { const y = scrollY; S.fold[b.dataset.fold] = false; render(); scrollTo(0, y); }; });
  $$('.step[data-s]').forEach((li) => {
    const pick = () => { const y = scrollY; S.step = li.dataset.s; S.logQ = ''; S.pin = null; S.lineTop = 0; setQuery({ L: '' }); render(); scrollTo(0, y); };
    li.onclick = pick; li.onkeydown = (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(); } };
  });
  if ($('#goerr')) $('#goerr').onclick = gotoError;
  if ($('#nextf')) $('#nextf').onclick = nextFailedJob;
  bindMenus();
}

// ---------- Releases ----------
const vst = (v) => (v === 'passed' ? st('passed', 'Verified') : v === 'failed' ? st('failed', 'Verify failed') : v === 'pending' ? st('running', 'Verifying') : st('unknown'));
function viewReleases() {
  const sn = D.snapshot;
  if (!sn) return `<div class="ph"><h1>Releases</h1></div><div class="empty">${D.snapshotErr ? `<b>Releases could not be read.</b> ${esc(D.snapshotErr)}` : '<b>Loading…</b> Reading the releases from GitHub and this PC.'}</div>`;
  const rs = sn.releases || []; const feeds = sn.feeds || [];
  const sel = route().q.get('v') || S.rel || (rs[0] && rs[0].version); const cur = rs.find((x) => x.version === sel) || rs[0];
  const names = P().steps && P().steps.length ? P().steps : LegacyCore.STEPS;
  const stepsOf = (x) => (x.steps || []).map((s) => ({ ...s, t: s.at ? Date.parse(s.at) : NaN }));
  const lane = (name) => { const f = feeds.find((x) => x.channel === name); const rel0 = f && rs.find((x) => x.version === f.version);
    if (f && f.status === 'ok') return `<div class="lane"><div class="ch">${esc(name)} channel${f.paused ? ' <span class="chip warn">paused</span>' : ''}</div><div class="v">${esc(f.version)}</div><div class="row2">${vst(rel0 ? rel0.verify : 'unknown')} <span class="muted">published ${when(f.published)}</span> <span class="chip ok">Feed OK</span></div>
      <div class="btns">${name === 'rc' ? laterBtn('btn sm', ico('promote'), 'How to promote to stable', 'Promoting runs in PowerShell today.', `data-dlg="promote" data-v="${esc(f.version)}"`) : ''}${laterBtn('btn sm', '', 'How to withdraw', 'Withdrawing runs in PowerShell today.', `data-dlg="withdraw" data-v="${esc(f.version)}"`)}</div></div>`;
    return `<div class="lane"><div class="ch">${esc(name)} channel</div><div class="v muted">unknown</div><div class="row2"><span class="chip warn">Feed not reachable from this PC</span> So which version ${esc(name)} users have is unknown here.</div><div class="btns"><button type="button" class="btn sm" data-check>Check again</button></div></div>`; };
  let detail = '';
  if (cur) {
    const ss = stepsOf(cur); const ok = ss.filter((x) => isFinite(x.t));
    const tot = ok.length > 1 ? ok[ok.length - 1].t - ok[0].t : 0;
    const durs = ss.map((x, i) => (i && isFinite(x.t) && isFinite(ss[i - 1].t) ? Math.max(0, x.t - ss[i - 1].t) : 0)); const maxd = Math.max(0, ...durs);
    const wf = tot > 0 ? `<div class="wf" role="img" aria-label="Time spent in each step">${ss.slice(1).map((x, i) => `<i class="${durs[i + 1] === maxd && maxd > 0 ? 'slow' : ''}" data-flex="${Math.max(durs[i + 1], tot / 120).toFixed(0)}" title="${esc(x.name)}: ${esc(dur(durs[i + 1]))}"></i>`).join('')}</div>
      <div class="wfl">${ss.map((x, i) => `<div>${sic(x.state === 'done' ? 'passed' : x.state === 'failed' ? 'failed' : x.state === 'running' ? 'running' : 'waiting')} <b>${esc(x.name)}</b> <span>${i && durs[i] ? esc(dur(durs[i])) : isFinite(x.t) ? esc(new Date(x.t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })) : ''}</span></div>`).join('')}</div><p class="muted fs-xs mt8">Bar width is time spent in each step. The darkest part is the slowest step.</p>`
      : '<div class="empty">No step record on this PC. This release was made before step recording started, or on another PC. Its GitHub page still has the build log.</div>';
    detail = `<section class="panel rel-d" aria-live="polite"><div class="relh"><h2 class="mono fs-xl">${esc(cur.version)}</h2>${vst(cur.verify)}<span class="chip">${esc(cur.channel || 'rc')}</span>${cur.withdrawn ? '<span class="chip bad">Withdrawn</span>' : ''}</div>
      <p class="muted mt4">Published ${when(cur.published)}${tot > 0 ? `, the whole release took <b class="strong">${esc(dur(tot))}</b>` : ''}.</p>${wf}
      <dl class="facts"><dt>Tag</dt><dd class="mono">${esc(cur.tag || '–')}</dd><dt>Channel</dt><dd>${esc(cur.channel || 'rc')}</dd><dt>On GitHub</dt><dd>${cur.onGitHub ? 'Yes' : 'No'}</dd><dt>On the update feed</dt><dd>${cur.onFeed ? 'Yes' : feeds.some((f) => f.status === 'ok') ? 'No' : 'Unknown'}</dd><dt>Published</dt><dd>${when(cur.published)}</dd><dt>Withdrawn</dt><dd>${cur.withdrawn ? 'Yes' : 'No'}</dd></dl>
      <div class="btnrow">${LegacyCore.link(cur.url, 'Release on GitHub') ? `<a class="btn" href="${esc(cur.url)}" target="_blank" rel="noopener noreferrer">${ico('out')} Release on GitHub</a>` : ''}${LegacyCore.link(cur.runUrl, 'x') ? `<a class="btn" href="${esc(cur.runUrl)}" target="_blank" rel="noopener noreferrer">${ico('out')} Build log on GitHub</a>` : ''}</div></section>`;
  }
  const row = (x) => `<tr class="row" tabindex="0" data-v="${esc(x.version)}" aria-selected="${cur && x.version === cur.version}"><td><a class="rl mono title" href="#/releases?v=${encodeURIComponent(x.version)}">${esc(x.version)}</a> <span class="muted">${esc(x.channel || '')}</span>${x.withdrawn ? ' <span class="chip bad">Withdrawn</span>' : ''}</td>
    <td class="hide-s"><span class="pips">${names.map((n) => { const s2 = (x.steps || []).find((y) => y.name === n); return `<i class="${s2 ? (s2.state === 'done' ? 'done' : s2.state === 'failed' ? 'failed' : '') : ''}" title="${esc(n)}${s2 ? ': ' + esc(s2.state) : ''}"></i>`; }).join('')}</span></td>
    <td>${vst(x.verify)}</td><td class="num hide-s">${(() => { const t = stepsOf(x).filter((y) => isFinite(y.t)); return t.length > 1 ? esc(dur(t[t.length - 1].t - t[0].t)) : '–'; })()}</td><td class="num">${when(x.published)}</td></tr>`;
  const withSteps = rs.filter((x) => (x.steps || []).length).length;
  return `<div class="ph"><h1>Releases</h1><p>Each release runs ${names.length} steps, from ${esc(names[0])} to ${esc(names[names.length - 1])}. Today releases are still built on GitHub; this page reads them.</p></div>
  ${Fresh.noteHtml('releases', Date.now())}
  ${sn.github && sn.github.ok === false ? `<p class="empty"><b>GitHub could not be reached.</b> ${esc(sn.github.detail || '')}</p>` : ''}
  <div class="chan">${lane('rc')}<div class="arrow" aria-hidden="true">→</div>${lane('stable')}</div>
  <div class="split"><section><div class="sh"><h2>All releases</h2><span class="muted">${rs.length} on GitHub, ${withSteps} with step records on this PC</span></div>
    ${rs.length ? `<table class="tbl"><thead><tr><th>Version</th><th class="hide-s">Steps</th><th>Verify</th><th class="num hide-s">Took</th><th class="num">Published</th></tr></thead><tbody>${rs.map(row).join('')}</tbody></table>` : '<p class="empty">No release found yet.</p>'}</section>${detail}</div>
  <div id="detail"></div>`;
}
function bindReleases() {
  const sn = D.snapshot; if (!sn) return;
  $$('tr.row[data-v]').forEach((tr) => {
    const a = tr.querySelector('a.rl'); if (a) a.onclick = (e) => e.preventDefault();
    const pick = () => { S.rel = tr.dataset.v; setQuery({ v: tr.dataset.v }); render(); };
    tr.onclick = pick; tr.onkeydown = (e) => { if (e.key === 'Enter') pick(); };
  });
  $$('[data-dlg]').forEach((b) => { b.onclick = () => dialog(b.dataset.dlg, b.dataset.v); });
  const rs = sn.releases || []; const sel = route().q.get('v') || S.rel || (rs[0] && rs[0].version);
  Legacy.renderDetail(rs.find((x) => x.version === sel) || rs[0] || null, { releases: rs, snap: sn });
}
function dialog(kind, v) {
  const promote = kind === 'promote';
  const cmd = promote ? 'pwsh tools/release/Release.ps1 -Channel stable' : `pwsh tools/release/Set-Control.ps1 -Channel rc -KeyFile <your key file> -Block ${v}`;
  const pts = promote
    ? [`It builds the newest master commit that has green checks. That may be a newer commit than ${v}; it only takes the version number from the rc.`, 'You type the signing passphrase in that PowerShell window. This page never sees the key.', 'It waits for the checks, signs, publishes on GitHub and the stable feed, then verifies on a clean GitHub machine.', 'If verify fails, the version is withdrawn on its own.']
    : [`It writes a signed control file that blocks ${v}. The feed still lists the version; PCs that have not installed it are not offered it.`, 'The control file lasts at most 90 days.', 'You type the signing passphrase in that PowerShell window.'];
  $('#layer').innerHTML = `<div class="scrim" id="scrim"><div class="dlg" role="dialog" aria-modal="true" aria-labelledby="dlgt"><header><h2 id="dlgt">${promote ? 'Promote to stable' : 'Withdraw ' + esc(v)}</h2><p class="muted fs-xs mt4"><span class="chip">Later</span> A button here that runs it is not planned yet. Today you run it in PowerShell on this PC.</p></header>
    <div class="body"><p class="muted m0">Only one release action runs at a time; the others wait for the release lock.</p><ul>${pts.map((p) => `<li>${esc(p)}</li>`).join('')}</ul><div class="cmd"><code>${esc(cmd)}</code><button type="button" class="btn sm" data-copy="${esc(cmd)}">${ico('copy')} Copy</button></div></div>
    <footer><button type="button" class="btn primary" id="dlgc">Close</button></footer></div></div>`;
  const close = () => { $('#layer').innerHTML = ''; };
  $('#dlgc').onclick = close; $('#scrim').onclick = (e) => { if (e.target.id === 'scrim') close(); };
  $$('#layer [data-copy]').forEach((b) => { b.onclick = () => navigator.clipboard.writeText(b.dataset.copy).then(() => toast('Command copied.'), () => toast('Could not copy.')); });
  $('#dlgc').focus();
}

// ---------- Machines ----------
function viewMachines() {
  const m = D.machines; const o = D.overview;
  if (!o && !m) return D.err ? noStore('Machines could not be read.') : loadingHtml;
  if (!m || (m.show === false && !(m.machines || []).length)) return `<div class="ph"><h1>Machines</h1></div><div class="empty"><b>This project has no machine records.</b> Start <span class="mono">unravis-ci serve</span> on a PC and it shows up here.</div>`;
  const ms = m.machines || []; const ag = (o && o.agents) || [];
  const now = Date.now(); const hourAgo = now - 3600e3;
  const mine = D.runs.filter((r) => r.agent === 'local' && ts(r.start) > hourAgo);
  const waits = D.runs.map(waitMs).filter((x) => x != null).sort((a, b) => a - b);
  const pc = (k) => (waits.length ? dur(waits[Math.min(waits.length - 1, Math.floor(waits.length * k))]) : '–');
  const card = (x) => {
    const busy = (x.jobs || []).length; const slots = x.slots || 0; const lh = x.lastHeard ? ts(x.lastHeard) : 0; const fresh = lh && now - lh < 120e3;
    const a = x.self ? ag[0] : null;
    return `<div class="mach"><div><div class="mach-top">${ico('pc')}<b class="mono">${esc(x.id)}</b>${x.self ? '<span class="chip">This PC</span>' : ''}</div>
      <div class="mt6">${!fresh ? '<span class="chip warn">Quiet</span>' : busy ? '<span class="chip run">Running</span>' : '<span class="chip ok">Idle</span>'} <span class="muted">${busy ? busy + ' job' + (busy === 1 ? '' : 's') + ' running' : 'no jobs'} · heard from ${when(x.lastHeard)}</span></div></div>
      <div><div class="lbl">Slots</div><div class="val"><span class="slots">${Array.from({ length: slots }, (_, i) => `<i class="${i < busy ? 'busy' : ''}"></i>`).join('')}</span> ${Math.max(0, slots - busy)} of ${slots} free</div></div>
      <div><div class="lbl">Last hour</div><div class="val">${x.self ? `${mine.length} run${mine.length === 1 ? '' : 's'}, ${mine.filter((r) => norm(r.status) === 'failed').length} failed` : '<span class="muted">Not reported by other PCs</span>'}</div></div>
      <div><div class="lbl">Wait for a slot</div><div class="val">${pc(0.5)} median</div><div class="muted fs-xs">slowest 1 in 20 waited ${pc(0.95)} or more, over the runs loaded</div></div>
      <div><div class="lbl">Results to GitHub</div><div class="val">${a ? `${a.posted || 0} sent${a.postFailed ? ', ' + a.postFailed + ' failed' : ', none failed'}${a.postWaiting ? ', ' + a.postWaiting + ' waiting' : ''}` : '<span class="muted">Not reported</span>'}</div><div class="muted fs-xs">Shadow mode: reported as <span class="mono">local/*</span>, does not gate merges</div></div>
      <div>${x.self ? laterBtn('btn sm', ico('pause'), 'Pause', WHY.pause) : '<span class="muted fs-xs">Other PCs cannot be paused from here</span>'}</div></div>`;
  };
  const cmd = `unravis-ci serve --repo ${REPO()}`;
  return `<div class="ph"><h1>Machines</h1><p>A machine is a PC that runs checks. Each slot runs one job at a time. A machine that has not reported for about 3 minutes shows as Quiet.</p></div>
  ${ms.length ? `<div class="panel">${ms.map(card).join('')}</div>` : '<div class="empty"><b>No machine has reported yet.</b></div>'}
  <section class="sec"><div class="sh"><h2>Add a machine</h2></div><p class="muted lede">A second machine lets two pull requests check at the same time, and keeps checks going when this PC is off. On that machine, run the agent for this project:</p>
    <div class="cmd"><code>${esc(cmd)}</code><button type="button" class="btn sm" data-copy="${esc(cmd)}">${ico('copy')} Copy</button></div>
    <p class="muted m0">It shows up here within about a minute. It needs a GitHub token that can write repository contents, or it stops at start.</p></section>`;
}

// ---------- keys ----------
function keysDialog() {
  const rows = [['g', 'then o Overview, r Runs, l Releases, m Machines'], ['/', 'Search the runs, or the log on a run page'], ['e', 'Go to the first error of a failed run'], ['f', 'Next failed job'], ['Enter', 'In log search: next match (Shift for previous)'], ['Esc', 'Close a menu, dialog or full-screen log']];
  $('#layer').innerHTML = `<div class="scrim" id="scrim"><div class="dlg" role="dialog" aria-modal="true" aria-labelledby="dlgt"><header><h2 id="dlgt">Keyboard shortcuts</h2></header><div class="body"><div class="keys">${rows.map(([k, t]) => `<kbd>${esc(k)}</kbd><span>${esc(t)}</span>`).join('')}</div></div><footer><button type="button" class="btn primary" id="dlgc">Close</button></footer></div></div>`;
  const close = () => { $('#layer').innerHTML = ''; };
  $('#dlgc').onclick = close; $('#scrim').onclick = (e) => { if (e.target.id === 'scrim') close(); }; $('#dlgc').focus();
}

// ---------- render ----------
const TITLES = { overview: 'Overview', runs: 'Runs', run: 'Run', releases: 'Releases', machines: 'Machines' };
function render() {
  loadDis(); paintChrome();
  const R = route(); const main = $('#main'); const w = worst();
  const loading = !D.loaded;
  let html;
  if (loading) html = loadingHtml;
  else if (D.projectsErr && !D.projects.length) html = `<div class="empty empty-top"><b>The project list could not be read.</b> ${esc(D.projectsErr)}</div>`;
  else if (R.name === 'overview') html = viewOverview();
  else if (R.name === 'runs') html = viewRuns();
  else if (R.name === 'run' && R.id) html = viewRun(R.id);
  else if (R.name === 'releases') html = viewReleases();
  else if (R.name === 'machines') html = viewMachines();
  else html = '<div class="empty"><b>No such page.</b> <a href="#/overview">Back to the overview</a>.</div>';
  // Older data is named at the top of the page; the Releases page puts its own note under its heading (viewReleases).
  const banner = (R.name === 'releases' ? '' : Fresh.noteHtml(R.name, Date.now())) || (D.err && D.loaded && !D.at ?`<div class="empty"><b>The last refresh failed.</b> ${esc(D.err)}</div>` : '');
  main.innerHTML = banner + html;
  $$('[data-check]', main).forEach((b) => { b.onclick = () => { toast('Checking again…'); Data.tick(true); }; });
  $$('table.tbl th', main).forEach((th) => th.setAttribute('scope', 'col'));
  $$('table.tbl', main).forEach((t) => { if (t.parentElement.className !== 'tw') { const d = document.createElement('div'); d.className = 'tw'; t.replaceWith(d); d.appendChild(t); } });
  applyDyn(main);
  if (!loading) {
    if (R.name === 'overview') bindRows();
    else if (R.name === 'runs') bindRuns();
    else if (R.name === 'run' && R.id) bindRun();
    else if (R.name === 'releases') bindReleases();
    else if (R.name === 'machines') bindMenus();
  }
  document.title = ({ failed: 'Failing · ', running: 'Running · ', passed: 'Passing · ' }[w]) + (TITLES[R.name] || 'Homestretch') + ' · ' + (P().name || 'Homestretch') + ' · Homestretch';
}

// A run that is open keeps refreshing its jobs and the log of the step on screen while it is live.
async function refreshRun() {
  const R = route(); if (R.name !== 'run' || !R.id) return false;
  let ch = false;
  try { const c = await Data.loadRun(R.id); S.runErr = ''; ch = !!c; } catch (e) { if (!detOf(R.id)) { S.runErr = e.message; ch = true; } }
  if (S.step && detOf(R.id)) {
    const [jk, idx] = splitStep(S.step); const lg = logOf(R.id, jk, idx); const run = detOf(R.id).run;
    if (!lg || lg.live || norm(run.status) === 'running') { try { if (await Data.loadLog(R.id, jk, idx)) { if (!ch) { paintLog(); } } } catch (e) { /* keep the last log */ } }
  }
  return ch;
}

function start() {
  if (!location.hash) history.replaceState(null, '', '#/overview');
  lastPath = location.hash.split('?')[0];
  addEventListener('hashchange', () => {
    $('#layer').innerHTML = ''; S.menu = null;
    const p = location.hash.split('?')[0];
    if (p !== lastPath) { S.step = null; S.logQ = ''; S.logHit = 0; S.full = false; S.pin = null; S.lineTop = 0; S.runErr = ''; scrollTo(0, 0); }
    lastPath = p;
    render();
    if (route().name === 'run') refreshRun().then((c) => { if (c) render(); });
  });
  document.addEventListener('click', (e) => {
    if (S.menu && !e.target.closest('.sw') && !e.target.closest('.rowmenu')) { S.menu = null; paintChrome(); $$('.rowmenu .menu').forEach((x) => { x.hidden = true; }); $$('.kebab').forEach((x) => x.setAttribute('aria-expanded', 'false')); }
  });
  $('#keysb').onclick = keysDialog;
  let gPending = 0;
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      if ($('#layer').children.length) { $('#layer').innerHTML = ''; return; }
      if (S.full) { S.full = false; render(); return; }
      if (S.menu) { S.menu = null; paintChrome(); $$('.rowmenu .menu').forEach((x) => { x.hidden = true; }); }
      return;
    }
    if (['INPUT', 'SELECT', 'TEXTAREA'].includes(document.activeElement && document.activeElement.tagName) || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === '/') { const t = $('#lq') || $('#q'); if (t) { e.preventDefault(); t.focus(); } return; }
    if (e.key === '?') { keysDialog(); return; }
    if (e.key === 'g') { gPending = Date.now(); return; }
    if (gPending && Date.now() - gPending < 1200) { const t = { o: 'overview', r: 'runs', l: 'releases', m: 'machines' }[e.key]; gPending = 0; if (t) { go('/' + t); return; } }
    if (route().name === 'run') { if (e.key === 'e') gotoError(); else if (e.key === 'f') nextFailedJob(); }
  });
  render();
  Data.start(async (kind) => {
    if (kind === 'tick') { paintChrome(); const c = await refreshRun(); if (c) render(); return; }
    const ae = document.activeElement; const typing = ae && ['INPUT', 'SELECT'].includes(ae.tagName) && ae.id !== 'lq';
    const c = await refreshRun();
    if (!typing || c) render();
    else paintChrome();
  });
  if (route().name === 'run') refreshRun().then((c) => { if (c) render(); });
}
start();
