'use strict';
// The page's data: one object D holding the newest answers of the read-only /api routes, and Data, which loads them.
// The page draws from D synchronously. Data.start() keeps D fresh (fast poll for runs, slow poll for releases) and calls
// onChange only when an answer really changed, so the page does not redraw for nothing.
// D.at is when runs and machines were last read (the fast poll); D.snapshotAt is when the releases were last read (the slow
// poll). Each moves only on a successful read, so when a read fails the old answer stays on screen and its time stays put.
const D = { projects: [], pid: '', overview: null, runs: [], runsTotal: 0, machines: null, snapshot: null,
  run: {}, ghrun: {}, logs: {}, at: 0, snapshotAt: 0, snapshotErr: '', err: '', loaded: false, projectsErr: '',
  // The queue, machine rules, holds and activity (BL-706). err holds one plain-words message per answer that failed;
  // old is true when this program is too old to know the four routes (it answered 404 without a message).
  ops: { queue: null, policy: null, holds: null, activity: null, err: {}, old: false, loaded: false } };

// How often each kind of data is read, and how many missed reads it takes before the page calls its data old.
const POLL = { fast: 4000, slow: 30000, warnAfter: 3 };

// A failed read in plain words. The browser's own text for a request that never arrived ("Failed to fetch") means nothing to a
// first-time reader; here it means the program that serves this page no longer answers.
function plainReason(e) {
  if (e && e.status) return e.message;
  return 'Homestretch could not reach the unravis-releases program on this PC. It may have been closed.';
}

// What the freshness chip and the "showing older data" note say. They only read D and the clock, so they can be tested alone.
const Fresh = {
  info(now) {
    const releasesAt = D.snapshot ? D.snapshotAt : 0; const runsAt = D.at;
    const stale = !!((runsAt && now - runsAt > POLL.warnAfter * POLL.fast) || (releasesAt && now - releasesAt > POLL.warnAfter * POLL.slow));
    const times = [releasesAt, runsAt].filter(Boolean);
    return { stale, older: times.length ? Math.min(...times) : 0, releasesAt, runsAt,
      releasesFailed: !!(releasesAt && D.snapshotErr), runsFailed: !!(runsAt && D.err) };
  },
  clock: (t) => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
  age(ms) { const s = Math.round(ms / 1000); return s < 60 ? `${s} s` : s < 3600 ? `${Math.round(s / 60)} min` : `${Math.round(s / 3600)} h`; },
  // The chip shows the older of the two read times; it turns to the warning style at 3 missed polls of either read.
  chip(now) {
    const f = this.info(now);
    if (!f.older) return { text: '', title: '', stale: false };
    const parts = [];
    if (f.releasesAt) parts.push(`Releases read at ${this.clock(f.releasesAt)}`);
    if (f.runsAt) parts.push(`Runs and machines read at ${this.clock(f.runsAt)}`);
    return { text: f.stale ? `Data is ${this.age(now - f.older)} old` : `Updated ${rel(f.older)}`, title: parts.join('. ') + '.', stale: f.stale };
  },
  // The note above the page. The Releases page shows both reads; every other page shows only the runs and machines one.
  noteHtml(page, now) {
    const f = this.info(now); const parts = []; const why = [];
    if (page === 'releases' && f.releasesFailed) { parts.push(`releases as read at ${this.clock(f.releasesAt)}`); why.push(D.snapshotErr); }
    if (f.runsFailed) { parts.push(`runs and machines as read at ${this.clock(f.runsAt)}`); why.push(D.err); }
    if (!parts.length) return '';
    const reason = [...new Set(why)].map((x) => (/[.!?]$/.test(x) ? x : x + '.')).join(' ');
    return `<div class="stale-note" role="status">${ico('clock')}<p class="txt"><b>Showing ${esc(parts.join(' and '))}.</b> Could not refresh: ${esc(reason)}</p><button type="button" class="btn sm" data-check>Check again</button></div>`;
  },
};

// The action key of the write channel arrives only in the start link's fragment (#a=...), which no server ever sees.
// Keep it for this tab in sessionStorage and take it out of the address bar, so it is not in history or a copied link.
const ActionKey = (() => {
  const K = 'hs-action-key';
  try {
    const m = /^#a=([0-9a-f]{64})$/.exec(location.hash || '');
    if (m) { sessionStorage.setItem(K, m[1]); history.replaceState(null, '', location.pathname + location.search); }
  } catch (e) { /* no storage: actions answer that the page has no key */ }
  return () => { try { return sessionStorage.getItem(K) || ''; } catch (e) { return ''; } };
})();

const Data = (() => {
  const SAVED = 'hs-project';
  let epoch = 0; let onChange = () => {}; let timers = []; let busy = false;
  const sig = {};

  async function api(path, params) {
    const q = new URLSearchParams(params || {});
    const res = await fetch(path + (q.toString() ? '?' + q.toString() : ''), { credentials: 'same-origin', cache: 'no-store' });
    let body = null;
    try { body = await res.json(); } catch (e) { /* not JSON */ }
    if (!res.ok) { const e = new Error((body && body.error) || (res.status === 401 ? 'This page needs a fresh link. Open the link that unravis-releases printed.' : `The server answered ${res.status}.`)); e.status = res.status; e.old = res.status === 404 && !(body && body.error); throw e; }
    return body;
  }
  const same = (key, val) => { const s = JSON.stringify(val); if (sig[key] === s) return true; sig[key] = s; return false; };
  const P = () => ({ project: D.pid });

  function pick(id) {
    if (id === D.pid) return;
    D.pid = id; epoch++;
    D.overview = null; D.runs = []; D.runsTotal = 0; D.machines = null; D.run = {}; D.ghrun = {}; D.logs = {};
    D.ops = { queue: null, policy: null, holds: null, activity: null, err: {}, old: false, loaded: false };
    for (const k of Object.keys(sig)) if (k !== 'projects' && k !== 'snapshot') delete sig[k];
    try { localStorage.setItem(SAVED, id); } catch (e) { /* not saved */ }
  }

  async function loadProjects() {
    const a = await api('/api/projects');
    D.projects = a.projects || []; D.projectsErr = '';
    if (!D.projects.some((p) => p.id === D.pid)) {
      let saved = ''; try { saved = localStorage.getItem(SAVED) || ''; } catch (e) { /* none */ }
      const first = D.projects.find((p) => p.id === saved) || D.projects.find((p) => p.ciFound) || D.projects[0];
      if (first) pick(first.id);
    }
    return !same('projects', D.projects);
  }

  // Runs and machines come from the project's own CI store, so they are loaded together and per project.
  async function loadFast() {
    if (!D.pid) return false;
    const my = epoch;
    const [ov, rs, ma] = await Promise.all([api('/api/overview', P()), api('/api/runs', { ...P(), limit: 200 }), api('/api/machines', P()).catch(() => null)]);
    if (my !== epoch) return false; // the project was switched while this was loading
    D.at = Date.now(); D.err = '';
    // "now" moves every second on the server, so it must not count as a change.
    const ovs = { ...ov, now: 0 }; const mas = ma && { ...ma, now: 0 };
    const c1 = !same('overview', ovs); const c2 = !same('runs', rs); const c3 = !same('machines', mas);
    D.overview = ov; D.runs = rs.runs || []; D.runsTotal = rs.total || 0; D.machines = ma;
    return c1 || c2 || c3;
  }

  // The four Homestretch answers are loaded on their own, never inside loadFast: one of them failing must not blank the
  // runs, and a program that is too old for them must still show its runs. It never throws.
  const OPS = [['queue', '/api/queue', { limit: 50 }], ['policy', '/api/policy', {}], ['holds', '/api/holds', {}], ['activity', '/api/activity', { limit: 200 }]];
  async function loadOps() {
    if (!D.pid || D.ops.old) return false;
    const my = epoch; const o = D.ops; let changed = false;
    await Promise.all(OPS.map(async ([key, path, extra]) => {
      try {
        const v = await api(path, { ...P(), ...extra });
        if (my !== epoch) return;
        o.err[key] = '';
        if (!same('ops-' + key, { ...v, now: '' })) changed = true;
        o[key] = v;
      } catch (e) {
        if (my !== epoch) return;
        if (e.old) { o.old = true; changed = true; return; }
        if (o.err[key] !== e.message) { o.err[key] = e.message; changed = true; }
      }
    }));
    if (my !== epoch) return false;
    if (!o.loaded) { o.loaded = true; changed = true; }
    return changed;
  }
  // refreshOps is for the moment after a write: it waits for the answers instead of skipping when a poll is running.
  async function refreshOps() { const c = await loadOps(); onChange(c ? 'data' : 'tick'); return c; }

  async function loadSlow() {
    const [pr, sn] = await Promise.all([loadProjects().catch(() => false), api('/api/snapshot').catch((e) => { D.snapshotErr = plainReason(e); return null; })]);
    let c = pr;
    if (sn) { D.snapshotErr = ''; D.snapshotAt = Date.now(); const s = { ...sn, at: 0, run: 0 }; if (!same('snapshot', s)) c = true; D.snapshot = sn; }
    return c;
  }

  async function loadRun(id) {
    const my = epoch;
    const det = await api('/api/run', { ...P(), id });
    if (my !== epoch) return null;
    const changed = !same('run:' + id, det);
    D.run[id] = det; return changed;
  }

  async function loadGhRun(id) {
    const my = epoch;
    const det = await api('/api/ghrun', { id });
    if (my !== epoch) return null;
    const changed = !same('ghrun:' + id, det);
    D.ghrun[id] = det; return changed;
  }

  // One step log (job "_checkout" is the log of getting the code). The last 5000 lines come back, with their first line number.
  async function loadLog(id, job, step) {
    const my = epoch;
    const key = `${id}|${job}|${step}`;
    const p = { ...P(), id, job, tail: 1, n: 5000 }; if (job !== '_checkout') p.step = step;
    const lg = await api('/api/run/log', p);
    if (my !== epoch) return null;
    const changed = !D.logs[key] || D.logs[key].total !== lg.total || D.logs[key].live !== lg.live;
    D.logs[key] = lg; return changed;
  }
  const logKey = (id, job, step) => `${id}|${job}|${step}`;

  async function tick(slow) {
    if (busy) return; busy = true;
    const was = D.err + '\n' + D.snapshotErr; // a failure that starts or ends must redraw the page, even when no data changed
    try {
      let c = false;
      if (slow || !D.loaded) c = (await loadSlow().catch((e) => { D.projectsErr = e.message; return false; })) || c;
      c = (await loadFast()) || c;
      D.loaded = true;
      c = (await loadOps()) || c;
      if (c || was !== D.err + '\n' + D.snapshotErr) onChange('data');
      else onChange('tick');
    } catch (e) {
      D.err = plainReason(e); D.loaded = true;
      onChange(was === D.err + '\n' + D.snapshotErr ? 'tick' : 'error'); // the same failure again: no redraw
    } finally { busy = false; }
  }

  function start(cb) {
    onChange = cb;
    tick(true);
    const visible = () => document.visibilityState !== 'hidden';
    timers.push(setInterval(() => { if (visible()) tick(false); }, POLL.fast));
    timers.push(setInterval(() => { if (visible()) tick(true); }, POLL.slow));
    timers.push(setInterval(() => { fetch('/api/ping', { credentials: 'same-origin' }).catch(() => {}); }, 20000));
    document.addEventListener('visibilitychange', () => { if (visible()) tick(true); });
    // Closing the page lets the program stop itself after a short wait (see --exit-when-closed).
    addEventListener('pagehide', () => { fetch('/api/bye', { credentials: 'same-origin', keepalive: true }).catch(() => {}); });
  }
  // act runs one Homestretch action (POST /api/action/<id>) and returns its answer {ok, message, impact?, error?}.
  async function act(id, project, args) {
    const res = await fetch('/api/action/' + encodeURIComponent(id), {
      method: 'POST', credentials: 'same-origin', cache: 'no-store',
      headers: { 'Content-Type': 'application/json', 'X-Homestretch-Token': ActionKey() },
      body: JSON.stringify({ project, args: args || {} }),
    });
    let body = null;
    try { body = await res.json(); } catch (e) { /* not JSON */ }
    return Object.assign({ ok: false, message: `The server answered ${res.status}.` }, body || {}, { status: res.status });
  }
  return { api, act, start, tick, pick, loadRun, loadGhRun, loadLog, logKey, refreshOps, get epoch() { return epoch; } };
})();
