'use strict';
// The page's data: one object D holding the newest answers of the read-only /api routes, and Data, which loads them.
// The page draws from D synchronously. Data.start() keeps D fresh (fast poll for runs, slow poll for releases) and calls
// onChange only when an answer really changed, so the page does not redraw for nothing.
const D = { projects: [], pid: '', overview: null, runs: [], runsTotal: 0, machines: null, snapshot: null,
  run: {}, logs: {}, at: 0, err: '', loaded: false, projectsErr: '' };

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
    if (!res.ok) { const e = new Error((body && body.error) || (res.status === 401 ? 'This page needs a fresh link. Open the link that unravis-releases printed.' : `The server answered ${res.status}.`)); e.status = res.status; throw e; }
    return body;
  }
  const same = (key, val) => { const s = JSON.stringify(val); if (sig[key] === s) return true; sig[key] = s; return false; };
  const P = () => ({ project: D.pid });

  function pick(id) {
    if (id === D.pid) return;
    D.pid = id; epoch++;
    D.overview = null; D.runs = []; D.runsTotal = 0; D.machines = null; D.run = {}; D.logs = {};
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

  async function loadSlow() {
    const [pr, sn] = await Promise.all([loadProjects().catch(() => false), api('/api/snapshot').catch((e) => { D.snapshotErr = e.message; return null; })]);
    let c = pr;
    if (sn) { D.snapshotErr = ''; const s = { ...sn, at: 0, run: 0 }; if (!same('snapshot', s)) c = true; D.snapshot = sn; }
    return c;
  }

  async function loadRun(id) {
    const my = epoch;
    const det = await api('/api/run', { ...P(), id });
    if (my !== epoch) return null;
    const changed = JSON.stringify(D.run[id] && { ...D.run[id], run: 0 }) !== JSON.stringify({ ...det, run: 0 });
    D.run[id] = det; return changed;
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
    try {
      let c = false;
      if (slow || !D.loaded) c = (await loadSlow().catch((e) => { D.projectsErr = e.message; return false; })) || c;
      c = (await loadFast()) || c;
      D.loaded = true;
      if (c) onChange('data');
      else onChange('tick');
    } catch (e) {
      D.err = e.message; D.loaded = true; onChange('error');
    } finally { busy = false; }
  }

  function start(cb) {
    onChange = cb;
    tick(true);
    const visible = () => document.visibilityState !== 'hidden';
    timers.push(setInterval(() => { if (visible()) tick(false); }, 4000));
    timers.push(setInterval(() => { if (visible()) tick(true); }, 30000));
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
  return { api, act, start, tick, pick, loadRun, loadLog, logKey, get epoch() { return epoch; } };
})();
