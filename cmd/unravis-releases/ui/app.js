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
const S = { job: null, step: null, detailOpen: false, jobFilter: 'all', showPassed: false, readRunQuery: true, revealRunDetail: false, causeLogs: null,
  logQ: '', logHit: 0, logFilter: 'all', wrap: true, follow: true, full: false, rel: null, menu: null, pin: null, lineTop: 0, runErr: '' };

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
  const cur = isRun(R) ? 'runs' : R.name; const oc = Ops.counts();
  $('#tabs').innerHTML = [['overview', 'Overview', ''], ['runs', 'Runs', nR], ['queue', 'Queue', oc.queue], ['releases', 'Releases', nRel], ['machines', 'Machines', nM], ['holds', 'Holds', oc.holds], ['activity', 'Activity', oc.activity]]
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
  ${Ops.holdLine()}
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
    ${Ops.queueCard()}
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
const splitStep = (k) => { const i = k.lastIndexOf(':'); return [k.slice(0, i), +k.slice(i + 1)]; };
const isRun = (R) => (R.name === 'run' || R.name === 'ghrun') && !!R.id;
const detOf = (R) => {
  const runs = D[R.name];
  return runs && Object.hasOwn(runs, R.id) ? runs[R.id] : null;
};
const jobsOf = (R) => detOf(R)?.jobs || [];
const logOf = (R, jk, idx) => D.logs[Data.logKey(detOf(R)?.run.id || R.id, jk, idx)];
const failedStepOf = (j) => (j.steps || []).find((s) => norm(s.status) === 'failed');
const jobName = (j) => j.label || j.id || j.key;
const stepKey = (j, s) => `${j.key}:${s.index}`;
const githubURL = (url) => typeof url === 'string' && url.startsWith('https://github.com/') ? url : '';
const runStates = { passed: 'Passed', failed: 'Failed', running: 'Running', waiting: 'Not started',
  skipped: 'Skipped', stopped: 'Stopped', finished: 'Finished', unknown: 'Not recorded' };
const runState = (record) => Object.hasOwn(runStates, norm(record.status)) ? norm(record.status) : 'unknown';
const runWord = (record) => record.word && record.status !== 'waiting' ? record.word : runStates[runState(record)];
const statusIcon = (state) => state === 'finished' ? 'clock' : state;
const runStatus = (record) => {
  const state = runState(record);
  return state === 'finished' ? `<span class="st">${ico('clock')}${esc(runWord(record))}</span>` : st(state, runWord(record));
};
const ERR_RE = /NEW FAILURE|^--- FAIL|panic:|::error::/;
const errorInLog = new WeakMap();
function firstError(log) {
  if (!log?.lines) return null;
  if (!errorInLog.has(log)) {
    const i = log.lines.findIndex((l) => ERR_RE.test(l.x));
    errorInLog.set(log, i < 0 ? null : { line: Number(log.from) + i + 1, text: log.lines[i].x });
  }
  return errorInLog.get(log);
}

function runColumns(jobs, unlisted) {
  const columns = [[]];
  const timed = [], waiting = [];
  for (const job of jobs) {
    const start = ts(job.start), end = ts(job.end);
    if (Number.isFinite(start)) timed.push({ job, start, end, depth: 0 });
    else if (runState(job) === 'waiting') waiting.push(job);
    else columns[0].push(job);
  }
  timed.sort((a, b) => a.start - b.start);
  const ended = timed.filter((j) => j.end >= j.start).sort((a, b) => a.end - b.end || a.start - b.start);
  let finished = 0, deepest = -1;
  for (const entry of timed) {
    // Equal starts share a depth, even if one job has zero recorded duration.
    while (finished < ended.length && ended[finished].end <= entry.start && ended[finished].start < entry.start) {
      deepest = Math.max(deepest, ended[finished++].depth);
    }
    entry.depth = deepest + 1;
    (columns[entry.depth] ||= []).push(entry.job);
  }
  const hasRun = timed.length || columns[0].some((j) => ['failed', 'passed', 'running', 'finished'].includes(runState(j)));
  const hasWaiting = !!(hasRun && (waiting.length || unlisted));
  if (hasWaiting) columns.push(waiting);
  else columns[0].push(...waiting);
  return { columns, hasWaiting };
}

function runHref(R, job, index, line) {
  const q = new URLSearchParams();
  if (job != null) q.set('job', job);
  if (index != null) q.set('step', index);
  if (line != null) q.set('L', line);
  return `#/${R.name}/${encodeURIComponent(R.id)}${q.size ? '?' + q : ''}`;
}

function initialRunStep(j, det) {
  const steps = j.steps || [];
  return failedStepOf(j) || steps.find((s) => runState(s) === 'running') ||
    steps.find((s) => s.hasLog) || steps[0] || (j.key === '_checkout' && det.checkout ? { index: 0 } : null);
}

function jobHtml(j) {
  const state = runState(j), selected = S.job === j.key;
  let active = null;
  const strip = (j.steps || []).map((s) => {
    const k = stepKey(j, s), status = runState(s);
    if ((status === 'failed' && active?.status !== 'failed') || (status === 'running' && !active)) active = s;
    const tip = `Step ${s.index} · ${s.name} · ${runWord(s)}${s.ms != null ? ' · ' + dur(s.ms) : ''}`;
    return `<span class="run-cell ${status}${S.step === k ? ' current' : ''}" data-strip="${esc(s.index)}" title="${esc(tip)}"></span>`;
  }).join('');
  return `<button class="run-node ${state}" type="button" id="node-${esc(j.key)}" data-job="${esc(j.key)}" data-job-state="${state}" aria-pressed="${selected}">
    <span class="run-node-head">${ico(statusIcon(state))}<strong>${esc(jobName(j))}</strong>${j.ms != null ? `<span class="mono">${esc(dur(j.ms))}</span>` : ''}</span>
    <span class="run-node-meta">${esc(runWord(j))}${active ? ` · Step ${esc(active.index)}` : ''}
      ${j.reason && ['skipped', 'waiting', 'stopped'].includes(state) ? `<span class="run-reason">${esc(j.reason)}</span>` : ''}</span>
    ${strip ? `<span class="run-strip" aria-hidden="true">${strip}</span>` : ''}</button>`;
}

function runSteps(det, j) {
  const steps = j?.steps || [];
  const max = steps.reduce((n, s) => Math.max(n, s.ms || 0), 1);
  const canHide = j && runState(j) === 'failed' && steps.some((s) => runState(s) === 'passed');
  const hide = canHide && !S.showPassed;
  let hidden = 0;
  const rows = [];
  const flush = () => {
    if (hidden) rows.push(`<li class="run-step-note">${hidden} passed step${hidden === 1 ? '' : 's'} hidden</li>`);
    hidden = 0;
  };
  for (const s of steps) {
    const k = stepKey(j, s), state = runState(s);
    if (hide && state === 'passed' && k !== S.step) { hidden++; continue; }
    flush();
    rows.push(`<li><button type="button" class="run-step ${state}" id="step-${esc(k)}" data-step="${esc(s.index)}" ${S.step === k ? 'aria-current="true"' : ''}>
      <span class="mono">${esc(s.index)}</span>${ico(statusIcon(state))}<span class="run-step-name">${esc(s.name)}<small>${esc(runWord(s))}${state === 'skipped' && s.err ? ` · ${esc(s.err)}` : ''}</small>
      ${s.ms > 0 ? `<span class="tb" aria-hidden="true"><i data-w="${Math.max(2, Math.round(100 * s.ms / max))}"></i></span>` : ''}</span>
      ${s.ms != null ? `<span class="mono">${esc(dur(s.ms))}</span>` : ''}</button></li>`);
  }
  flush();
  const checkout = det.checkout ? `<button type="button" class="run-checkout link" id="run-checkout">Checkout log</button>` : '';
  return `<div class="panel run-steps" aria-label="Steps">
    <div class="run-step-head"><h3>${esc(S.step === '_checkout:0' && !j ? 'Getting the code' : j ? jobName(j) : 'Steps')}</h3>
    ${j ? `<div>${runStatus(j)}${j.ms != null ? ` · ${esc(dur(j.ms))}` : ''}</div>` : ''}
    ${j?.reason && ['skipped', 'waiting', 'stopped'].includes(runState(j)) ? `<div class="run-reason">${esc(j.reason)}</div>` : ''}
    ${canHide ? `<button type="button" class="link" id="run-show-passed">${hide ? 'Show passed steps' : 'Hide passed steps'}</button>` : ''}</div>
    ${rows.length ? `<ol>${rows.join('')}</ol>` : '<p class="run-step-note">No steps recorded.</p>'}${checkout}</div>`;
}

function viewRun(R) {
  const det = detOf(R);
  const gh = R.name === 'ghrun';
  const crumb = '<a href="#/runs">Runs</a>';
  if (!det) return `<div class="crumb">${crumb}</div>${S.runErr ? `<div class="empty"><b>The run could not be read.</b> ${esc(S.runErr)}</div>` : loadingHtml}`;
  const r = det.run;
  const state = runState(r), jobs = jobsOf(R);
  const byKey = new Map(jobs.map((j) => [j.key, j]));
  const failed = [], counts = Object.fromEntries(Object.keys(runStates).map((s) => [s, 0]));
  let running;
  for (const j of jobs) {
    const s = runState(j);
    counts[s]++;
    if (s === 'failed') failed.push(j);
    if (s === 'running' && !running) running = j;
  }
  if (!byKey.has(S.job) && !(S.job === '_checkout' && det.checkout)) {
    const job = failed[0] || running || jobs[0];
    S.job = job?.key ?? (det.checkout ? '_checkout' : null);
    S.step = null;
  }
  if (!S.step) {
    const job = byKey.get(S.job);
    const step = job && initialRunStep(job, det);
    S.step = step ? stepKey(job, step) : S.job === '_checkout' && det.checkout ? '_checkout:0' : null;
    if (step && runState(step) === 'failed') S.logFilter = 'err';
  }
  if (S.readRunQuery) {
    const job = byKey.get(R.q.get('job'));
    const index = R.q.get('step');
    const step = job && ((job.steps || []).find((s) => index != null && s.index === +index) || initialRunStep(job, det));
    if (job) {
      S.job = job.key;
      S.step = step ? stepKey(job, step) : null;
    } else if (R.q.get('job') === '_checkout' && det.checkout) {
      S.job = '_checkout'; S.step = '_checkout:0';
    }
    const line = Number(R.q.get('L'));
    S.pin = Number.isSafeInteger(line) && line > 0 ? line : null;
    if (S.pin) S.logFilter = 'all';
    if (S.pin || R.q.has('job')) { S.detailOpen = true; S.revealRunDetail = true; }
    S.readRunQuery = false;
  }
  const j = byKey.get(S.job);
  const selectedIndex = S.step ? splitStep(S.step)[1] : null;
  const selectedStep = j?.steps?.find((s) => s.index === selectedIndex);
  const title = r.release ? `Release ${r.release}` : [wfName(r.workflow), r.pr ? `PR ${r.pr}` : r.branch].filter(Boolean).join(' · ') || 'Run';
  const sha = r.headSha || r.sha, url = githubURL(r.url);
  const meta = [
    r.branch && `<span class="run-pill">${ico('branch')}${esc(r.branch)}</span>`,
    sha && `<span class="run-pill mono" title="${esc(sha)}">${ico('commit')}${esc(short(sha))}</span>`,
    r.start && `<span class="run-pill">${ico('clock')}<time datetime="${esc(r.start)}">${esc(exact(r.start))}</time></span>`,
    r.agent && `<span class="run-pill">${ico('pc')}${esc(r.agent)}</span>`,
    r.author && `<span class="run-pill">${ico('user')}${esc(r.author)}</span>`,
  ].filter(Boolean).join('');
  const primary = ['failed', 'running', 'waiting', 'passed'];
  const unlisted = det.live ? Math.max(0, (det.planned || 0) - jobs.length) : 0;
  const total = jobs.length + unlisted;
  counts.waiting += unlisted;
  const extra = Object.keys(runStates).filter((s) => !primary.includes(s) && counts[s]);
  const states = [...primary, ...extra];
  const totals = states.map((s) => `${counts[s]} ${runStates[s].toLowerCase()}`);
  const tiles = primary.map((s) => `<div class="run-tile ${s}">${ico(statusIcon(s))}<strong>${counts[s]}</strong><span>${runStates[s].toLowerCase()}</span></div>`).join('');
  const { columns, hasWaiting } = runColumns(jobs, unlisted);
  const filters = `<div class="run-filters" role="radiogroup" aria-label="Filter jobs">${[['all', 'All'], ...primary.map((s) => [s, runStates[s]])].map(([s, label]) =>
    `<label><input type="radio" name="run-filter" id="run-filter-${s}" value="${s}" ${S.jobFilter === s ? 'checked' : ''}><span>${label}</span></label>`).join('')}</div>`;
  const board = total ? `<div class="run-board" id="run-board" tabindex="0" aria-label="Jobs in run order">${columns.map((col, i) => {
    const label = hasWaiting && i === columns.length - 1 ? 'Waiting' : i === 0 ? 'Ran first' : i === columns.length - 1 ? 'Last' : 'Then';
    const notStarted = unlisted && i === (hasWaiting ? columns.length - 1 : 0) ? `<p class="run-unlisted">${unlisted} ${unlisted === 1 ? 'job' : 'jobs'} not started</p>` : '';
    return `<div class="run-column${col.length >= 5 && columns.length <= 3 ? ' wide' : ''}">
      <h3><span>${i + 1}</span> ${label}</h3><div class="run-stack">${col.map(jobHtml).join('')}${notStarted}<p class="run-column-empty" hidden>No jobs match.</p></div></div>`;
  }).join('')}</div>` : '<p class="run-step-note">No jobs recorded.</p>';
  const actions = `${failed.length ? `<button type="button" class="btn" id="goerr">${gh ? 'Open failed job' : 'Go to error'} <kbd>e</kbd></button>` : ''}
    ${failed.length > 1 ? '<button type="button" class="btn" id="nextf">Next failed job <kbd>f</kbd></button>' : ''}`;
  const cards = failed.map((job) => {
    const step = failedStepOf(job);
    const lg = !gh && (step?.hasLog ? logOf(R, job.key, step.index) : job.key === '_checkout' && det.checkout ? logOf(R, job.key, 0) : null);
    const line = firstError(lg)?.text || (step?.err || '').split(/\r?\n/, 1)[0];
    return `<article class="run-cause">
      <div class="run-cause-head">${sic('failed')}<div><h3>${esc(jobName(job))}</h3>${step ? `<p>${esc(step.name)}</p>` : ''}</div></div>
      <p class="run-cause-line mono" id="cause-${esc(job.key)}"${line ? '' : ' hidden'}>${esc(line)}</p>
      <a class="btn sm" data-cause="${esc(job.key)}" href="${esc(runHref(R, job.key, step?.index))}">See the log</a></article>`;
  }).join('');
  const rate = !gh && state === 'failed' ? stepRate(r) : null;
  return `<div class="run-page"><div class="crumb">${crumb} / ${esc(title)}</div>
    <header class="rh"><h1>${esc(title)}</h1><div class="meta">${meta}</div>
      ${gh ? `<p class="run-source">Read from GitHub${url ? ` · <a href="${esc(url)}" target="_blank" rel="noopener noreferrer">Open the run on GitHub</a>` : ''}</p>` : ''}</header>
    <section class="run-hero ${state}" aria-label="Run result">
      <div class="run-verdict">${ico(statusIcon(state))}<div><h2>${esc(runWord(r))}</h2>
        <p>${total} ${total === 1 ? 'job' : 'jobs'}${r.ms != null ? ` · ${esc(dur(r.ms))}` : ''}</p>
        ${r.reason && (['skipped', 'waiting', 'stopped'].includes(state) || (state === 'failed' && !failed.length)) ? `<p>${esc(r.reason)}</p>` : ''}</div></div>
      <div class="run-tiles">${tiles}</div>
      ${total ? `<div class="run-bar" role="img" aria-label="${esc(totals.join(', '))}">${states.filter((s) => counts[s]).map((s) =>
        `<span class="${s}" data-flex="${counts[s]}"></span>`).join('')}</div>` : ''}
      ${extra.length ? `<p class="run-other-counts">${esc(totals.slice(primary.length).join(' · '))}</p>` : ''}
      ${actions.trim() ? `<div class="run-actions">${actions}</div>` : ''}</section>
    ${cards ? `<section class="run-causes" aria-label="What failed">${cards}</section>` : ''}
    ${rate?.of > 1 ? `<p class="run-rate">This step failed in ${rate.same} of the last ${rate.of} finished runs of this workflow on this PC.</p>` : ''}
    <section class="run-flow panel" aria-labelledby="run-order"><div class="run-flow-head"><h2 id="run-order">The run, in order</h2>${filters}</div>${board}</section>
    <details class="run-detail panel" id="run-detail"${S.detailOpen ? ' open' : ''}>
      <summary id="run-toggle">${ico('chev', 'ch')}<strong>Steps and log</strong>
        ${j ? `<span class="run-selected">${runStatus(j)} <span>${esc(jobName(j))}${selectedStep ? ` · Step ${esc(selectedStep.index)}` : ''}</span></span>` : ''}
        <span class="run-detail-toggle" aria-hidden="true"></span></summary>
      <div class="rbody">${runSteps(det, j)}<section id="logpane" aria-label="Log"></section></div>
    </details></div>`;
}

const pendingLogs = new Map();
function ensureLog(R, jk, idx) {
  const epoch = Data.epoch;
  const id = detOf(R)?.run.id || R.id;
  const logKey = Data.logKey(id, jk, idx), k = `${epoch}|${logKey}`;
  if (pendingLogs.has(k)) return pendingLogs.get(k);
  const fetch = Data.loadLog(id, jk, idx).catch((e) => {
    if (Data.epoch !== epoch || D.logs[logKey]?.lines || D.logs[logKey]?.err === e.message) return false;
    D.logs[logKey] = { err: e.message };
    return true;
  }).finally(() => pendingLogs.delete(k));
  pendingLogs.set(k, fetch);
  return fetch;
}

function syncCauseLogs(R) {
  if (R.name !== 'run') return;
  const epoch = Data.epoch, det = detOf(R);
  const work = S.causeLogs ||= { queue: [], seen: new Set(), active: 0 };
  const current = () => Data.epoch === epoch && sameRun(R) && S.causeLogs === work;
  for (const job of det.jobs || []) {
    if (runState(job) !== 'failed') continue;
    const step = failedStepOf(job);
    const idx = step?.hasLog ? step.index : job.key === '_checkout' && det.checkout ? 0 : null;
    if (idx == null || logOf(R, job.key, idx)) continue;
    const key = `${det.run.id}|${job.key}|${idx}`;
    if (!work.seen.has(key)) { work.seen.add(key); work.queue.push([job.key, idx]); }
  }
  const read = async () => {
    work.active++;
    try {
      while (work.queue.length && current()) {
        const [key, idx] = work.queue.shift();
        await ensureLog(R, key, idx);
        if (!current()) return;
        const error = firstError(logOf(R, key, idx));
        const line = document.getElementById('cause-' + key);
        if (error && line) { line.textContent = error.text; line.hidden = false; }
      }
    } finally {
      work.active--;
    }
  };
  const workers = Math.min(3 - work.active, work.queue.length);
  for (let i = 0; i < workers; i++) void read();
}

function paintLog() {
  const R = route(), pane = $('#logpane'), det = detOf(R);
  if (!pane || !det || !S.detailOpen) return;
  const [jk, idx] = S.step ? splitStep(S.step) : [S.job, null];
  const job = (det.jobs || []).find((j) => j.key === jk);
  const step = job?.steps?.find((s) => s.index === idx);
  const checkout = jk === '_checkout' && idx === 0 && det.checkout;
  const title = checkout ? 'Checkout log' : step ? `${jobName(job)} / ${step.index}. ${step.name}` : job ? jobName(job) : 'Log';
  const head = `<h3>${step ? runStatus(step) : ''}<span>${esc(title)}</span></h3>`;
  const empty = (html) => { pane.innerHTML = `<div class="log"><div class="lt">${head}</div><div class="empty pad-log">${html}</div></div>`; };
  const gh = R.name === 'ghrun';
  if (gh) {
    const url = githubURL(job?.url);
    if (job) empty(`GitHub keeps this log.${url ? ` <a href="${esc(url)}" target="_blank" rel="noopener noreferrer">Open the job on GitHub.</a>` : ''}`);
    else empty('No job selected.');
    return;
  }
  if (!checkout && !step?.hasLog) {
    empty(step ? step.err ? esc(step.err) : 'No log recorded for this step.' : job ? 'No step selected.' : 'No job selected.');
    return;
  }

  const lg = logOf(R, jk, idx);
  if (!lg || (lg.live && !det.live)) {
    if (!lg) empty('Loading the log…');
    const epoch = Data.epoch, key = S.step;
    ensureLog(R, jk, idx).then((changed) => {
      if (changed && Data.epoch === epoch && sameRun(R) && S.step === key && $('#logpane') === pane) paintLog();
    });
    if (!lg) return;
  }
  if (lg.err) { empty(`<b>The log could not be read.</b> ${esc(lg.err)}`); return; }
  const active = document.activeElement;
  const focused = pane.contains(active) ? active.id : '';
  const caret = focused === 'lq' ? [active.selectionStart, active.selectionEnd] : null;
  const cls = (x) => (/NEW FAILURE|^--- FAIL|panic:|FAIL\t|::error::|exit code [1-9]/.test(x) ? 'err' : /::notice::|::warning::/.test(x) ? 'note' : /^\s+\|/.test(x) ? 'dim' : '');
  const all = (lg.lines || []).map((l, i) => [Number(lg.from) + i + 1, l.x, cls(l.x)]);
  const nErr = all.filter((l) => l[2] === 'err' || l[2] === 'note').length;
  const shown = S.logFilter === 'err' ? all.filter((l) => l[2] === 'err' || l[2] === 'note' || l[0] === S.pin) : all;
  const q = S.logQ.trim().toLowerCase();
  const hits = q ? shown.map((l, i) => (l[1].toLowerCase().includes(q) ? i : -1)).filter((i) => i >= 0) : [];
  if (S.logHit >= hits.length) S.logHit = 0;
  const re = q ? new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'gi') : null;
  const hl = (x, i) => {
    if (!re) return esc(x);
    let start = 0, html = '', match;
    while ((match = re.exec(x))) {
      html += esc(x.slice(start, match.index)) + `<mark${hits[S.logHit] === i && start === 0 ? ' class="cur"' : ''}>${esc(match[0])}</mark>`;
      start = re.lastIndex;
    }
    return html + esc(x.slice(start));
  };
  const live = !!lg.live;
  const error = firstError(lg);
  pane.innerHTML = `<div class="log${S.full ? ' full' : ''}"><div class="lt">${head}
    <div class="seg" role="group" aria-label="Log view">
      <button type="button" id="run-log-errors" data-lf="err" aria-pressed="${S.logFilter === 'err'}" title="Show errors and notes">Errors <span class="n">${nErr}</span></button>
      <button type="button" id="run-log-all" data-lf="all" aria-pressed="${S.logFilter === 'all'}">Whole log</button></div>
    <label class="field" id="lqf">${ico('search')}<input id="lq" type="search" placeholder="Search this log" value="${esc(S.logQ)}" aria-label="Search this log"></label>${q ? `<span class="hits" role="status">${hits.length ? S.logHit + 1 + ' of ' + hits.length : 'No match'}</span><button type="button" class="btn sm ghost icon" id="hp" aria-label="Previous match">${ico('up')}</button><button type="button" class="btn sm ghost icon" id="hn" aria-label="Next match">${ico('down')}</button>` : ''}
    <label class="tog"><input type="checkbox" id="wrap" ${S.wrap ? 'checked' : ''}> Wrap</label>${live ? `<label class="tog"><input type="checkbox" id="fol" ${S.follow ? 'checked' : ''}> Follow</label>` : ''}
    <button type="button" class="btn ghost icon" id="lcopy" title="Copy the log" aria-label="Copy the log">${ico('copy')}</button><button type="button" class="btn ghost icon" id="ldl" title="Download the log" aria-label="Download the log">${ico('dl')}</button><button type="button" class="btn ghost icon" id="lfull" title="${S.full ? 'Exit full screen' : 'Full screen'}" aria-label="${S.full ? 'Exit full screen' : 'Full screen'}">${ico('full')}</button></div>
    ${error ? `<div class="run-log-error">${sic('failed')}<span>First error on line ${error.line}</span><button class="btn sm" type="button" id="run-log-jump">Go to line</button></div>` : ''}
    <div class="lines${S.wrap ? '' : ' nowrap'}" id="lines" tabindex="0">${shown.map((l, i) => `<div class="ln ${l[2]} ${S.pin === l[0] ? 'pin' : ''}" id="L${l[0]}"><a class="g" href="${esc(runHref(R, jk, idx, l[0]))}" data-l="${l[0]}" title="Link to line ${l[0]}">${l[0]}</a><span>${hl(l[1], i)}</span></div>`).join('') || `<div class="empty pad-log">${all.length ? 'No errors or notes in this log.' : 'This log is empty so far.'}</div>`}</div>
    <div class="lf"><span>${esc(lg.total)} line${lg.total === 1 ? '' : 's'}${lg.cut ? ' · Earlier lines omitted' : ''}${live ? ' · Live' : ''}</span><span>Click a line number to copy its link</span></div></div>`;
  const ln = $('#lines');
  const cur = $('mark.cur', ln);
  const pin = S.pin && $('#L' + S.pin, ln);
  if (cur) ln.scrollTop += cur.getBoundingClientRect().top - ln.getBoundingClientRect().top - ln.clientHeight / 2;
  else if (pin) ln.scrollTop += pin.getBoundingClientRect().top - ln.getBoundingClientRect().top - 60;
  else if (live && S.follow) ln.scrollTop = ln.scrollHeight;
  else ln.scrollTop = S.lineTop;
  ln.onscroll = () => {
    S.lineTop = ln.scrollTop;
    if (live && S.follow && !cur && !pin && ln.scrollHeight - ln.scrollTop - ln.clientHeight > 40) {
      S.follow = false;
      $('#fol').checked = false;
    }
  };

  const lq = $('#lq');
  lq.oninput = () => { S.logQ = lq.value; S.logHit = 0; S.lineTop = 0; paintLog(); };
  const hop = (d) => {
    if (!hits.length) return;
    S.logHit = (S.logHit + d + hits.length) % hits.length;
    paintLog();
  };
  lq.onkeydown = (e) => { if (e.key === 'Enter') { e.preventDefault(); hop(e.shiftKey ? -1 : 1); } };
  if (q) { $('#hp').onclick = () => hop(-1); $('#hn').onclick = () => hop(1); }
  $$('[data-lf]', pane).forEach((b) => { b.onclick = () => { S.logFilter = b.dataset.lf; S.logHit = 0; S.lineTop = 0; paintLog(); }; });
  $('#wrap').onchange = (e) => { S.wrap = e.target.checked; paintLog(); };
  if (live) $('#fol').onchange = (e) => {
    S.follow = e.target.checked;
    if (S.follow) { S.pin = null; S.logQ = ''; setQuery({ L: '', job: '', step: '' }); }
    paintLog();
  };
  const text = () => all.map((l) => l[1]).join('\n');
  $('#lcopy').onclick = () => navigator.clipboard.writeText(text()).then(() => toast('Log copied.'), () => toast('Could not copy.'));
  $('#ldl').onclick = () => { const a = document.createElement('a'); const u = URL.createObjectURL(new Blob([text()], { type: 'text/plain' })); a.href = u; a.download = `${det.run.id}-${jk}-step${idx}.log`; a.click(); setTimeout(() => URL.revokeObjectURL(u), 1000); };
  $('#lfull').onclick = () => { S.full = !S.full; paintLog(); };
  const pinLine = (line) => {
    S.pin = line;
    setQuery({ L: line, job: jk, step: idx });
    paintLog();
  };
  if (error) $('#run-log-jump').onclick = () => { S.logQ = ''; S.logFilter = 'all'; pinLine(error.line); };
  ln.onclick = (e) => {
    const a = e.target.closest('a[data-l]');
    if (!a || !plainClick(e)) return;
    e.preventDefault();
    pinLine(+a.dataset.l);
    navigator.clipboard.writeText(location.href).then(() => toast(`Link to line ${S.pin} copied.`), () => toast('Could not copy.'));
  };
  if (focused) {
    const replacement = document.getElementById(focused);
    replacement?.focus({ preventScroll: true });
    if (replacement && caret) replacement.setSelectionRange(...caret);
  }
}

const sameRun = (R) => { const now = route(); return now.name === R.name && now.id === R.id; };
const plainClick = (e) => !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey;
function pickRunStep(R, job, index, { reveal = false, open = true, scroll = true } = {}) {
  const det = detOf(R);
  const j = jobsOf(R).find((x) => x.key === job);
  if (!j && !(job === '_checkout' && det.checkout)) return;
  const step = j && (index == null ? initialRunStep(j, det) : j.steps?.find((s) => s.index === index));
  if (S.job !== job) S.showPassed = false;
  S.job = job;
  S.step = step ? stepKey(j, step) : job === '_checkout' && det.checkout ? '_checkout:0' : null;
  S.detailOpen = open;
  S.pin = null;
  S.logQ = '';
  S.logHit = 0;
  S.lineTop = 0;
  S.logFilter = step && runState(step) === 'failed' ? 'err' : 'all';
  setQuery({ L: '', job: '', step: '' });
  render();
  const d = $('#run-detail');
  if (open && scroll && (reveal || d.getBoundingClientRect().top > innerHeight * 0.65)) {
    d.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  }
}

async function gotoError(jobKey = S.job) {
  const R = route(), det = detOf(R);
  const failed = jobsOf(R).filter((j) => runState(j) === 'failed');
  const job = failed.find((j) => j.key === jobKey) || failed[0];
  if (!job) return;
  const step = failedStepOf(job);
  pickRunStep(R, job.key, step?.index, { reveal: true });
  const key = S.step, epoch = Data.epoch;
  const idx = step?.hasLog ? step.index : job.key === '_checkout' && det.checkout ? 0 : null;
  if (R.name !== 'run' || idx == null) return;
  if (!logOf(R, job.key, idx)) await ensureLog(R, job.key, idx);
  if (Data.epoch !== epoch || !sameRun(R) || S.step !== key) return;
  const error = firstError(logOf(R, job.key, idx));
  if (!error) return;
  S.pin = error.line;
  S.logFilter = 'all';
  setQuery({ L: S.pin, job: job.key, step: idx });
  paintLog();
}

function nextFailedJob() {
  const R = route();
  const failed = jobsOf(R).filter((j) => runState(j) === 'failed');
  if (failed.length < 2) return;
  const i = failed.findIndex((j) => j.key === S.job);
  if (S.jobFilter !== 'all' && S.jobFilter !== 'failed') S.jobFilter = 'failed';
  pickRunStep(R, failed[(i + 1) % failed.length].key, null, { reveal: true });
}

function bindRun(R) {
  const details = $('#run-detail'), det = detOf(R);
  if (!details) return;
  details.ontoggle = () => {
    if (!details.isConnected || S.detailOpen === details.open) return;
    S.detailOpen = details.open;
    if (details.open) paintLog();
    else S.full = false;
  };
  if (!det) return;
  const filterBoard = () => {
    $$('.run-stack').forEach((stack) => {
      let visible = false;
      $$('[data-job]', stack).forEach((b) => {
        b.hidden = S.jobFilter !== 'all' && S.jobFilter !== b.dataset.jobState;
        if (!b.hidden) visible = true;
      });
      const unlisted = $('.run-unlisted', stack);
      if (unlisted) {
        unlisted.hidden = !['all', 'waiting'].includes(S.jobFilter);
        visible ||= !unlisted.hidden;
      }
      $('.run-column-empty', stack).hidden = visible;
    });
  };
  filterBoard();
  syncCauseLogs(R);
  paintLog();
  if (S.revealRunDetail) {
    S.revealRunDetail = false;
    details.scrollIntoView({ block: 'start' });
  }
  const board = $('#run-board');
  if (board) board.onclick = (e) => {
    const b = e.target.closest('[data-job]');
    if (!b) return;
    const cell = e.target.closest('[data-strip]');
    pickRunStep(R, b.dataset.job, cell ? +cell.dataset.strip : null);
    if (e.detail === 0) $('#run-toggle').focus();
  };
  $('.run-filters').onchange = (e) => {
    S.jobFilter = e.target.value;
    filterBoard();
    const first = $('[data-job]:not([hidden])');
    if (first && !jobsOf(R).some((j) => j.key === S.job && (S.jobFilter === 'all' || S.jobFilter === runState(j)))) {
      pickRunStep(R, first.dataset.job, null, { open: S.detailOpen, scroll: false });
    }
  };
  $$('.run-step').forEach((b) => { b.onclick = () => pickRunStep(R, S.job, +b.dataset.step, { scroll: false }); });
  $$('.run-cause [data-cause]').forEach((a) => {
    a.onclick = (e) => {
      if (!plainClick(e)) return;
      e.preventDefault();
      gotoError(a.dataset.cause);
    };
  });
  if ($('#run-checkout')) $('#run-checkout').onclick = () => pickRunStep(R, '_checkout', 0, { scroll: false });
  if ($('#run-show-passed')) $('#run-show-passed').onclick = () => { S.showPassed = !S.showPassed; render(); };
  if ($('#goerr')) $('#goerr').onclick = () => gotoError();
  if ($('#nextf')) $('#nextf').onclick = nextFailedJob;
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
      <div>${Ops.machineCell(x)}</div></div>`;
  };
  const cmd = `unravis-ci serve --repo ${REPO()}`;
  return `<div class="ph"><h1>Machines</h1><p>A machine is a PC that runs checks. Each slot runs one job at a time. A machine that has not reported for about 3 minutes shows as Quiet.</p></div>
  ${ms.length ? `<div class="panel">${ms.map(card).join('')}</div>` : '<div class="empty"><b>No machine has reported yet.</b></div>'}
  <section class="sec"><div class="sh"><h2>Add a machine</h2></div><p class="muted lede">A second machine lets two pull requests check at the same time, and keeps checks going when this PC is off. On that machine, run the agent for this project:</p>
    <div class="cmd"><code>${esc(cmd)}</code><button type="button" class="btn sm" data-copy="${esc(cmd)}">${ico('copy')} Copy</button></div>
    <p class="muted m0">It shows up here within about a minute. It needs a GitHub token that can write repository contents, or it stops at start.</p></section>
  ${Ops.machinesExtra()}`;
}

// ---------- keys ----------
function keysDialog() {
  const rows = [['g', 'then o Overview, r Runs, q Queue, l Releases, m Machines, h Holds, a Activity'], ['/', 'Search the runs, or the log on a run page'], ['e', 'Go to the first error of a failed run'], ['f', 'Next failed job'], ['Enter', 'In log search: next match (Shift for previous)'], ['Esc', 'Close a menu, dialog or full-screen log']];
  $('#layer').innerHTML = `<div class="scrim" id="scrim"><div class="dlg" role="dialog" aria-modal="true" aria-labelledby="dlgt"><header><h2 id="dlgt">Keyboard shortcuts</h2></header><div class="body"><div class="keys">${rows.map(([k, t]) => `<kbd>${esc(k)}</kbd><span>${esc(t)}</span>`).join('')}</div></div><footer><button type="button" class="btn primary" id="dlgc">Close</button></footer></div></div>`;
  const close = () => { $('#layer').innerHTML = ''; };
  $('#dlgc').onclick = close; $('#scrim').onclick = (e) => { if (e.target.id === 'scrim') close(); }; $('#dlgc').focus();
}

// ---------- render ----------
const TITLES = { overview: 'Overview', runs: 'Runs', run: 'Run', ghrun: 'GitHub run', releases: 'Releases', machines: 'Machines', queue: 'Queue', holds: 'Holds', activity: 'Activity' };
function render() {
  const R = route(); const main = $('#main'); const w = worst();
  const run = isRun(R);
  const active = document.activeElement;
  const focused = run && main.contains(active) ? active.id : '';
  const caret = focused === 'lq' ? [active.selectionStart, active.selectionEnd] : null;
  const boardScroll = run ? $('#run-board')?.scrollLeft : null;
  const stepScroll = run ? $('.run-steps')?.scrollTop : null;
  loadDis(); paintChrome();
  const loading = !D.loaded;
  let html;
  if (loading) html = loadingHtml;
  else if (D.projectsErr && !D.projects.length) html = `<div class="empty empty-top"><b>The project list could not be read.</b> ${esc(D.projectsErr)}</div>`;
  else if (R.name === 'overview') html = viewOverview();
  else if (R.name === 'runs') html = viewRuns();
  else if (run) html = viewRun(R);
  else if (R.name === 'releases') html = viewReleases();
  else if (R.name === 'machines') html = viewMachines();
  else if (R.name === 'queue') html = Ops.viewQueue();
  else if (R.name === 'holds') html = Ops.viewHolds();
  else if (R.name === 'activity') html = Ops.viewActivity();
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
    else if (run) bindRun(R);
    else if (R.name === 'releases') bindReleases();
    else if (R.name === 'machines') { bindMenus(); Ops.bind(); }
    else if (['queue', 'holds', 'activity'].includes(R.name)) Ops.bind();
  }
  if (boardScroll && $('#run-board')) $('#run-board').scrollLeft = boardScroll;
  if (stepScroll && $('.run-steps')) $('.run-steps').scrollTop = stepScroll;
  if (focused) {
    const replacement = document.getElementById(focused);
    replacement?.focus({ preventScroll: true });
    if (replacement && caret) replacement.setSelectionRange(...caret);
  }
  document.title = ({ failed: 'Failing · ', running: 'Running · ', passed: 'Passing · ' }[w]) + (TITLES[R.name] || 'Homestretch') + ' · ' + (P().name || 'Homestretch') + ' · Homestretch';
}

// A run that is open keeps refreshing its jobs and the log of the step on screen while it is live.
const pendingRuns = new Map();
async function refreshRun(force = false) {
  const R = route();
  if (!D.loaded || !isRun(R)) return false;
  const prior = detOf(R);
  if (!force && prior && !prior.live) return false;
  const epoch = Data.epoch, key = `${epoch}|${R.name}|${R.id}`;
  const current = () => Data.epoch === epoch && sameRun(R);
  if (pendingRuns.has(key)) return pendingRuns.get(key);
  const load = async () => {
    let changed = false;
    try {
      const read = R.name === 'ghrun' ? Data.loadGhRun : Data.loadRun;
      changed = !!(await read(R.id));
      if (!current()) return false;
      if (S.runErr) { S.runErr = ''; changed = true; }
    } catch (e) {
      if (!current()) return false;
      if (!detOf(R) && S.runErr !== e.message) { S.runErr = e.message; changed = true; }
    }
    if (R.name === 'run' && S.step && (prior?.live || detOf(R)?.live)) {
      const selected = S.step, [jk, idx] = splitStep(selected);
      const det = detOf(R), job = det?.jobs?.find((j) => j.key === jk);
      const hasLog = job?.steps?.some((s) => s.index === idx && s.hasLog) || (jk === '_checkout' && det?.checkout);
      if (hasLog && (S.detailOpen || logOf(R, jk, idx)?.live)) {
        const logChanged = await ensureLog(R, jk, idx);
        if (!current()) return false;
        if (logChanged && !changed && S.step === selected) paintLog();
      }
    }
    return changed;
  };
  const pending = load().finally(() => pendingRuns.delete(key));
  pendingRuns.set(key, pending);
  return pending;
}

function start() {
  if (!location.hash) history.replaceState(null, '', '#/overview');
  lastPath = location.hash.split('?')[0];
  addEventListener('hashchange', () => {
    $('#layer').innerHTML = ''; S.menu = null;
    const p = location.hash.split('?')[0];
    if (p !== lastPath) {
      Object.assign(S, { job: null, step: null, detailOpen: false, jobFilter: 'all', showPassed: false, revealRunDetail: false, causeLogs: null,
        logQ: '', logHit: 0, logFilter: 'all', full: false, pin: null, lineTop: 0, runErr: '' });
      scrollTo(0, 0);
      Ops.reset();
    }
    S.readRunQuery = true;
    lastPath = p;
    render();
    if (isRun(route())) refreshRun(true).then((c) => { if (c) render(); });
  });
  document.addEventListener('click', (e) => {
    if (S.menu && !e.target.closest('.sw') && !e.target.closest('.rowmenu')) { S.menu = null; paintChrome(); $$('.rowmenu .menu').forEach((x) => { x.hidden = true; }); $$('.kebab').forEach((x) => x.setAttribute('aria-expanded', 'false')); }
  });
  $('#keysb').onclick = keysDialog;
  let gPending = 0;
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      if ($('#layer').children.length) { $('#layer').innerHTML = ''; return; }
      if (Ops.escape()) return;
      if (S.full) { S.full = false; paintLog(); return; }
      if (S.menu) { S.menu = null; paintChrome(); $$('.rowmenu .menu').forEach((x) => { x.hidden = true; }); }
      return;
    }
    if (['INPUT', 'SELECT', 'TEXTAREA'].includes(document.activeElement && document.activeElement.tagName) || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === '/') {
      if (isRun(route()) && $('#run-detail') && !S.detailOpen) { S.detailOpen = true; $('#run-detail').open = true; paintLog(); }
      const t = $('#lq') || $('#q'); if (t) { e.preventDefault(); t.focus(); } return;
    }
    if (e.key === '?') { keysDialog(); return; }
    if (e.key === 'g') { gPending = Date.now(); return; }
    if (gPending && Date.now() - gPending < 1200) { const t = { o: 'overview', r: 'runs', q: 'queue', l: 'releases', m: 'machines', h: 'holds', a: 'activity' }[e.key]; gPending = 0; if (t) { go('/' + t); return; } }
    if (isRun(route())) { if (e.key === 'e') gotoError(); else if (e.key === 'f') nextFailedJob(); }
  });
  render();
  Data.start(async (kind) => {
    if (kind === 'tick') { paintChrome(); const c = await refreshRun(); if (c) render(); return; }
    const ae = document.activeElement; const typing = ae && ['INPUT', 'SELECT'].includes(ae.tagName) && ae.id !== 'lq';
    const c = await refreshRun();
    if (!typing || c) render();
    else paintChrome();
  });
  if (isRun(route())) refreshRun(true).then((c) => { if (c) render(); });
}
start();
