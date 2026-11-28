'use strict';
// The queue, machine rules, holds and activity pages (BL-706).
// Every answer comes from the four read routes (data.js, D.ops) and every change goes through Data.act (one action each,
// the same ones the unravis-ci command line runs, so the activity log records them with the same words).
// Nothing here is simulated: with no answer, a page says why in plain words. All text goes through esc().
const Ops = (() => {
  // State of the page itself: what is open, what is being typed, the last result. The data lives in D.ops.
  const O = { repo: '', actor: 'all', confirm: null, reason: '', pol: null, edit: null, drawer: null, note: null, busy: {}, hold: { project: '', hours: '4h', reason: '' } };
  const repaint = () => render();
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
  const ops = () => D.ops;
  // A sentence from the server starts with a capital letter. A project path or an id (abhinavjp/unravis, q-2026...) is left as written.
  const cap = (t) => { const x = String(t == null ? '' : t); return /^[a-z]+(?= |,|\.|$)/.test(x) ? x[0].toUpperCase() + x.slice(1) : x; };
  // Times in the future (a hold's end) are not "just now": say how far away they are, and give the clock time.
  const fut = (t) => {
    const sec = (new Date(t).getTime() - Date.now()) / 1000; if (!(sec > 0)) return '';
    if (sec < 60) return 'in under a minute';
    if (sec < 3600) return `in ${Math.round(sec / 60)} min`;
    if (sec < 86400) return `in ${Math.round(sec / 3600)} h`;
    const n = Math.round(sec / 86400); return `in ${n} day${n === 1 ? '' : 's'}`;
  };
  const clock = (t) => { const d = new Date(t); return isNaN(d) ? '' : d.toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' }); };
  const at = (t) => { const f = t ? fut(t) : ''; return f ? `<time datetime="${esc(t)}" title="${esc(exact(t))}">${esc(f)}</time>` : when(t); };
  const until = (t) => { const f = t ? fut(t) : ''; return f ? `<time datetime="${esc(t)}" title="${esc(exact(t))}">${esc(clock(t))} (${esc(f)})</time>` : when(t); };
  const stw = (icon, word, cls, tip) => `<span class="st c-${cls}"${tip ? ` title="${esc(tip)}"` : ''}>${ico(icon)}<span class="st-l">${esc(word)}</span></span>`;
  const cli = (line, note) => `<div class="cli-wrap"><div class="cli-line"><span class="pr" aria-hidden="true">$</span><code>${esc(line)}</code><button type="button" class="btn sm ghost" data-ocopy="${esc(line)}" title="Copy this command">${ico('copy')} Copy</button></div>${note ? `<p class="cli-note">${esc(note)}</p>` : ''}</div>`;
  const loading = '<div class="empty empty-top"><b>Loading…</b> Reading this PC’s queue, rules and holds.</div>';
  const head = (title, lead, right) => `<div class="ph"><div class="minw0"><h1>${esc(title)}</h1><p>${esc(lead)}</p></div>${right ? `<div class="ml-a row-actions">${right}</div>` : ''}</div>`;
  const noteHtml = () => (O.note ? `<div class="warnbox ${O.note.kind === 'ok' ? 'ok' : O.note.kind === 'bad' ? 'bad' : ''}" role="status">${ico(O.note.kind === 'ok' ? 'passed' : O.note.kind === 'bad' ? 'failed' : 'waiting')}<div class="wb">${esc(O.note.text)}</div><button type="button" class="btn sm ghost icon" data-ops="dismiss" aria-label="Dismiss this message" title="Dismiss">${ico('x')}</button></div>` : '');

  // The first thing every page checks: is there an answer to draw? Returns the page's reason text, or '' when there is.
  function blocked(key, title, lead) {
    const o = ops();
    if (!D.loaded || !o.loaded) return head(title, lead) + loading;
    if (o.old) return head(title, lead) + `<div class="empty empty-top"><b>This copy of unravis-releases is older than this page.</b> It does not know about the queue, machine rules or holds yet, so nothing can be shown or changed here. Update unravis-releases, then open the new link it prints.</div>`;
    const v = o[key];
    if (!v) return head(title, lead) + `<div class="empty empty-top"><b>${esc(title)} could not be read.</b> ${esc(o.err[key] || 'Waiting for the first answer.')}</div>`;
    if (!v.ciFound) return head(title, lead) + `<div class="empty empty-top"><b>No check records found for this project.</b> This page reads them from the runs saved by <span class="mono">unravis-ci</span> on this PC. Start the agent once, and this page fills in.</div>`;
    return '';
  }
  const staleNote = (key) => { const o = ops(); const v = o[key]; return `${v && v.error ? `<div class="warnbox bad" role="status">${ico('failed')}<div class="wb"><b>Part of this could not be read.</b><p>${esc(v.error)}</p></div></div>` : ''}${o.err[key] ? `<div class="warnbox" role="status">${ico('waiting')}<div class="wb"><b>The last refresh failed.</b> ${esc(o.err[key])} Showing the last answer.</div></div>` : ''}`; };

  // ---------- doing things ----------
  const ACTION_WORD = { 'queue.top': 'Move to top', 'queue.cancel': 'Cancel', 'queue.retry': 'Retry', 'queue.pause': 'Pause the queue', 'queue.resume': 'Resume the queue', 'machines.drain': 'Drain a machine', 'machines.resume': 'Resume a machine', 'machines.allow': 'Allow a machine', 'machines.deny': 'Deny a machine', 'policy.set': 'Set a machine rule', 'hold.add': 'Add a hold', 'hold.lift': 'Lift a hold' };
  const actionWord = (a) => ACTION_WORD[a] || String(a || '');
  // A refusal or failure, in plain words. The server's own message is shown; nothing is swallowed.
  function plainFail(res) {
    const m = cap(String(res.message || '').trim());
    const e = res.error && !m.includes(res.error) ? ` (${res.error})` : '';
    if (res.status === 404 && !res.error) return 'This copy of unravis-releases cannot do that yet. Update it, then open the new link it prints.';
    if (res.status === 500) return `Something broke inside unravis-releases. ${m}${e}`.trim();
    return (m || 'That did not work.') + e;
  }
  async function run(key, id, args) {
    if (O.busy[key]) return null;
    O.busy[key] = 1; repaint();
    let res;
    try { res = await Data.act(id, D.pid, args); } catch (e) { res = { ok: false, message: 'Could not reach unravis-releases. Is it still running? ' + e.message, status: 0 }; }
    delete O.busy[key];
    return res;
  }
  // After a change: show the server's own words and read the four answers again.
  async function finish(res, ok) {
    O.note = res.ok ? { kind: 'ok', text: cap(ok || res.message || 'Done.') } : { kind: 'bad', text: plainFail(res) };
    try { await Data.refreshOps(); } catch (e) { /* the next poll shows it */ }
    repaint();
  }
  async function simple(key, id, args, okText) {
    O.confirm = null; O.note = null;
    const res = await run(key, id, args);
    if (res) await finish(res, okText && res.ok ? okText(res) : '');
  }

  // A rule edit runs in two steps: a dry run that writes nothing and says what would change, then the real save that
  // echoes back what the dry run saw (base and the stranded ids), so a rule that moved in between is caught.
  async function check(id, args, where) {
    O.edit = null; O.note = null;
    const res = await run('check', id, { ...args, dryRun: true });
    if (!res) return;
    if (res.impact) { O.edit = { id, args, where, impact: res.impact, message: res.message, ack: false, moved: false }; repaint(); return; }
    if (res.ok) { O.pol = null; await finish(res); return; }
    O.note = { kind: 'bad', text: plainFail(res) }; repaint();
  }
  async function save() {
    const e = O.edit; if (!e) return;
    const imp = e.impact || {};
    const res = await run('save', e.id, { ...e.args, confirm: true, base: imp.base || '', stranded: imp.stranded || [] });
    if (!res) return;
    if (res.ok) { O.edit = null; O.pol = null; await finish(res); return; }
    if (res.impact) { O.edit = { ...e, impact: res.impact, ack: false, moved: true }; O.note = { kind: 'warn', text: 'The rules changed while you were looking at this. Read the new effect below, then save again.' }; repaint(); return; }
    O.note = { kind: 'bad', text: plainFail(res) }; repaint();
  }
  const dropEdit = () => { O.edit = null; };

  // ---------- the queue ----------
  const FORK = 'Pull requests from forks never run on your machines.';
  const prLink = (r) => (r.number && r.repo ? `https://github.com/${r.repo}/pull/${r.number}` : '');
  function jobCell(r) {
    const l = prLink(r);
    const name = l ? `<a class="rl title" href="${esc(l)}" target="_blank" rel="noopener noreferrer" title="Open on GitHub">${esc(r.label)}</a>` : `<span class="title">${esc(r.label)}</span>`;
    const bits = [wfName(r.workflow), r.repo, r.sha ? short(r.sha) : ''].filter(Boolean).map(esc).join(' · ');
    const run = runIdForRequest(r);
    return `${name}<small class="mono">${bits}</small>${run ? `<a class="why" href="#/run/${encodeURIComponent(run)}" title="Open the run in Homestretch">Open run</a>` : ''}`;
  }
  const FIX = {
    'no-machine': ['Add a machine', 'machines'], 'no-labels': ['Add a machine', 'machines'], denied: ['Change the rule', 'rule'], deferred: ['Open holds', 'holds'], paused: ['Resume the queue', 'resume'],
  };
  function fixBtn(r) {
    const f = r.why && FIX[r.why.code]; if (!f) return '';
    if (f[1] === 'machines') return `<a class="btn sm" href="#/machines">${f[0]}</a>`;
    if (f[1] === 'holds') return `<a class="btn sm" href="#/holds">${f[0]}</a>`;
    if (f[1] === 'resume') return `<button type="button" class="btn sm" data-ops="resume">${ico('play')}${f[0]}</button>`;
    return `<button type="button" class="btn sm" data-ops="pol-open" data-project="${esc(r.repo)}">${f[0]}</button>`;
  }
  function queuedState(r, first, paused) {
    const c = r.why && r.why.code;
    if (paused || c === 'paused') return stw('pause', 'Paused', 'waiting', 'The whole queue is paused. Nothing new starts.');
    if (c === 'deferred') return stw('lock', 'On hold', 'waiting', 'A hold is active for this project.');
    if (r.waiting || ['busy', 'offline', 'draining', 'no-machine', 'no-labels', 'denied'].includes(c)) return stw('waiting', 'Waiting', 'waiting', 'No machine can take this job yet. The next column says why.');
    if (c === 'ready' && r.why.machines?.length && longWait(r)) return stw('waiting', 'Waiting', 'waiting', 'A slot is free but this job is still waiting.');
    if (first && c === 'ready') return stw('play', 'Starts next', 'running', 'A machine has a free slot for this job.');
    return stw('waiting', 'Queued', 'unknown', 'In line behind the jobs above it.');
  }
  function confirmCell(kind, id, text, yes, extra) {
    return `<div class="note"><b>${esc(text)}</b></div>${extra || ''}<div class="row-actions mt6"><button type="button" class="btn sm bad" data-ops="${kind}-yes" data-id="${esc(id)}">${esc(yes)}</button><button type="button" class="btn sm ghost" data-ops="confirm-no">Keep it</button></div>`;
  }
  const reasonBox = (label) => `<label class="sr" for="why-in">${esc(label)}</label><input id="why-in" class="in" type="text" maxlength="300" placeholder="${esc(label)} (optional)" data-in="reason" value="${esc(O.reason)}">`;
  function queueActions(r, running) {
    const c = O.confirm;
    if (c && c.kind === 'cancel' && c.id === r.id) return confirmCell('cancel', r.id, running ? 'Stop this running job?' : 'Take this job out of the queue?', 'Yes, cancel it', reasonBox('Why (goes in the activity log)'));
    const top = !running && r.priorityLabel !== 'First' ? `<button type="button" class="btn sm" data-ops="top" data-id="${esc(r.id)}" ${O.busy['top:' + r.id] ? 'disabled' : ''} title="Start this job before everything else waiting">${ico('up')}Move to top</button>` : '';
    return `<div class="row-actions">${top}<button type="button" class="btn sm" data-ops="cancel" data-id="${esc(r.id)}" title="${running ? 'Stop this job on its machine' : 'Remove this job from the queue'}">${ico('stop')}Cancel</button></div>`;
  }
  const prio = (r) => (r.priorityLabel === 'First' ? `<span class="chip prio first" title="Starts before the Normal jobs">${ico('up')}First</span>` : '<span class="chip prio" title="Starts in the usual order">Normal</span>');
  const queueControls = '<a href="#/queue?controls=1" title="Go to the Pause or Resume button">Queue controls</a>';
  const seeMachine = '<a href="#/machines">See machine</a>';

  function machineProblem(m) {
    const link = machineLink(m.name);
    if (m.state === 'gone') return `${link} is offline (${lastSeen(m)}). Start the Unravis agent on it.`;
    if (m.state === 'low-disk') return `${link} is not starting runs: disk is low. Free some disk space on it.`;
    if (m.state === 'draining') return `${link} is draining. It finishes its current jobs before it stops.`;
    if (m.state === 'paused') return `${link} is paused. Resume it to start new jobs.`;
    if (m.state === 'ready' && m.slots > 0 && m.running >= m.slots) return `${link} is full: ${esc(slotsBusy(m))}.`;
    return '';
  }
  function stoppedReporting(name, machines = reportedMachines()) {
    const m = machines.find((m) => m.name === name);
    return m?.state === 'gone' ? `<span class="why">Running on ${machineLink(name)}, but it stopped reporting (${lastSeen(m)}).</span>` : '';
  }
  function nextFree(m) {
    if (m?.state !== 'ready' || !Number.isInteger(m.running) || m.running < 0 || m.running >= m.slots) return '';
    return `<span class="why">Next free: ${slotLabel(m.running + 1, m.slots)}</span>`;
  }
  function nextFreeEstimate(machines) {
    const active = (ops().queue?.running || []).filter((r) => machines.some((m) => m.name === r.claimedBy));
    if (!active.length || machines.some((m) => !m.slots || active.filter((r) => r.claimedBy === m.name).length !== m.running)) return '';
    const estimates = active.map((r) => {
      const samples = (D.runs || []).filter((run) => run.kind === 'run' && run.agent === r.claimedBy && run.workflow === r.workflow &&
        ['passed', 'failed'].includes(run.status) && Number.isFinite(run.ms) && run.ms > 0).map((run) => run.ms).sort((a, b) => a - b);
      if (samples.length < 3) return NaN;
      const middle = Math.floor(samples.length / 2);
      const median = (samples[middle] + samples[Math.floor((samples.length - 1) / 2)]) / 2;
      return Date.parse(r.claimedAt) + median;
    });
    const eta = Math.min(...estimates);
    if (!Number.isFinite(eta) || eta <= Date.now()) return '';
    const time = new Date(eta);
    return ` <span title="Estimate from the median recorded duration of this workflow on this machine">Estimated next free slot ${at(time.toISOString())}.</span>`;
  }
  function waitReason(r, page) {
    const why = r.why;
    const names = why?.machines || [];
    const matches = reportedMachines().filter((m) => names.includes(m.name));
    const m = matches[0];
    let text = esc(why?.text ? cap(why.text) : r.deferNote || r.reason || (r.waiting ? 'No machine can take this yet.' : 'The queue has not reported why this run is waiting.'));
    if (ops().queue?.paused || why?.code === 'paused') {
      text = `The queue is paused. Nothing new starts. ${queueControls}`;
    } else if (why?.code === 'ready' && names.length) {
      const link = machineLink(names[0]);
      text = longWait(r) ? `Not starting though a slot is free on ${link}. This is longer than usual. Is the queue paused? ${queueControls}` : `Slot free on ${link}. Starts soon.`;
    } else if (why?.code === 'busy' && names.length) {
      text = `Waiting for a free slot on ${names.map((name) => {
        const machine = matches.find((m) => m.name === name);
        return machineLink(name) + (machine && Number.isInteger(machine.running) && machine.slots > 0 ? ` (${esc(slotsBusy(machine))})` : '');
      }).join(', ')}.${matches.length === names.length ? nextFreeEstimate(matches) : ''}`;
    } else if (why?.code === 'offline' && names.length) {
      text = `${names.map((name) => {
        const machine = matches.find((m) => m.name === name);
        return `${machineLink(name)} is offline${machine ? ` (${lastSeen(machine)})` : ''}. Nothing can start. Start the Unravis agent on it.`;
      }).join(' ')}${page === 'machines' ? '' : ` ${seeMachine}`}`;
    } else if (why?.code === 'draining' && matches.length === names.length && matches.length &&
        matches.every((m) => ['low-disk', 'draining', 'paused'].includes(m.state))) {
      text = `${matches.map(machineProblem).join(' ')}${page === 'machines' ? '' : ` ${seeMachine}`}`;
    }
    const minutes = waitMinutes(r);
    const age = Number.isFinite(minutes) ? ` · waiting ${minutes >= 60 ? `${Math.floor(minutes / 60)} h ` : ''}${minutes % 60} min` : '';
    return `<span class="why${why?.code === 'ready' && !longWait(r) ? ' ok' : ''}">${text}${esc(age)}</span>${page === 'queue' && why?.code === 'ready' && !ops().queue?.paused ? nextFree(m) : ''}`;
  }
  const waitForRun = (run) => waitReason(queueRequest(run) || { enqueuedAt: run.start, reason: run.reason });
  const statusResult = (status) => ['waiting', 'running'].includes(status) ? st('waiting', 'after run') : st(status, status);
  function queueCovers(r, verb) {
    const n = changeCount(r);
    if (n <= 1) return '';
    return `<details id="queue-covers-${esc(r.id)}" class="cover-more" data-cover><summary title="${esc(CHANGE_TITLE)}">${esc(verb)} ${esc(n)} changes${verb === 'Will check' ? ' together' : ''}</summary>${coverList(r)}</details>`;
  }
  function coverSummary(r, verb, from) {
    const n = changeCount(r);
    if (n <= 1) return '';
    const run = runIdForRequest(r);
    const href = from === 'queue' ? `#/queue?changes=${encodeURIComponent(r.id)}` : `#/run/${encodeURIComponent(run)}?changes=1`;
    return `<span title="${esc(CHANGE_TITLE)}">${esc(verb)} ${esc(n)} changes</span> · <a href="${esc(href)}">See which changes</a>`;
  }
  function runningRow(r, claims, machines) {
    const offline = machines.find((m) => m.name === r.claimedBy && m.state === 'gone');
    const where = `<span class="why">Running on ${placementHtml(machinePlacement(r.claimedBy, r, claims, machines)) || 'a machine'}${offline ? `, but it stopped reporting (${lastSeen(offline)})` : r.claimedAt ? `, started ${when(r.claimedAt)}` : ''}${r.attempts > 1 ? `, attempt ${esc(r.attempts)}` : ''}.</span>`;
    return `<tr><td class="w-st">${st('running', 'Running')}</td><td>${jobCell(r)}</td><td class="whyc">${where}${queueCovers(r, 'Checking')}</td><td class="hide-s" data-l="Priority">${prio(r)}</td><td class="num hide-s" data-l="Queued">${when(r.enqueuedAt)}</td><td>${queueActions(r, true)}</td></tr>`;
  }
  function queuedRow(r, first, paused) {
    const need = (r.labels || []).length ? `<span class="why">Needs a machine with: ${r.labels.map((x) => `<span class="mono">${esc(x)}</span>`).join(', ')}.</span>` : '';
    const fix = fixBtn(r);
    return `<tr><td class="w-st">${queuedState(r, first, paused)}</td><td>${jobCell(r)}</td><td class="whyc">${waitReason(r, 'queue')}${need}${queueCovers(r, 'Will check')}${fix ? `<div class="fixes">${fix}</div>` : ''}</td><td class="hide-s" data-l="Priority">${prio(r)}</td><td class="num hide-s" data-l="Queued">${when(r.enqueuedAt)}</td><td>${queueActions(r, false)}</td></tr>`;
  }
  const RESULT = { success: ['passed', 'Passed'], failure: ['failed', 'Failed'], error: ['failed', 'Error'], cancelled: ['stopped', 'Cancelled'], superseded: ['skipped', 'Replaced'] };
  function doneRow(r) {
    const [s, w] = RESULT[r.conclusion] || RESULT[r.state] || ['unknown', r.state || 'Done'];
    const c = O.confirm;
    const why = r.display || r.reason || '';
    const retry = r.runId ? (c && c.kind === 'retry' && c.id === r.runId
      ? confirmCell('retry', r.runId, 'Run this job again?', 'Yes, run it again')
      : `<button type="button" class="btn sm" data-ops="retry" data-id="${esc(r.runId)}" ${O.busy['retry:' + r.runId] ? 'disabled' : ''} title="Put this job back in the queue as a new run">${ico('rerun')}Retry</button>`) : '<span class="muted fs-xs" title="Only a job that made a run can be run again.">No run to repeat</span>';
    const covering = r.state === 'superseded' || r.conclusion === 'superseded' ? replacement(r) : '';
    return `<tr><td class="w-st">${st(s, w)}</td><td>${jobCell(r)}</td><td class="whyc">${why || !r.claimedBy ? `<span class="why">${esc(why) || '<span class="dim">nothing more recorded</span>'}</span>` : ''}${r.claimedBy ? `<span class="why">ran on ${machineLink(r.claimedBy)}</span>` : ''}${covering}${!covering && s !== 'skipped' ? queueCovers(r, 'Checked') : ''}</td><td class="num hide-s" data-l="Finished">${when(r.finishedAt)}</td><td>${retry}</td></tr>`;
  }
  function replacement(r) {
    if (r.state !== 'superseded' && r.conclusion !== 'superseded') return '';
    const q = ops().queue;
    const id = /^Replaced by a newer request \(([^()]+)\)$/.exec(r.display || '')?.[1] || r.supersededBy;
    const next = [...(q?.queued || []), ...(q?.running || []), ...(q?.done || [])].find((req) => req.id === id);
    if (!next?.sha || !r.sha || !next.covers?.some((c) => c.sha && short(c.sha) === short(r.sha) && (!r.number || c.number === r.number))) return '';
    const run = runIdForRequest(next);
    return `<span class="why">covered by run on <a class="mono" href="#/run/${encodeURIComponent(run)}?changes=1" title="Open the run that checks this commit">${esc(short(next.sha))}</a></span>`;
  }
  function viewQueue() {
    const lead = 'The jobs waiting for a machine, the ones running now, and the ones that just finished.';
    const b = blocked('queue', 'Queue', lead); if (b) return b;
    const q = ops().queue; const c = q.counts || {};
    const repos = [...new Set([...(q.running || []), ...(q.queued || []), ...(q.done || [])].map((r) => r.repo).filter(Boolean))];
    const repo = repos.includes(O.repo) ? O.repo : '';
    const f = (a) => (repo ? (a || []).filter((r) => r.repo === repo) : a || []);
    const run = f(q.running); const que = f(q.queued); const done = f(q.done);
    const right = q.paused
      ? `<button type="button" class="btn primary" id="queue-controls" data-ops="resume" ${O.busy['resume'] ? 'disabled' : ''} title="Let machines start jobs again">${ico('play')}Resume the queue</button>`
      : `<button type="button" class="btn" id="queue-controls" data-ops="pause" ${O.busy['pause'] ? 'disabled' : ''} title="Stop machines from starting new jobs. Jobs already running finish.">${ico('pause')}Pause the queue</button><span class="chip" title="Machines may start new jobs">Queue running</span>`;
    const firstReady = que.findIndex((r) => r.why?.code === 'ready');
    const claims = runningClaims(), machines = reportedMachines();
    const rows = run.map((r) => runningRow(r, claims, machines)).join('') + que.map((r, i) => queuedRow(r, i === firstReady, q.paused)).join('');
    const next = [...run, ...que, ...done].find((r) => changeCount(r) > 1 && r.state !== 'superseded' && r.conclusion !== 'superseded');
    const summary = next ? coverSummary(next, next.state === 'queued' ? 'The next run will check' : next.state === 'running' ? 'This run is checking' : 'This run checked', 'queue') : '';
    const none = '<div class="empty-big"><h2>Nothing is waiting or running.</h2><p>When a pull request or a push needs checking, it appears here, with the reason if it has to wait.</p></div>';
    return `${head('Queue', lead, right)}${noteHtml()}${staleNote('queue')}
    <div class="nowline"><span>${stw('running', plural(c.running || 0, 'running', 'running'), 'running')}</span><span>${stw('waiting', (c.queued || 0) + ' queued', 'waiting')}</span><span><b>${c.waiting || 0} waiting</b> for a machine</span><span class="fork">${esc(FORK)}</span></div>
    ${q.paused ? `<div class="warnbox" role="status">${ico('pause')}<div class="wb"><b>The queue is paused.</b> No machine starts a new job until it is resumed.<p>It was paused with a pause file on this PC. Resuming removes that file.</p>${q.pausedReason ? `<p class="ev mono">${esc(q.pausedReason)}</p>` : ''}</div></div>` : ''}
    ${summary ? `<p class="note">${summary}</p>` : ''}
    ${repos.length > 1 ? `<div class="chips qf" role="group" aria-label="Show jobs for">${[''].concat(repos).map((x) => `<button type="button" class="chipbtn" data-ops="repo" data-repo="${esc(x)}" aria-pressed="${x === repo}">${x ? esc(x) : 'All projects'}</button>`).join('')}</div>` : ''}
    <section class="sec"><div class="sh"><h2>Now and next</h2><span class="muted">running first, then in the order they will start</span></div>
    ${rows ? `<table class="tbl qt"><thead><tr><th class="w-st">State</th><th>Job</th><th>Where it is, or why it waits</th><th class="hide-s">Priority</th><th class="num hide-s">Queued</th><th>Actions</th></tr></thead><tbody>${rows}</tbody></table>` : none}</section>
    <section class="sec"><div class="sh"><h2>Recently finished</h2><span class="muted">the latest ${done.length} of this project’s finished jobs</span></div>
    ${done.length ? `<table class="tbl qt"><thead><tr><th class="w-st">Result</th><th>Job</th><th>What happened</th><th class="num hide-s">Finished</th><th>Actions</th></tr></thead><tbody>${done.map(doneRow).join('')}</tbody></table>` : '<p class="empty">No job has finished here yet.</p>'}</section>
    <p class="note mt">Pull requests start before pushes, and pushes before scheduled runs. Move to top puts a job ahead of everything waiting. Cancel and Retry are written to the Activity tab.</p>
    ${cli('unravis-ci queue top <id|pr>', 'The same buttons as commands: unravis-ci cancel <id|pr> [--reason R], unravis-ci rerun <run id>, unravis-ci queue pause|resume.')}`;
  }

  // ---------- who may build what (machine rules) ----------
  const pol = () => ops().policy;
  const modeText = { any: 'Any machine', only: 'Only these machines', except: 'Any machine except these' };
  const allowed = (p, name) => p.mode === 'any' || (p.mode === 'only' ? (p.machines || []).includes(name) : !(p.machines || []).includes(name));
  const polMachine = (name) => ((pol() && pol().machines) || []).find((m) => m.name === name) || null;
  const MSTATE = { ready: ['passed', 'Ready'], paused: ['stopped', 'Paused'], draining: ['waiting', 'Draining'], 'low-disk': ['failed', 'Low disk'], gone: ['stopped', 'Not running'] };
  const mst = (m) => { const [c, w] = MSTATE[m.state] || ['unknown', m.state || 'Unknown']; return st(c, w); };
  function impactBox(e) {
    const i = e.impact || {}; const n = i.strandedCount || (i.stranded || []).length;
    const ids = (i.stranded || []).slice(0, 8).map((x) => `<span class="chip mono">${esc(x)}</span>`).join(' ') + ((i.stranded || []).length > 8 ? ` <span class="muted">and ${(i.stranded || []).length - 8} more</span>` : '');
    if (n) return `<div class="warnbox bad in" role="alert">${ico('failed')}<div class="wb"><b>${plural(n, 'queued job would have', 'queued jobs would have')} no machine.</b><p>They would stay in the queue until a rule changes again.</p><p class="chips">${ids}</p>
      <label class="chk"><input type="checkbox" data-in="ack" ${e.ack ? 'checked' : ''}> <span>I understand they will wait until I change this again.</span></label></div></div>`;
    return `<div class="warnbox ok in" role="status">${ico('passed')}<div class="wb"><b>No queued job loses its machine.</b>${i.line ? `<p>${esc(i.line)}</p>` : ''}</div></div>`;
  }
  function editPanel(where, saveLabel) {
    const e = O.edit; if (!e || e.where !== where) return '';
    const n = (e.impact && (e.impact.strandedCount || (e.impact.stranded || []).length)) || 0;
    return `${e.moved ? '' : ''}${impactBox(e)}<div class="row-actions mt8"><button type="button" class="btn primary" data-ops="save" ${n && !e.ack ? 'disabled' : ''} ${O.busy.save ? 'disabled' : ''}>${esc(saveLabel)}</button><button type="button" class="btn ghost" data-ops="edit-cancel">Cancel</button></div>`;
  }
  function polCli(p) {
    const list = (p.machines || []).length ? p.machines.join(',') : '<machine>';
    return `unravis-ci policy set ${p.project} ${p.mode === 'any' ? '--any' : p.mode === 'only' ? '--only ' + list : '--except ' + list}`;
  }
  function polEditor() {
    const p = O.pol; if (!p) return '';
    const ms = (pol().machines || []);
    const emptyOnly = p.mode === 'only' && !p.machines.length;
    return `<div class="pedit" role="group" aria-label="Machine rule for ${esc(p.project)}"><div class="sh"><h2>Rule for ${esc(p.project)}</h2><button type="button" class="btn sm ghost end" data-ops="pol-close">Close</button></div>
      <p class="note">Labels decide whether a machine can run a job. This rule decides whether it may.</p>
      <div class="fld"><div class="lb" id="pm-l">Which machines may build this project?</div><div class="chips" role="group" aria-labelledby="pm-l">${['any', 'only', 'except'].map((m) => `<button type="button" class="chipbtn" data-ops="pol-mode" data-mode="${m}" aria-pressed="${p.mode === m}">${modeText[m]}</button>`).join('')}</div></div>
      <div class="fld"><div class="lb" id="pm-m">${p.mode === 'except' ? 'Keep these machines out' : 'Machines'}</div>${ms.length ? `<div class="chips" role="group" aria-labelledby="pm-m">${ms.map((m) => `<button type="button" class="chipbtn" data-ops="pol-mach" data-name="${esc(m.name)}" aria-pressed="${p.machines.includes(m.name)}" ${p.mode === 'any' ? 'disabled' : ''}>${esc(m.name)}</button>`).join('')}</div>` : '<p class="note">No machine has reported yet, so there is nothing to pick.</p>'}${p.mode === 'any' ? '<p class="h">Every machine may build it. Pick Only these machines, or Any machine except these, to choose.</p>' : ''}</div>
      ${emptyOnly ? `<div class="warnbox bad" role="status">${ico('failed')}<div class="wb">Pick at least one machine, or choose Any machine. A project with no machine can never build.</div></div>` : ''}
      ${editPanel('pol', 'Save the rule') || `<div class="row-actions"><button type="button" class="btn primary" data-ops="pol-check" ${emptyOnly || O.busy.check ? 'disabled' : ''} title="Shows what this would do. Nothing is saved yet.">Check what this changes</button><button type="button" class="btn ghost" data-ops="pol-close">Cancel</button></div>`}
      ${cli(polCli(p) + ' --dry-run', 'Run it with --dry-run to see the effect, then again with --confirm to save.')}</div>`;
  }
  function matrix() {
    const o = ops();
    if (o.old) return '';
    const p = pol();
    if (!p) return `<div class="empty">${esc(o.err.policy || 'The machine rules have not been read yet.')}</div>`;
    if (!p.ciFound) return '';
    const ms = p.machines || []; const ps = p.projects || [];
    const body = ps.map((x) => `<tr><td class="hd">${esc(x.project)}<div class="ev">${esc(x.text || modeText[x.mode] || '')}${x.explicit ? '' : ' (the default)'}</div>${x.error ? `<div class="ev c-failed">${esc(x.error)}</div>` : ''}</td>${ms.map((m) => `<td>${allowed(x, m.name) ? st('passed', 'Allowed') : st('stopped', 'Not allowed')}</td>`).join('')}<td><button type="button" class="btn sm" data-ops="pol-open" data-project="${esc(x.project)}">Change</button></td></tr>`).join('');
    return `<section class="sec" id="rules"><div class="sh"><h2>Who may build what</h2><span class="muted">one rule for each project</span></div>
      <p class="note">Fit and permission are separate. A machine needs the right labels to be able to run a job, and the rule below to be allowed. ${esc(FORK)}</p>
      ${ps.length && ms.length ? `<div class="tw"><table class="dtbl pm"><thead><tr><th>Project</th>${ms.map((m) => `<th><button type="button" class="thbtn" data-ops="drawer-open" data-name="${esc(m.name)}" title="Drain or resume ${esc(m.name)}, and choose which projects it may build">${esc(m.name)}</button><div class="ev">${esc((MSTATE[m.state] || [0, m.state])[1])}</div></th>`).join('')}<th><span class="sr">Change</span></th></tr></thead><tbody>${body}</tbody></table></div>` : `<div class="empty"><b>${ms.length ? 'No project is listed.' : 'No machine has reported yet.'}</b> ${ms.length ? '' : 'Once a machine runs unravis-ci serve, you can choose who builds what here.'}</div>`}
      ${polEditor()}</section>`;
  }

  // ---------- the machine drawer ----------
  function drainBox(m) {
    const c = O.confirm;
    if (m.drainAsked || m.state === 'draining') return `<div class="note"><b>This machine has been asked to drain.</b> It finishes what it is running and takes nothing new.</div><div class="row-actions mt8"><button type="button" class="btn" data-ops="m-resume" data-name="${esc(m.name)}" ${O.busy['mres:' + m.name] ? 'disabled' : ''}>${ico('play')}Resume this machine</button></div><p class="note">Resume only works before the machine has stopped. After that, start it again with <span class="mono">unravis-ci serve</span>.</p>`;
    if (c && c.kind === 'drain' && c.id === m.name) return `<div class="warnbox" role="status">${ico('waiting')}<div class="wb"><b>Drain ${esc(m.name)}?</b><p>It finishes the ${plural(m.running || 0, 'job', 'jobs')} it is running, takes nothing new, then stops. To bring it back after that, start it again with <span class="mono">unravis-ci serve</span>.</p></div></div><div class="row-actions"><button type="button" class="btn warn" data-ops="m-drain-yes" data-name="${esc(m.name)}">Drain it</button><button type="button" class="btn ghost" data-ops="confirm-no">Keep it running</button></div>`;
    return `<div class="row-actions"><button type="button" class="btn" data-ops="m-drain" data-name="${esc(m.name)}" ${m.state === 'gone' ? 'disabled' : ''} title="Finish the current job, take nothing new, then stop">${ico('pause')}Drain this machine</button></div><p class="note">Use Drain to take a machine out of service without losing a job.</p>`;
  }
  function drawerHtml() {
    if (!O.drawer || ops().old || !pol()) return '';
    const m = polMachine(O.drawer);
    if (!m) return '';
    const ps = pol().projects || [];
    const rows = ps.map((x) => {
      const ok = allowed(x, m.name);
      return `<tr><td class="hd">${esc(x.project)}<div class="ev">${esc(x.text || '')}</div></td><td>${ok ? st('passed', 'Allowed') : st('stopped', 'Not allowed')}</td><td><button type="button" class="btn sm" data-ops="${ok ? 'm-deny' : 'm-allow'}" data-name="${esc(m.name)}" data-project="${esc(x.project)}" ${O.busy.check ? 'disabled' : ''}>${ok ? 'Deny' : 'Allow'}</button></td></tr>`;
    }).join('');
    const e = O.edit && O.edit.where === 'drawer' ? O.edit : null;
    return `<div class="scrim under" data-ops="drawer-close"></div><aside class="drawer" role="dialog" aria-modal="true" aria-labelledby="dr-t"><header><div class="minw0"><h2 id="dr-t" class="mono">${esc(m.name)}</h2><div class="mt6">${mst(m)} <span class="muted fs-xs">heard from ${esc(m.seenAgo || 'not recorded')}</span></div></div><button type="button" class="btn ghost icon" data-ops="drawer-close" aria-label="Close" title="Close">${ico('x')}</button></header>
      <div class="db">${noteHtml()}
        <dl class="kv"><dt>Slots</dt><dd>${m.running || 0} of ${m.slots || 0} in use</dd><dt>Labels</dt><dd>${(m.labels || []).length ? m.labels.map((l) => `<span class="chip mono">${esc(l)}</span>`).join(' ') : '<span class="muted">none reported</span>'}</dd></dl>
        <div class="sh"><h2>Take it out of service</h2></div><div class="fld">${drainBox(m)}</div>
        <div class="sh"><h2>Projects this machine may build</h2></div>
        ${ps.length ? `<table class="dtbl"><thead><tr><th>Project</th><th>Now</th><th><span class="sr">Change</span></th></tr></thead><tbody>${rows}</tbody></table>` : '<p class="note">No project is listed.</p>'}
        ${e ? editPanel('drawer', e.id === 'machines.allow' ? 'Allow it' : 'Deny it') : ''}
        <p class="note mt">${esc(FORK)} Allow and Deny change the project’s rule, and the effect is checked before anything is saved.</p>
        ${cli(`unravis-ci machines ${allowed(ps[0] || { mode: 'any' }, m.name) ? 'deny' : 'allow'} ${m.name} --project ${(ps[0] && ps[0].project) || 'owner/name'}`, 'Drain and resume as commands: unravis-ci drain --name N, unravis-ci machines resume N.')}</div></aside>`;
  }
  // The cell at the end of each machine card on the Machines page.
  function machineCell(x) {
    const o = ops();
    if (o.old) return '<span class="muted fs-xs">Drain and rules need a newer unravis-releases.</span>';
    const all = (pol() && pol().machines) || [];
    const m = polMachine(x.id) || (x.self ? (polMachine('local') || (all.length === 1 ? all[0] : null)) : null);
    if (!m) return `<span class="muted fs-xs">${o.loaded ? 'Rules for this machine are in Who may build what, below.' : 'Reading the rules…'}</span>`;
    const drain = m.drainAsked || m.state === 'draining' ? `<span class="chip warn">${ico('waiting')}Draining</span> ` : '';
    return `<div class="row-actions">${drain}<button type="button" class="btn sm" data-ops="drawer-open" data-name="${esc(m.name)}" title="Drain or resume this machine, and choose which projects it may build">${ico('pc')}Rules and drain</button></div>`;
  }
  const machinesExtra = () => `${O.drawer ? '' : noteHtml()}${matrix()}${drawerHtml()}`;

  // ---------- holds ----------
  const HSTATE = { active: ['lock', 'Active', 'waiting'], upcoming: ['clock', 'Starts later', 'unknown'], lifted: ['stopped', 'Lifted', 'stopped'], ended: ['passed', 'Ended', 'passed'] };
  const projName = (id) => { if (!id || id === 'all') return 'All projects'; const p = (D.projects || []).find((x) => x.id === id); return p ? p.name : id; };
  function holdRow(h, kind) {
    const [ic, w, cls] = HSTATE[kind]; const c = O.confirm;
    const who = h.liftedBy ? ` · lifted by ${esc(h.liftedBy)}${h.liftReason ? ': ' + esc(h.liftReason) : ''}` : '';
    let act = '';
    if (kind === 'active' || kind === 'upcoming') {
      act = c && c.kind === 'lift' && c.id === h.id
        ? confirmCell('lift', h.id, kind === 'active' ? 'Lift this hold now?' : 'Cancel this hold?', kind === 'active' ? 'Yes, lift it' : 'Yes, cancel it', reasonBox('Why'))
        : `<button type="button" class="btn sm" data-ops="lift" data-id="${esc(h.id)}" ${O.busy['lift:' + h.id] ? 'disabled' : ''} title="${kind === 'active' ? 'End the hold now, so Promote is allowed again' : 'Remove this hold before it starts'}">${kind === 'active' ? 'Lift it' : 'Cancel it'}</button>`;
    }
    return `<tr><td class="w-st">${stw(ic, w, cls)}</td><td data-l="Project">${esc(projName(h.project))}</td><td data-l="Why">${esc(h.reason)}<div class="ev">Owner: ${esc(h.owner || 'not recorded')}${who}</div></td><td class="num" data-l="${kind === 'lifted' || kind === 'ended' ? 'Started' : 'Starts'}">${at(h.start)}</td><td class="num" data-l="${kind === 'lifted' ? 'Lifted' : kind === 'ended' ? 'Ended' : 'Ends'}">${at(kind === 'lifted' ? (h.liftedAt || h.end) : h.end)}</td><td>${act}</td></tr>`;
  }
  const HOURS = [['1h', 'An hour'], ['4h', '4 hours'], ['12h', '12 hours'], ['24h', 'A day'], ['3d', '3 days'], ['7d', 'A week'], ['30d', '30 days']];
  function holdForm() {
    const f = O.hold; const proj = f.project || D.pid;
    return `<form class="holdform" data-ops="hold-form" novalidate><div class="fld"><label for="hp">Which project?</label><select id="hp" class="in" data-in="hold-project">${[['all', 'All projects']].concat((D.projects || []).map((p) => [p.id, p.name])).map(([v, n]) => `<option value="${esc(v)}" ${v === proj ? 'selected' : ''}>${esc(n)}</option>`).join('')}</select></div>
      <div class="fld"><label for="hh">For how long?</label><select id="hh" class="in" data-in="hold-hours">${HOURS.map(([v, n]) => `<option value="${v}" ${v === f.hours ? 'selected' : ''}>${n}</option>`).join('')}</select></div>
      <div class="fld grow"><label for="hr">Why?</label><input id="hr" class="in" type="text" maxlength="300" data-in="hold-reason" placeholder="For example: demo on Friday, nothing new until it is over" value="${esc(f.reason)}" autocomplete="off"></div>
      <button type="submit" class="btn primary" ${O.busy.hold ? 'disabled' : ''}>${ico('lock')}Start the hold</button></form>`;
  }
  function viewHolds() {
    const lead = 'A hold stops Promote for a project until a set time. Use it before a demo or a freeze.';
    const b = blocked('holds', 'Holds', lead); if (b) return b;
    const h = ops().holds;
    const act = h.active || []; const up = h.upcoming || []; const past = (h.lifted || []).concat(h.ended || []);
    const top = act.length
      ? `<div class="warnbox" role="status">${ico('lock')}<div class="wb">${act.map((x) => `<p><b>Promote is held for ${esc(projName(x.project))}</b> until ${until(x.end)}. ${esc(x.reason)} Owner: ${esc(x.owner || 'not recorded')}.</p>`).join('')}</div></div>`
      : `<div class="warnbox ok" role="status">${ico('passed')}<div class="wb"><b>No hold is active.</b> Promotion is allowed.</div></div>`;
    const rows = act.map((x) => holdRow(x, 'active')).concat(up.map((x) => holdRow(x, 'upcoming'))).join('');
    return `${head('Holds', lead)}${noteHtml()}${staleNote('holds')}${top}
    <section class="sec"><div class="sh"><h2>Holds that matter now</h2><span class="muted">active, and starting later</span></div>
    ${rows ? `<table class="tbl qt"><thead><tr><th class="w-st">State</th><th>Project</th><th>Why</th><th class="num">Starts</th><th class="num">Ends</th><th>Actions</th></tr></thead><tbody>${rows}</tbody></table>` : '<p class="empty">No hold is active or planned.</p>'}</section>
    <section class="sec"><div class="sh"><h2>Start a hold</h2></div>${holdForm()}
      <p class="note mt8">A hold only stops Promote (moving a build from rc to stable). Checks, the queue and ordinary releases keep running. It ends by itself at the time you pick, or you can lift it early here.</p>
      ${cli('unravis-ci hold add --project <id> --until 4h --reason "demo on Friday"', 'Lift one early with: unravis-ci hold lift <id> --reason R')}</section>
    ${past.length ? `<section class="sec"><div class="sh"><h2>Earlier holds</h2><span class="muted">lifted or ended</span></div><table class="tbl qt"><thead><tr><th class="w-st">State</th><th>Project</th><th>Why</th><th class="num">Started</th><th class="num">Ended or lifted</th><th><span class="sr">Actions</span></th></tr></thead><tbody>${(h.lifted || []).map((x) => holdRow(x, 'lifted')).join('')}${(h.ended || []).map((x) => holdRow(x, 'ended')).join('')}</tbody></table></section>` : ''}`;
  }

  // ---------- activity ----------
  const ACTORS = [['all', 'Everyone', ''], ['ui', 'This screen', 'ui'], ['cli', 'Command line', 'cli'], ['claude', 'Claude', 'claude']];
  function viewActivity() {
    const lead = 'Every change made to the queue, machines, rules and holds: who did it, from where, and why.';
    const b = blocked('activity', 'Activity', lead); if (b) return b;
    const a = ops().activity; const all = (a.entries || []).slice().reverse();
    const n = (k) => (k === 'all' ? all.length : all.filter((e) => e.actor === k).length);
    const list = O.actor === 'all' ? all : all.filter((e) => e.actor === O.actor);
    const row = (e) => `<tr><td class="num" data-l="When">${when(e.time)}</td><td data-l="From"><span class="chip actor-${esc(e.actor)}">${esc((ACTORS.find((x) => x[0] === e.actor) || [0, e.actor])[1])}</span></td><td data-l="Who">${esc(e.person || '') || '<span class="dim">not recorded</span>'}</td>
      <td class="act">${esc(actionWord(e.action))}${e.outcome && e.outcome !== 'done' ? ` <span class="chip warn">${esc(e.outcome)}</span>` : ''}</td><td class="mono" data-l="On">${esc(e.target || '')}</td><td data-l="Why">${esc(e.reason || '')}${e.detail ? `<span class="ev">${esc(e.detail)}</span>` : ''}</td></tr>`;
    return `${head('Activity', lead)}${staleNote('activity')}
    <div class="chips qf" role="group" aria-label="Show actions from">${ACTORS.map(([k, l]) => `<button type="button" class="chipbtn" data-ops="actor" data-actor="${k}" aria-pressed="${O.actor === k}">${l}<span class="n">${n(k)}</span></button>`).join('')}</div>
    ${list.length ? `<table class="tbl qt"><thead><tr><th class="num">When</th><th>From</th><th>Who</th><th>Action</th><th>On</th><th>Why</th></tr></thead><tbody>${list.map(row).join('')}</tbody></table>`
      : `<div class="empty-big"><h2>${all.length ? 'Nothing from that source.' : 'No change has been made yet.'}</h2><p>${all.length ? 'Pick Everyone to see all of it.' : 'When someone pauses the queue, moves a job, changes a rule or starts a hold, a line appears here.'}</p></div>`}
    ${a.skipped ? `<p class="note mt8">${plural(a.skipped, 'line', 'lines')} of the log could not be read and ${a.skipped === 1 ? 'was' : 'were'} left out.</p>` : ''}
    <p class="note mt8">This list is read-only, newest first.</p>`;
  }

  // ---------- small pieces other pages use ----------
  // Overview: a line when Promote is held.
  function holdLine() {
    const h = ops().holds; const act = (h && h.active) || [];
    if (!act.length) return '';
    return `<div class="warnbox" role="status">${ico('lock')}<div class="wb">${act.map((x) => `<p><b>Promote is held</b> by ${esc(x.owner || 'someone')} until ${until(x.end)}${x.project && x.project !== 'all' ? ' for ' + esc(projName(x.project)) : ''}. ${esc(x.reason)}</p>`).join('')}<p><a href="#/holds">See the hold</a></p></div></div>`;
  }
  function queueCard() {
    const o = ops(); const q = o.queue;
    if (o.old || !q || !q.ciFound) return '';
    const c = q.counts || {};
    const claims = runningClaims(), machines = reportedMachines();
    const next = q.queued?.[0];
    const covered = [...(q.running || []), ...(q.queued || []), ...(q.done || [])]
      .find((r) => changeCount(r) > 1 && r.state !== 'superseded' && r.conclusion !== 'superseded');
    const active = (q.running || []).map((r) => placementHtml(machinePlacement(r.claimedBy, r, claims, machines))).filter(Boolean);
    const capacity = machines.map((m) => machineProblem(m) || (Number.isInteger(m.running) && m.slots > 0
      ? `${machineLink(m.name)}: ${esc(slotsBusy(m))}` : '')).filter(Boolean);
    return `<div class="card"><h3>Queue <a href="#/queue">Open the queue</a></h3><p><b>${esc(c.running || 0)}</b> running, <b>${esc(c.queued || 0)}</b> queued${c.waiting ? `, <b>${esc(c.waiting)}</b> waiting for a machine` : ''}.${q.paused && !next ? ' The queue is paused. Nothing new starts.' : ''}</p>
      ${active.length || capacity.length ? `<p title="A machine is a PC that runs checks. Each slot runs one job at a time. ${SLOT_TITLE}">${[...active, ...capacity].join(' · ')}</p>` : ''}
      ${next || covered ? `<p>${next ? waitReason(next) : ''}${covered ? coverSummary(covered, covered.state === 'queued' ? 'Next run checks' : covered.state === 'running' ? 'Checking' : 'Checked') : ''}</p>` : ''}</div>`;
  }
  // The numbers on the tabs. '' while there is nothing to say.
  function counts() {
    const o = ops(); const q = o.queue; const h = o.holds; const a = o.activity;
    return {
      queue: q && q.ciFound ? (q.counts.queued || 0) + (q.counts.running || 0) : '',
      holds: h && h.ciFound ? (h.active || []).length + (h.upcoming || []).length : '',
      activity: a && a.ciFound ? (a.entries || []).length : '',
    };
  }

  // ---------- events ----------
  const val = (el) => el.value;
  function click(e) {
    const t = e.target.closest('[data-ops]'); if (!t || t.tagName === 'FORM') return;
    const k = t.dataset.ops; const id = t.dataset.id; const name = t.dataset.name; const project = t.dataset.project;
    if (t.tagName === 'A') return;
    switch (k) {
      case 'dismiss': O.note = null; return repaint();
      case 'repo': O.repo = t.dataset.repo; return repaint();
      case 'actor': O.actor = t.dataset.actor; return repaint();
      case 'pause': return simple('pause', 'queue.pause', {});
      case 'resume': return simple('resume', 'queue.resume', {});
      case 'top': return simple('top:' + id, 'queue.top', { id });
      case 'cancel': O.confirm = { kind: 'cancel', id }; O.reason = ''; return repaint();
      case 'cancel-yes': return simple('cancel:' + id, 'queue.cancel', O.reason.trim() ? { id, reason: O.reason.trim() } : { id });
      case 'retry': O.confirm = { kind: 'retry', id }; return repaint();
      case 'retry-yes': return simple('retry:' + id, 'queue.retry', { run: id });
      case 'confirm-no': O.confirm = null; return repaint();
      case 'lift': O.confirm = { kind: 'lift', id }; O.reason = ''; return repaint();
      case 'lift-yes': return simple('lift:' + id, 'hold.lift', O.reason.trim() ? { id, reason: O.reason.trim() } : { id });
      case 'drawer-open': O.drawer = name; O.edit = null; O.confirm = null; O.note = null; return repaint();
      case 'drawer-close': O.drawer = null; O.edit = null; O.confirm = null; return repaint();
      case 'm-drain': O.confirm = { kind: 'drain', id: name }; return repaint();
      case 'm-drain-yes': return simple('mdrain:' + name, 'machines.drain', { name });
      case 'm-resume': return simple('mres:' + name, 'machines.resume', { name });
      case 'm-allow': return check('machines.allow', { name, project }, 'drawer');
      case 'm-deny': return check('machines.deny', { name, project }, 'drawer');
      case 'pol-open': {
        const p = ((pol() && pol().projects) || []).find((x) => x.project === project);
        O.pol = { project, mode: p ? p.mode : 'any', machines: p ? (p.machines || []).slice() : [] }; O.edit = null; O.note = null;
        if (route().name !== 'machines') { go('/machines'); return; }
        repaint(); const r = $('#rules'); if (r && r.scrollIntoView) r.scrollIntoView({ block: 'start' }); return;
      }
      case 'pol-close': O.pol = null; O.edit = null; return repaint();
      case 'pol-mode': O.pol.mode = t.dataset.mode; if (O.pol.mode === 'any') O.pol.machines = []; dropEdit(); return repaint();
      case 'pol-mach': { const a = O.pol.machines; const i = a.indexOf(name); if (i >= 0) a.splice(i, 1); else a.push(name); dropEdit(); return repaint(); }
      case 'pol-check': return check('policy.set', { project: O.pol.project, mode: O.pol.mode, machines: O.pol.machines.slice() }, 'pol');
      case 'edit-cancel': O.edit = null; return repaint();
      case 'save': return save();
      default:
    }
  }
  function input(e) {
    const t = e.target; const k = t.dataset && t.dataset.in; if (!k) return;
    if (k === 'reason') O.reason = val(t);
    else if (k === 'hold-reason') O.hold.reason = val(t);
    else if (k === 'hold-hours') O.hold.hours = val(t);
    else if (k === 'hold-project') O.hold.project = val(t);
    else if (k === 'ack') { if (O.edit) O.edit.ack = !!t.checked; const b = $('[data-ops="save"]'); if (b) b.disabled = !!O.busy.save || !t.checked; }
  }
  function submit(e) {
    const f = e.target.closest('[data-ops="hold-form"]'); if (!f) return;
    e.preventDefault();
    const h = O.hold; const project = h.project || D.pid;
    if (!h.reason.trim()) { O.note = { kind: 'bad', text: 'Say why, in a few words. The reason is shown on the Overview while the hold is active.' }; repaint(); return; }
    const args = project === 'all' ? { all: true, until: h.hours, reason: h.reason.trim() } : { project, until: h.hours, reason: h.reason.trim() };
    run('hold', 'hold.add', args).then((res) => { if (!res) return; if (res.ok) O.hold.reason = ''; return finish(res, 'Hold started. Promote is stopped until the time shown below. You can lift it from the list at any time.'); });
  }
  function copy(e) {
    const t = e.target.closest('[data-ocopy]'); if (!t) return;
    navigator.clipboard.writeText(t.dataset.ocopy).then(() => toast('Command copied.'), () => toast('Could not copy.'));
  }
  // One set of handlers on #main, set again after each draw (the element itself stays).
  function bind() {
    const m = $('#main'); if (!m) return;
    m.onclick = (e) => { copy(e); click(e); };
    m.oninput = input; m.onchange = input; m.onsubmit = submit;
  }
  // Esc closes the drawer first, then the rule editor. True when it closed something.
  function escape() {
    if (O.drawer) { O.drawer = null; O.edit = null; O.confirm = null; repaint(); return true; }
    if (O.pol) { O.pol = null; O.edit = null; repaint(); return true; }
    return false;
  }
  // A new page: forget what was half done there.
  function reset() { O.confirm = null; O.note = null; O.edit = null; O.drawer = null; O.reason = ''; }

  return { viewQueue, viewHolds, viewActivity, machineCell, machinesExtra, holdLine, queueCard, counts, bind, escape, reset, state: O, allowed, actionWord, plainFail,
    waitReason, waitForRun, machineProblem, stoppedReporting, nextFree, statusResult, replacement };
})();
