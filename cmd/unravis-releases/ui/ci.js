'use strict';
// The CI side of the page: the Overview and Runs tabs, the project switcher and the hash routes (#/overview, #/runs,
// #/runs/<id>, #/releases/<version>). Everything is read from the program's own /api/ routes (read only) and put on the page
// with textContent, never as HTML. The helpers el, when, ago, dur, clock, chip, getJSON, viewer and mountViewer come from
// app.js and detail.js. The Releases tab is the page those two files draw; this file only shows or hides it.

// One entry for each status word: the sign, the colour family and what the word means (shown as a tooltip).
// The word itself always carries the meaning; the colour and the sign only help.
const CI_STATUS = {
  passed: ['✓', 'ok', 'Every job finished without an error.'],
  failed: ['✗', 'bad', 'A job or step ended with an error.'],
  running: ['▶', 'run', 'Being checked right now.'],
  waiting: ['·', 'mut', 'In the queue. An agent starts it as soon as it is free.'],
  stopped: ['■', 'warn', 'It was cancelled, or replaced by a newer run, before it finished.'],
  'not-recorded': ['?', 'mut', 'It finished, but this PC has no saved record of how it went.'],
  skipped: ['–', 'mut', 'This step did not run.'],
  finished: ['✓', 'mut', 'This step ended. The verdict is known when the whole run ends.'],
};
const CI_STATUS_FILTER = [['', 'Any status'], ['passed', 'Passed'], ['failed', 'Failed'], ['running', 'Running'], ['waiting', 'Waiting'], ['stopped', 'Stopped'], ['not-recorded', 'Not recorded here']];
const CI_TABS = [['overview', 'Overview', 'The latest result on each branch, what is running, and how the checks on this PC compare with GitHub.'], ['runs', 'Runs', 'Every check run, newest first. Open one to see its jobs, steps and logs.'], ['machines', 'Machines', 'The PCs that check this repository, what each is doing and when it was last heard from.'], ['releases', 'Releases', 'Every release, its channel and the release steps.']];
// The words of a PC, the same ones the runs use: Running, Idle, Quiet, Stopped.
const CI_MACHINE = {
  Running: ['▶', 'run', 'Checking something right now.'],
  Idle: ['·', 'mut', 'Awake and ready. Nothing to check at the moment.'],
  Quiet: ['?', 'warn', 'Has not been heard from for a few minutes. It may be asleep, offline or switched off.'],
  Stopped: ['■', 'warn', 'Said it was stopping. It takes no work and holds no claims.'],
};
const CI_FAST = 4000, CI_SLOW = 30000;

const ci = { projects: [], pid: '', ready: false, tab: '', epoch: 0, busyEp: -1, timer: 0, sig: '', view: null, fast: false, failed: false, ok: false, open: new Set(), opened: '', rs: null, machines: {}, mprobe: 0 };
const first = () => (ci.projects[0] || {}).id || '';
const proj = () => ci.projects.find((p) => p.id === ci.pid) || ci.projects[0] || null;

// ---------- small helpers ----------
function safeDecode(s) { try { return decodeURIComponent(s); } catch (e) { return s; } }
function parseHash() {
  let h = location.hash.replace(/^#\/?/, ''), q = '';
  const i = h.indexOf('?'); if (i >= 0) { q = h.slice(i + 1); h = h.slice(0, i); }
  const parts = h.split('/').filter(Boolean).map(safeDecode);
  return { tab: parts[0] || '', id: parts[1] || '', q: new URLSearchParams(q) };
}
// The link to a page. The first project is the default one, so it is left out of the link.
function hashFor(tab, id, params, pid) {
  const q = new URLSearchParams(); const p = pid === undefined ? ci.pid : pid;
  if (p && p !== first()) q.set('project', p);
  for (const [k, v] of Object.entries(params || {})) if (v !== '' && v != null) q.set(k, v);
  const s = q.toString();
  return '#/' + tab + (id ? '/' + encodeURIComponent(id) : '') + (s ? '?' + s : '');
}
function apiUrl(path, params) {
  const q = new URLSearchParams(); q.set('project', ci.pid);
  for (const [k, v] of Object.entries(params || {})) if (v !== '' && v != null) q.set(k, v);
  return path + '?' + q.toString();
}
function code(text) { return el('code', 'code', text); }
function para(cls, ...bits) { const p = el('p', cls); for (const b of bits) p.append(b); return p; }
function ms2s(ms) { return ms == null ? null : Math.round(ms / 1000); }
function took(ms) { return ms ? dur(ms2s(ms)).replace(/ /g, '\u00a0') : ''; } // a duration never breaks across two lines

function statusTag(key, word, extra) {
  const s = CI_STATUS[key] || ['?', 'mut', ''];
  const t = el('span', 'tag cis ' + s[1] + (extra ? ' ' + extra : ''));
  const g = el('span', 'g', s[0]); g.setAttribute('aria-hidden', 'true');
  t.append(g, el('span', 'w', word || key)); t.title = s[2];
  return t;
}
// What a run is about, in one line a first-time reader can follow.
function runTitle(r) {
  if (r.event === 'pull_request' && r.pr) return 'Pull request #' + r.pr + (r.branch ? ' · ' + r.branch + (r.base ? ' into ' + r.base : '') : '');
  if (r.event === 'push') return 'Push to ' + (r.branch || 'a branch');
  return r.workflow || r.event || 'Run';
}
function shaChip(r) { if (!r.sha) return null; const c = el('span', 'chip mono', r.sha.slice(0, 7)); c.title = 'Commit ' + r.sha; return c; }
function relChip(r) {
  if (!r.release) return null;
  const a = el('a', 'chip rel', 'Release ' + r.release); a.href = hashFor('releases', r.release);
  a.title = 'This commit is in release ' + r.release + '. Open that release.'; return a;
}
function runWhen(r) {
  if (r.status === 'waiting') return 'Queued ' + ago(r.start);
  if (r.status === 'running') return 'Started ' + ago(r.start) + (r.ms ? ' · ' + took(r.ms) + ' so far' : '');
  const bits = [r.start ? when(r.start) : '']; if (r.ms) bits.push(took(r.ms));
  return bits.filter(Boolean).join(' · ');
}
function emptyBox(title, ...lines) {
  const b = el('div', 'ci-empty'); b.append(el('p', 'nl-title', title)); for (const l of lines) b.append(l); return b;
}
function card(title, help) {
  const c = el('section', 'card'); c.append(el('h3', '', title)); if (help) c.append(el('p', 'help', help)); return c;
}
function sectionHead(text) { return el('h3', '', text); }
function focusHead() { const h = $('ci-main').querySelector('h2'); if (h) { h.tabIndex = -1; h.focus({ preventScroll: true }); } }

// ---------- the run row (Runs list, Overview) ----------
function runRow(r, opt) {
  const li = el('li', 'rrow'); li.dataset.id = r.id;
  const main = el('div', 'rmain');
  const t = el('a', 'rt', runTitle(r)); t.href = hashFor('runs', r.id);
  const sub = el('div', 'rsub');
  const bits = [r.workflow, r.author ? 'by ' + r.author : '', runWhen(r)].filter(Boolean);
  sub.textContent = bits.join(' · ');
  main.append(t, sub);
  if (r.reason && r.status !== 'passed') main.append(el('div', 'rwhy', r.reason));
  if (r.stale) main.append(el('div', 'rwhy', r.note || 'The agent that was running this has gone quiet.'));
  const chips = el('div', 'rchips'); for (const c of [shaChip(r), relChip(r)]) if (c) chips.append(c);
  li.append(statusTag(r.status, r.word), main, chips);
  li.addEventListener('click', (e) => { if (!e.target.closest('a, button')) location.hash = hashFor('runs', r.id); });
  return li;
}
function runList(list, cls) { const ul = el('ul', 'rlist' + (cls ? ' ' + cls : '')); for (const r of list) ul.append(runRow(r)); return ul; }

// ---------- the project record: what the Releases page reads instead of hard coded names ----------
function applyProject(p) {
  if (!p) return;
  STEPS = (p.steps && p.steps.length ? p.steps : STEPS).slice();
  CHANNELS = (p.channels && p.channels.length ? p.channels : CHANNELS).slice();
  const sel = $('proj-sel'); if (sel) sel.value = p.id;
  if (snap) render();
}

function buildSwitcher() {
  const box = $('proj'), sel = $('proj-sel'); sel.replaceChildren();
  for (const p of ci.projects) { const o = el('option', '', p.name || p.id); o.value = p.id; sel.append(o); }
  box.hidden = ci.projects.length < 2; // one project: nothing to switch
}
$('proj-sel').addEventListener('change', () => { location.hash = hashFor(ci.tab === 'releases' || ci.tab === 'runs' || ci.tab === 'overview' ? ci.tab : 'overview', '', {}, $('proj-sel').value); });

// ---------- routing ----------
function showSection(tab) {
  section = tab; ci.tab = tab;
  const rel = tab === 'releases';
  document.querySelector('.body').hidden = !rel; $('foot').hidden = !rel; $('ci-foot').hidden = rel; $('run').hidden = !rel; $('ci').hidden = rel;
  $('chan-rc').hidden = !rel; $('chan-stable').hidden = !rel || CHANNELS.length < 2;
  const p = proj();
  for (const [id] of CI_TABS) {
    const a = $('mt-' + id); a.hidden = (id === 'releases' && p && !p.releases) || (id === 'machines' && id !== tab && !machinesShown(p));
    a.href = hashFor(id); if (id === tab) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  }
}

// A link names a project this program does not have (a typo, or an old bookmark). Say so, and offer the real ones, instead of
// quietly showing a different project.
function unknownProject(asked, tab) {
  ci.epoch++; clearTimeout(ci.timer); ci.view = null; ci.sig = ''; ci.fast = false; ci.failed = false;
  ci.pid = first(); applyProject(proj());
  showSection('overview');
  for (const [id] of CI_TABS) { const a = $('mt-' + id); a.removeAttribute('aria-current'); }
  const m = main();
  const box = emptyBox('There is no project called "' + asked + '".');
  box.setAttribute('role', 'alert');
  box.append(para('nl-next', 'The link points to a project this program does not know. It may have been renamed or removed, or the link has a typing mistake. Pick one of the projects below.'));
  const ul = el('ul', 'lines');
  for (const p of ci.projects) { const li = el('li'); const a = el('a', '', p.name || p.id); a.href = hashFor(CI_TABS.some((t) => t[0] === tab) && !(tab === 'releases' && !p.releases) && tab !== 'machines' ? tab : 'overview', '', {}, p.id); li.append(a); ul.append(li); }
  box.append(ul);
  m.append(box);
  setFoot(true);
  $('refresh').classList.remove('primary');
}

// The Machines tab is shown when the project record asks for it or when this program has seen more than one PC. The answer
// comes from /api/machines; it is asked again now and then because a second PC can appear while the page is open.
function machinesShown(p) { return !!(p && (p.machines || ci.machines[p.id])); }
async function probeMachines(force) {
  const p = proj(); if (!p) return;
  const now = Date.now();
  if (!force && now - ci.mprobe < CI_SLOW) return;
  ci.mprobe = now;
  try {
    const m = await getJSON(apiUrl('/api/machines'));
    setMachinesShown(p.id, !!m.show);
  } catch (e) { /* the tab keeps its last state */ }
}
function setMachinesShown(pid, on) {
  if (!!ci.machines[pid] === on) return;
  ci.machines[pid] = on;
  const p = proj();
  if (p && p.id === pid) { const a = $('mt-machines'); a.hidden = !machinesShown(p) && ci.tab !== 'machines'; }
}

function route(initial) {
  if (!ci.ready) return;
  const r = parseHash();
  const asked = r.q.get('project') || '';
  if (asked && !ci.projects.some((p) => p.id === asked)) { unknownProject(asked, r.tab); return; }
  const pid = asked || first();
  const changed = pid !== ci.pid; ci.pid = pid;
  const p = proj();
  let tab = r.tab;
  if (!CI_TABS.some((t) => t[0] === tab)) tab = p && p.ciFound ? 'overview' : (p && p.releases ? 'releases' : 'overview');
  if (tab === 'releases' && p && !p.releases) tab = 'overview';
  if (changed) applyProject(p);
  if (!r.tab && !initial) { /* nothing to do */ }
  if (!r.tab) history.replaceState(null, '', hashFor(tab));
  showSection(tab);
  ci.epoch++; clearTimeout(ci.timer); ci.sig = ''; ci.view = null; ci.fast = false;
  if (tab === 'releases') {
    if (r.id) { selected = r.id; if (snap) { render(); const li = document.getElementById('r-' + r.id.replace(/[^A-Za-z0-9_.-]/g, '_')); if (li) li.scrollIntoView({ block: 'nearest' }); } }
    else if (snap) render();
    return;
  }
  probeMachines(true);
  if (tab === 'overview') showOverview();
  else if (tab === 'machines') showMachines();
  else if (r.id) showRun(r.id);
  else showRuns(r.q);
  if (!initial) focusHead();
}
window.addEventListener('hashchange', () => route(false));

// ---------- reading and drawing, one view at a time ----------
// A view is {fetch(): data, draw(data), fast(data): boolean}. The page reads again every few seconds while something is
// running or waiting, and every 30 seconds otherwise. Answers that arrive after the page moved on are dropped.
function ciSchedule() {
  clearTimeout(ci.timer);
  if (section === 'releases' || !ci.view) return;
  ci.timer = setTimeout(() => { if (document.hidden) ciSchedule(); else tick(false); }, ci.fast || ci.failed ? CI_FAST : CI_SLOW);
}
async function tick(force) {
  const view = ci.view, ep = ci.epoch;
  if (!view || ci.busyEp === ep) return;
  ci.busyEp = ep;
  try {
    const data = await view.fetch();
    if (ep !== ci.epoch) return;
    ci.failed = false; ci.ok = true;
    const s = JSON.stringify(data);
    if (force || s !== ci.sig) { ci.sig = s; view.draw(data); }
    ci.fast = !!view.fast(data);
    setFoot(true);
    probeMachines(false);
  } catch (e) {
    if (ep !== ci.epoch) return;
    ci.failed = true;
    if (view.fail) view.fail(e); // the first read failed: the page says why
    setFoot(false);
  } finally { if (ci.busyEp === ep) ci.busyEp = -1; if (ep === ci.epoch) ciSchedule(); }
}
function ciRefresh() { ci.sig = ''; tick(true); }
function setFoot(ok) {
  const f = $('ci-foot'); f.replaceChildren();
  const p = proj();
  f.append(el('span', '', 'Read only'), el('span', '', 'Runs are read from the CI store on this PC' + (p && p.name ? ' (' + p.name + ')' : '')));
  f.append(el('span', ok ? '' : 'bad', ok ? 'Updated ' + clock(new Date()) : 'Lost the connection to the program. Trying again...'));
}
document.addEventListener('visibilitychange', () => { if (!document.hidden && section !== 'releases') tick(false); });

// The browser says "Failed to fetch" when the program is gone; say it in words a first-time reader can act on.
function plainErr(e) {
  const m = String((e && e.message) || '').trim();
  return !m || /failed to fetch|networkerror|load failed/i.test(m) ? 'Could not reach the Releases program. It may have been closed. Start it again, then press Refresh.' : m;
}
function main() { const m = $('ci-main'); m.replaceChildren(); return m; }
function failBox(e, retry) {
  const m = main();
  const why = plainErr(e);
  m.append(para('note bad', /^could not/i.test(why) ? why : 'Could not read this: ' + why));
  const b = el('button', '', 'Try again'); b.type = 'button'; b.addEventListener('click', retry || ciRefresh); m.append(b);
}
// A store the page cannot find, or that has no runs: say why and what to do (the project rule for empty states).
function noStoreBox(p) {
  const m = main();
  if (p && !p.hasCi) {
    m.append(emptyBox('This project has no CI store set.', para('nl-next', 'Start the program with ', code('--ci-store <folder>'), ' (or add "ciStore" to the project in the projects file). Then press Refresh.')));
  } else {
    m.append(emptyBox('No CI runs found on this PC yet.', para('nl-next', 'The folder where ', code('unravis-ci'), ' keeps its runs does not exist. Start the agent with ', code('unravis-ci serve'), ', then press Refresh.')));
  }
  $('refresh').classList.add('primary');
}

// ---------- Overview ----------
function showOverview() {
  const p = proj();
  ci.view = {
    fetch: () => getJSON(apiUrl('/api/overview')),
    fast: (o) => !!(o.running.length || o.waiting.length),
    fail: (e) => { if (!ci.sig) failBox(e); },
    draw: (o) => drawOverview(o, p),
  };
  main().append(el('p', 'note', 'Reading...'));
  tick(true);
}

function drawOverview(o, p) {
  $('refresh').classList.remove('primary');
  if (!o.ciFound) { noStoreBox(p); return; }
  const m = main();
  m.append(el('h2', 'ctitle', (p && p.name ? p.name + ': ' : '') + 'Overview'));
  m.append(compareCard(o.shadow));
  const grid = el('div', 'cgrid'); m.append(grid);

  const live = card('Running and waiting', 'Runs the agent is working on now, and runs lined up behind them.');
  if (!o.running.length && !o.waiting.length) live.append(el('p', 'none', 'Nothing is running or waiting.'));
  if (o.running.length) { live.append(el('h4', '', 'Running (' + o.running.length + ')')); live.append(runList(o.running)); }
  if (o.waiting.length) { live.append(el('h4', '', 'Waiting (' + o.waiting.length + ')')); live.append(runList(o.waiting)); }
  grid.append(live);

  const br = card('Latest run on each branch', 'The newest finished or running check after a push to the branch.');
  if (!o.branches.length) br.append(el('p', 'none', 'No branches are set for this project.'));
  const bl = el('ul', 'rlist');
  for (const b of o.branches) {
    if (b.run) bl.append(runRow(b.run));
    else { const li = el('li', 'rrow'); li.append(statusTag('not-recorded', 'No run yet'), (() => { const d = el('div', 'rmain'); d.append(el('span', 'rt', 'Push to ' + b.branch), el('div', 'rsub', 'No check has run for this branch on this PC.')); return d; })()); bl.append(li); }
  }
  br.append(bl); grid.append(br);

  const pr = card('Latest pull requests', 'The newest check of each pull request.');
  if (!o.pulls.length) pr.append(el('p', 'none', 'No pull request has been checked here yet.'));
  else pr.append(runList(o.pulls));
  if (o.pulls.length) pr.append(para('more', (() => { const a = el('a', '', 'See all runs'); a.href = hashFor('runs'); return a; })()));
  grid.append(pr);

  const ag = card('Agents', 'An agent is the program on a PC that picks up waiting runs and runs them.');
  if (!o.agents.length) ag.append(para('none', 'No agent has reported in. Start one with ', code('unravis-ci serve'), '.'));
  for (const a of o.agents) ag.append(agentRow(a));
  grid.append(ag);

  const po = card('Last results posted to GitHub', 'What this PC told GitHub about a commit, newest first.');
  if (!o.posted.length) po.append(el('p', 'none', 'Nothing has been posted to GitHub yet.'));
  else po.append(postedList(o.posted));
  grid.append(po);
}

// The headline: do the checks on this PC give the same answers as GitHub? ("shadow" in the docs; the page says what it is.)
function compareCard(s) {
  const c = el('section', 'card wide headline');
  c.append(el('h3', '', 'Checks on this PC compared with GitHub'));
  c.append(el('p', 'help', 'Pull requests are checked on GitHub and also here, and the two answers are compared. Once ' + s.gate + ' pull requests in a row get the same answer in both places, the checks on this PC can be trusted on their own.'));
  if (!s.available) {
    c.append(para('none', 'No comparison has been saved yet. Run ', code('unravis-ci compare --save'), ' to make one; this page shows the newest.'));
    return c;
  }
  const row = el('div', 'streak');
  const pg = el('progress'); pg.max = s.gate; pg.value = Math.min(s.streak, s.gate); pg.setAttribute('aria-label', 'Pull requests in a row that agree: ' + s.streak + ' of ' + s.gate);
  row.append(el('span', 'big', s.streak + ' of ' + s.gate), el('span', 'bigl', ' pull requests in a row agree'), pg);
  c.append(row);
  c.append(el('p', s.gateMet ? 'note ok' : 'note', s.gateMet ? 'The target is reached.' : (s.gate - s.streak) + ' more in a row to reach the target.'));
  if (!s.agree && !s.disagree && !s.localOnly && s.githubOnly) c.append(para('note', 'None of these pull requests has been checked on this PC yet. Start the agent with ', code('unravis-ci serve'), '; new pull requests are then checked here as well.'));
  const f = el('dl', 'facts');
  const fact = (k, v, tip) => { const dt = el('dt', '', k); if (tip) dt.title = tip; f.append(dt, el('dd', '', String(v))); };
  fact('Same answer', s.agree, 'Pull requests where this PC and GitHub agreed.');
  fact('Different answer', s.disagree, 'Pull requests where this PC and GitHub did not agree.');
  fact('Only checked here', s.localOnly, 'Checked on this PC, but GitHub has no matching check.');
  fact('Only checked on GitHub', s.githubOnly, 'Checked on GitHub, but this PC has no matching check.');
  fact('Compared', ago(s.generated), 'To make a newer comparison run unravis-ci compare --save.');
  c.append(f);
  if (s.disagreeing.length) { c.append(el('h4', '', 'Pull requests with a different answer')); const ul = el('ul', 'lines bad'); for (const t of s.disagreeing) ul.append(el('li', '', t)); c.append(ul); }
  if (s.warnings.length) { c.append(el('h4', '', 'Notes')); const ul = el('ul', 'lines'); for (const t of s.warnings) ul.append(el('li', '', t)); c.append(ul); }
  const a = el('a', '', 'See all runs'); a.href = hashFor('runs'); c.append(para('more', a));
  return c;
}

function agentRow(a) {
  const d = el('div', 'agent');
  const head = el('div', 'ahead');
  head.append(el('span', 'tag ' + (a.fresh ? 'ok' : 'warn'), a.fresh ? 'Online' : 'Quiet'), el('span', 'aname', a.name));
  d.append(head);
  const bits = [];
  if (a.fresh) bits.push(a.state ? 'State: ' + a.state : '', a.slots ? a.slots + (a.slots === 1 ? ' job at a time' : ' jobs at a time') : '');
  else bits.push('Has not been heard from for ' + (a.silent || 'a while') + '. It may have stopped.');
  if (a.statusMode) bits.push('Posts to GitHub: ' + a.statusMode);
  d.append(el('div', 'rsub', bits.filter(Boolean).join(' · ')));
  for (const j of a.jobs) d.append(el('div', 'rsub', 'Working on: ' + j.label + (j.since ? ' (since ' + clock(j.since) + ')' : '')));
  if (a.postFailed) d.append(el('div', 'rwhy', a.postFailed + ' status post' + (a.postFailed === 1 ? '' : 's') + ' to GitHub failed' + (a.lastError ? ': ' + a.lastError : '.')));
  if (a.postWaiting) d.append(el('div', 'rsub', a.postWaiting + ' waiting to be posted.'));
  return d;
}

function postedList(list) {
  const ul = el('ul', 'rlist');
  for (const x of list) {
    const li = el('li', 'rrow'); const main2 = el('div', 'rmain');
    main2.append(el('span', 'rt plain', x.context), el('div', 'rsub', [x.description, x.at ? when(x.at) : ''].filter(Boolean).join(' · ')));
    const chips = el('div', 'rchips'); if (x.sha) { const c = el('span', 'chip mono', x.sha.slice(0, 7)); c.title = 'Commit ' + x.sha; chips.append(c); }
    li.append(statusTag(x.status, x.word), main2, chips); ul.append(li);
  }
  return ul;
}

// ---------- Machines ----------
function machineTag(word) {
  const s = CI_MACHINE[word] || ['?', 'mut', ''];
  const t = el('span', 'tag cis ' + s[1]);
  const g = el('span', 'g', s[0]); g.setAttribute('aria-hidden', 'true');
  t.append(g, el('span', 'w', word)); t.title = s[2];
  return t;
}
function showMachines() {
  const p = proj();
  ci.view = {
    fetch: () => getJSON(apiUrl('/api/machines')),
    fast: (m) => m.machines.some((x) => x.state === 'Running'),
    fail: (e) => { if (!ci.sig) failBox(e); },
    draw: (m) => drawMachines(m, p),
  };
  main().append(el('p', 'note', 'Reading...'));
  tick(true);
}
// A cell is a td holding one value box, so the stacked phone layout is a two-column grid of label and value.
// It returns the value box; the td is on its .cell.
function machineCell(label, cls) { const td = el('td', cls || ''); td.dataset.label = label; const v = el('div', 'mv'); td.append(v); v.cell = td; return v; }
function machineRow(x) {
  const tr = el('tr', 'mrow' + (x.state === 'Quiet' || x.state === 'Stopped' ? ' off' : ''));
  const who = machineCell('Machine', 'mwho');
  who.append(el('span', 'mid', x.id));
  if (x.self) { const t = el('span', 'chip', 'this PC'); t.title = 'This is the PC you are looking at. Its record is the one the agent here publishes.'; who.append(t); }
  const labels = machineCell('Labels');
  if (x.labels.length) { for (const l of x.labels) { const c = el('span', 'chip', l); c.title = 'A label the owner of this PC gave it, such as the kind of work it takes.'; labels.append(c); } }
  else labels.append(el('span', 'dim', 'none'));
  const st = machineCell('State'); st.append(machineTag(x.state));
  if (x.note) st.append(el('div', 'rsub', x.note));
  const cur = machineCell('Current run');
  if (x.jobs.length) for (const j of x.jobs) cur.append(el('div', '', j.label + (j.since ? ' (since ' + clock(j.since) + ')' : '')));
  else cur.append(el('span', 'dim', 'Nothing right now'));
  if (x.slots) cur.append(el('div', 'rsub', x.slots + (x.slots === 1 ? ' check at a time' : ' checks at a time')));
  const heard = machineCell('Last heard');
  const h = el('span', '', x.lastHeard ? ago(x.lastHeard) : 'never'); if (x.lastHeard) h.title = when(x.lastHeard);
  heard.append(h);
  tr.append(who.cell, labels.cell, st.cell, cur.cell, heard.cell);
  return tr;
}
function drawMachines(m, p) {
  $('refresh').classList.remove('primary');
  setMachinesShown(ci.pid, !!m.show);
  if (!m.ciFound) { noStoreBox(p); return; }
  const out = main();
  out.append(el('h2', 'ctitle', (p && p.name ? p.name + ': ' : '') + 'Machines'));
  out.append(para('help lead', 'A machine is a PC that runs the agent for this repository. Before a PC checks something it puts a claim on it, so two PCs never do the same check. If a PC goes quiet, another PC takes its check over. Other PCs are shown by a short code only; their names are never shared. The list is refreshed about once a minute, so a change on another PC can take that long to show.'));
  if (!m.seen) {
    out.append(emptyBox('No list of PCs has been saved yet.', para('nl-next', 'The agent on this PC writes the list about once a minute while it runs. Start it with ', code('unravis-ci serve'), ', wait a minute, then press Refresh. If it is already running, check that it was not started with ', code('--no-claims'), '; that turns the list off.')));
    return;
  }
  if (m.stale) out.append(para('note warn', 'This list was last saved ' + m.stale + ' ago, so it may be out of date. The agent on this PC may have stopped. Start it with ', code('unravis-ci serve'), '.'));
  if (m.machines.length === 1) out.append(para('note', 'Only this PC has been seen so far. Other PCs appear here once they run ', code('unravis-ci serve'), ' for the same repository.'));
  if (!m.machines.length) { out.append(para('none', 'No PC is on the list.')); return; }
  const c = card('PCs that check this repository', 'Read only. This page cannot start, stop or change a PC.');
  const tbl = el('table', 'mtable');
  const cap = el('caption', 'sr', 'PCs that check this repository'); tbl.append(cap);
  const head = el('tr'); for (const h of ['Machine', 'Labels', 'State', 'Current run', 'Last heard']) { const th = el('th', '', h); th.scope = 'col'; head.append(th); }
  const th = el('thead'); th.append(head); tbl.append(th);
  const tb = el('tbody'); for (const x of m.machines) tb.append(machineRow(x)); tbl.append(tb);
  c.append(tbl);
  out.append(c);
}

// ---------- Runs list ----------
function showRuns(q) {
  const rs = ci.rs = { rows: [], next: '', total: 0, loaded: false, err: '', more: false, f: { status: q.get('status') || '', branch: q.get('branch') || '', pr: q.get('pr') || '', sha: q.get('sha') || '', release: q.get('release') || '' } };
  const p = proj();
  const m = main();
  m.append(el('h2', 'ctitle', (p && p.name ? p.name + ': ' : '') + 'Runs'));
  const form = el('form', 'filters'); form.setAttribute('role', 'search'); form.setAttribute('aria-label', 'Filter the runs');
  const field = (label, input, tip) => { const l = el('label', 'fld'); l.append(el('span', '', label), input); if (tip) l.title = tip; return l; };
  const st = el('select'); for (const [k, w] of CI_STATUS_FILTER) { const o = el('option', '', w); o.value = k; st.append(o); } st.value = rs.f.status;
  const br = el('input'); br.type = 'text'; br.placeholder = 'for example master'; br.value = rs.f.branch; br.maxLength = 120; br.autocomplete = 'off';
  const pr = el('input'); pr.type = 'number'; pr.min = '1'; pr.step = '1'; pr.placeholder = 'number'; pr.value = rs.f.pr;
  const sh = el('input'); sh.type = 'text'; sh.placeholder = 'first letters'; sh.value = rs.f.sha; sh.maxLength = 64; sh.autocomplete = 'off'; sh.pattern = '[0-9a-fA-F]{4,64}'; sh.title = '4 or more letters and digits of a commit (0-9, a-f).';
  form.append(field('Status', st), field('Branch', br, 'The branch that was pushed, or the branch of the pull request.'), field('Pull request', pr), field('Commit', sh, 'The first letters of a commit, at least 4.'));
  const clear = el('button', '', 'Clear filters'); clear.type = 'button';
  form.append(clear);
  const sum = el('p', 'count'); sum.id = 'rs-count'; sum.setAttribute('aria-live', 'polite');
  const err = el('p', 'note bad'); err.id = 'rs-err'; err.hidden = true; err.setAttribute('role', 'alert');
  const listBox = el('div'); listBox.id = 'rs-list';
  const moreBox = el('div', 'vmore'); moreBox.id = 'rs-more';
  m.append(form, err, sum, listBox, moreBox);

  const apply = () => {
    rs.f = { status: st.value, branch: br.value.trim(), pr: pr.value.trim(), sha: sh.value.trim().toLowerCase(), release: rs.f.release };
    history.replaceState(null, '', hashFor('runs', '', rs.f)); ci.lastRuns = location.hash;
    rs.rows = []; rs.next = ''; rs.loaded = false; rs.err = ''; ci.sig = ''; ci.epoch++; ci.busyEp = -1; clearTimeout(ci.timer); ci.view = runsView(rs);
    drawRuns(rs); tick(true);
  };
  st.addEventListener('change', apply);
  let deb = 0; const later = () => { clearTimeout(deb); deb = setTimeout(apply, 350); };
  for (const i of [br, pr, sh]) { i.addEventListener('input', later); i.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); clearTimeout(deb); apply(); } e.stopPropagation(); }); }
  form.addEventListener('submit', (e) => { e.preventDefault(); apply(); });
  clear.addEventListener('click', () => { st.value = ''; br.value = ''; pr.value = ''; sh.value = ''; rs.f.release = ''; apply(); });
  ci.lastRuns = location.hash;
  ci.view = runsView(rs);
  drawRuns(rs); tick(true);
}

function runsView(rs) {
  return {
    fetch: async () => {
      const n = rs.rows.length;
      if (n > 200) throw new Error('keep'); // many pages are open: the list stops refreshing itself
      return getJSON(apiUrl('/api/runs', { status: rs.f.status, branch: rs.f.branch, pr: rs.f.pr, sha: rs.f.sha, release: rs.f.release, limit: Math.min(200, Math.max(30, n)) }));
    },
    fast: (d) => d.runs.some((r) => r.status === 'running' || r.status === 'waiting'),
    fail: (e) => { rs.err = e.message === 'keep' ? '' : plainErr(e); rs.loaded = true; drawRuns(rs); },
    draw: (d) => { rs.rows = d.runs; rs.next = d.next || ''; rs.total = d.total; rs.found = d.ciFound; rs.loaded = true; rs.err = ''; drawRuns(rs); },
  };
}

function filtersOn(f) { return !!(f.status || f.branch || f.pr || f.sha || f.release); }
function drawRuns(rs) {
  const p = proj();
  $('refresh').classList.remove('primary');
  const err = $('rs-err'), sum = $('rs-count'), box = $('rs-list'), more = $('rs-more');
  if (!err) return; // the page moved on
  err.hidden = !rs.err; err.textContent = rs.err;
  box.replaceChildren(); more.replaceChildren();
  if (rs.err) { sum.textContent = ''; return; }
  if (!rs.loaded) { sum.textContent = ''; box.append(el('p', 'note', 'Reading...')); return; }
  if (rs.found === false) { sum.textContent = ''; noStoreBox(p); return; }
  sum.textContent = rs.total + (rs.total === 1 ? ' run' : ' runs') + (filtersOn(rs.f) ? ' match the filters' : '') + (rs.rows.length < rs.total ? '. Showing the newest ' + rs.rows.length + '.' : '.');
  if (rs.f.release) sum.textContent += ' Release ' + rs.f.release + '.';
  if (!rs.rows.length) {
    box.append(filtersOn(rs.f) ? emptyBox('No run matches these filters.', para('nl-next', 'Change a filter, or press Clear filters.')) : emptyBox('No runs yet.', para('nl-next', 'Start the agent with ', code('unravis-ci serve'), ', then press Refresh.')));
    return;
  }
  box.append(runList(rs.rows, 'big'));
  if (rs.next) {
    const b = el('button', '', 'Show ' + Math.min(30, rs.total - rs.rows.length) + ' more runs'); b.type = 'button';
    b.addEventListener('click', async () => {
      b.disabled = true; const ep = ci.epoch;
      try {
        const d = await getJSON(apiUrl('/api/runs', { status: rs.f.status, branch: rs.f.branch, pr: rs.f.pr, sha: rs.f.sha, release: rs.f.release, limit: 30, cursor: rs.next }));
        if (ep !== ci.epoch) return;
        const seen = new Set(rs.rows.map((r) => r.id)); for (const r of d.runs) if (!seen.has(r.id)) rs.rows.push(r);
        rs.next = d.next || ''; rs.total = d.total; ci.sig = JSON.stringify({ runs: rs.rows }); drawRuns(rs);
      } catch (e) { rs.err = plainErr(e); drawRuns(rs); }
    });
    more.append(b);
  }
}

// ---------- one run ----------
function logKey(id, job, step) { return 'ci:' + ci.pid + '|' + id + '|' + job + '|' + step; }
// Called by the log viewer (detail.js) to read one page of a CI step log.
function ciLogUrl(file, from, n) {
  const [pid, id, job, step] = file.slice(3).split('|');
  const q = new URLSearchParams({ project: pid, id, job, step, from: String(from), n: String(n) });
  return '/api/run/log?' + q.toString();
}

function showRun(id) {
  const p = proj();
  const back = el('a', 'back', '← All runs'); back.href = ci.lastRuns && ci.lastRuns.startsWith('#/runs') && !ci.lastRuns.startsWith('#/runs/') ? ci.lastRuns : hashFor('runs');
  const reqLog = { notFound: false };
  ci.view = {
    fetch: () => getJSON(apiUrl('/api/run', { id })),
    fast: (d) => !!d.live || d.run.status === 'running' || d.run.status === 'waiting',
    fail: (e) => { if (!ci.sig) { const m = main(); m.append(back); failBox(e); m.prepend(back); reqLog.notFound = true; } },
    draw: (d) => drawRun(d, back, p),
  };
  const m = main(); m.append(back, el('p', 'note', 'Reading...'));
  tick(true);
}

function drawRun(d, back, p) {
  $('refresh').classList.remove('primary');
  const m = $('ci-main'), box = $('ci'); const keepTop = box.scrollTop; m.replaceChildren();
  const r = d.run;
  if (ci.opened !== ci.pid + '|' + r.id) { ci.opened = ci.pid + '|' + r.id; ci.open = new Set(); defaultOpen(d); }
  m.append(back);
  const h = el('h2', 'ctitle', runTitle(r)); m.append(h);
  const meta = el('div', 'meta rmeta');
  meta.append(statusTag(r.status, r.word)); for (const c of [shaChip(r), relChip(r)]) if (c) meta.append(c);
  m.append(meta);
  const f = el('dl', 'facts');
  const fact = (k, v) => { if (v) f.append(el('dt', '', k), el('dd', '', v)); };
  fact('Check', r.workflow);
  fact(r.event === 'pull_request' ? 'Opened by' : 'Pushed by', r.author);
  fact(r.status === 'waiting' ? 'Queued' : 'Started', r.start ? when(r.start) + ' (' + ago(r.start) + ')' : '');
  fact('Finished', r.end ? when(r.end) : '');
  fact('Took', took(r.ms) ? took(r.ms) + (r.status === 'running' ? ' so far' : '') : '');
  fact('Run by', r.agent);
  if (r.sha) { const dt = el('dt', '', 'Commit'), dd = el('dd', 'mono', r.sha); f.append(dt, dd); }
  m.append(f);
  if (r.reason) m.append(el('p', r.status === 'failed' ? 'note bad' : 'note', r.reason));
  if (r.stale) m.append(el('p', 'note warn', r.note || 'The agent that was running this has gone quiet, so what you see may be out of date.'));
  if (d.live) m.append(el('p', 'note', 'This run is going. The page reads again every few seconds.'));

  if (!d.jobs.length && !d.checkout) m.append(noJobsNote(r));
  if (d.checkout) {
    const holder = el('div', 'vhold'); const det = detailsFor('co', 'Before the jobs: fetching and merging the code', holder, () => viewer(logKey(r.id, '_checkout', 1), { from: 0, to: null, live: d.live && !d.jobs.length }), 'The log of getting the code ready. Open it if the run stopped before any job started.');
    const wrap = el('div', 'cjob'); wrap.append(det); m.append(sectionHead('Preparation'), wrap);
  }
  if (d.jobs.length) {
    m.append(sectionHead('Jobs' + (d.planned && d.planned > d.jobs.length ? ' (' + d.jobs.length + ' of ' + d.planned + ' so far)' : '')));
    for (const j of d.jobs) m.append(jobBlock(r, j, d));
  }
  if (d.summary) {
    m.append(sectionHead('Summary written by the run'));
    m.append(el('pre', 'sumpre', d.summary));
    if (d.summaryCut) m.append(el('p', 'note', 'The summary is long. Only the start is shown.'));
  }
  m.append(sectionHead('Reported to GitHub'));
  if (d.posted.length) m.append(postedList(d.posted)); else m.append(el('p', 'none', 'Nothing was reported to GitHub for this commit.'));
  box.scrollTop = keepTop;
}

function noJobsNote(r) {
  if (r.status === 'waiting') return para('note', 'This run has not started. It is in the queue until an agent is free. Start one with ', code('unravis-ci serve'), '.');
  if (r.status === 'not-recorded') return el('p', 'note', 'This run finished, but its jobs and logs are not saved on this PC.');
  if (r.status === 'stopped') return el('p', 'note', 'This run was stopped before any job ran.');
  return el('p', 'note', 'No jobs were saved for this run.');
}

// Failed or running jobs start open, and the first failed step is opened so the reason is one look away.
function defaultOpen(d) {
  for (const j of d.jobs) {
    if (j.status === 'failed' || j.status === 'running') ci.open.add('job:' + j.key);
    for (const s of j.steps) if (s.status === 'failed' && !ci.open.has('first')) { ci.open.add('first'); ci.open.add('step:' + j.key + ':' + s.index); }
  }
  if (d.jobs.length === 1) ci.open.add('job:' + d.jobs[0].key);
  if (d.checkout && (!d.jobs.length || d.run.status === 'failed' && !ci.open.has('first'))) ci.open.add('co');
}

// A <details> that remembers whether it is open while the page is redrawn, and mounts its log viewer only when open.
function detailsFor(id, title, holder, makeViewerFor, tip) {
  const det = el('details', 'logd'); det.id = 'ci-' + id.replace(/[^A-Za-z0-9_-]/g, '_');
  const sum = el('summary', 'srow'); if (tip) sum.title = tip;
  sum.append(el('span', 'nm', title), el('span', 'at', 'Log'));
  det.append(sum, holder);
  det.open = ci.open.has(id);
  const mount = () => { if (det.open) mountViewer(holder, makeViewerFor()); };
  det.addEventListener('toggle', () => { if (det.open) ci.open.add(id); else ci.open.delete(id); mount(); });
  mount();
  return det;
}

function jobBlock(r, j, d) {
  const id = 'job:' + j.key;
  const det = el('details', 'cjob ' + (CI_STATUS[j.status] || CI_STATUS.skipped)[1]); det.open = ci.open.has(id);
  det.addEventListener('toggle', () => { if (det.open) ci.open.add(id); else ci.open.delete(id); });
  const sum = el('summary', 'jhead');
  const nm = el('span', 'nm'); nm.append(el('span', 'n', j.label));
  if (j.pseudo) nm.append(el('span', 'help', 'The run stopped here, before this part of the workflow could start.'));
  const at = el('span', 'at', [took(j.ms), j.steps.length ? j.steps.length + (j.steps.length === 1 ? ' step' : ' steps') : ''].filter(Boolean).join(' · '));
  sum.append(statusTag(j.status, j.word), nm, at);
  det.append(sum);
  if (j.reason && !(j.steps || []).some((s) => s.err === j.reason)) det.append(el('p', 'note bad jwhy', j.reason));
  const list = el('div', 'stepl');
  if (!j.steps.length) list.append(el('p', 'none', j.pseudo ? '' : 'No steps were saved for this job.'));
  for (const s of j.steps) list.append(stepBlock(r, j, s, d));
  det.append(list);
  return det;
}

function stepBlock(r, j, s, d) {
  const id = 'step:' + j.key + ':' + s.index;
  const kind = (CI_STATUS[s.status] || CI_STATUS.skipped)[1];
  if (!s.hasLog) {
    const row = el('div', 'step ' + kind); const sum = el('div', 'srow');
    sum.append(statusTag(s.status, s.word), stepName(s), el('span', 'at', took(s.ms)));
    row.append(sum); if (s.err) row.append(el('div', 'reason', s.err)); return row;
  }
  const row = el('details', 'step ' + kind); row.id = 'ci-' + id.replace(/[^A-Za-z0-9_-]/g, '_'); row.open = ci.open.has(id);
  const sum = el('summary', 'srow'); sum.append(statusTag(s.status, s.word), stepName(s), el('span', 'at', took(s.ms)));
  row.append(sum); if (s.err) row.append(el('div', 'reason', s.err));
  const holder = el('div', 'vhold'); row.append(holder);
  const live = !!d.live && s.status === 'running';
  const mount = () => { if (row.open) mountViewer(holder, viewer(logKey(r.id, j.key, s.index), { from: 0, to: null, live })); };
  row.addEventListener('toggle', () => { if (row.open) ci.open.add(id); else ci.open.delete(id); mount(); });
  mount();
  return row;
}
function stepName(s) { const nm = el('span', 'nm'); nm.append(el('span', 'n', s.name)); return nm; }

// ---------- the Releases page: the CI runs on a release's commit (the link back from a release to its runs) ----------
const relRuns = new Map();
function releaseRunChips(host, r) {
  const p = proj(); if (!p || !p.ciFound || !r || r.version === NEXT) return;
  const key = ci.pid + '|' + r.version, hit = relRuns.get(key);
  const put = (list) => {
    const old = host.querySelector('.runchips'); if (old) old.remove();
    if (!list || !list.length) return;
    const box = el('p', 'runchips'); box.append(el('span', 'k', 'Checks on this release\'s commit: '));
    for (const x of list) {
      const a = el('a', 'chip', (CI_STATUS[x.status] ? CI_STATUS[x.status][0] + ' ' : '') + (x.word || x.status) + ' · ' + (x.sha ? x.sha.slice(0, 7) : x.id));
      a.href = hashFor('runs', x.id); a.title = 'Open this run in the Runs tab. ' + runTitle(x); box.append(a, ' ');
    }
    host.append(box);
  };
  if (hit) put(hit.list);
  if (hit && Date.now() - hit.at < 60000) return;
  const pid = ci.pid;
  getJSON(apiUrl('/api/runs', { release: r.version, limit: 3 })).then((j) => {
    relRuns.set(key, { at: Date.now(), list: j.runs || [] });
    const h = $('dhead'); if (h && ui.ver === r.version && ci.pid === pid && section === 'releases') put(j.runs || []);
  }).catch(() => { /* the chips are a convenience; the release page works without them */ });
}

// ---------- start ----------
async function startCI() {
  try {
    const j = await getJSON('/api/projects');
    ci.projects = j.projects || [];
  } catch (e) { ci.projects = []; }
  if (!ci.projects.length) { // the program answered no project list: the Releases page alone, as before
    $('mainnav').hidden = true; showSection('releases'); $('ci').hidden = true; return;
  }
  ci.ready = true; buildSwitcher(); route(true);
}
$('refresh').addEventListener('click', () => { if (section !== 'releases') ciRefresh(); });
document.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey || section === 'releases') return;
  const t = e.target;
  if (t && t.closest && t.closest('input, textarea, select, .vbody')) return;
  if (e.key === 'r' || e.key === 'R') ciRefresh();
});
startCI();
