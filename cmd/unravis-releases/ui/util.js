'use strict';
// Small shared helpers: selectors, escaping, icons, status words, time and duration text, toasts.
// Everything that writes HTML goes through esc() or a number, so no page text is ever read as markup.
const $ = (sel, root) => (root || document).querySelector(sel);
const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

const WORD = { passed: 'Passed', failed: 'Failed', running: 'Running', waiting: 'Waiting', stopped: 'Stopped', skipped: 'Skipped', unknown: 'Not recorded here' };
// The server says "not-recorded"; the page says "unknown".
const norm = (s) => (s === 'not-recorded' ? 'unknown' : s || 'unknown');

const ico = (n, cls) => `<svg class="ic ${cls || ''}" aria-hidden="true"><use href="#i-${n}"/></svg>`;
const sic = (s, cls) => { s = norm(s); return `<span class="sic c-${s} ${cls || ''}" title="${esc(WORD[s] || s)}">${ico(s, s === 'running' ? 'spin' : '')}<span class="sr">${esc(WORD[s] || s)}</span></span>`; };
const st = (s, label) => { s = norm(s); return `<span class="st c-${s}" title="${esc(WORD[s] || s)}">${ico(s, s === 'running' ? 'spin' : '')}<span class="st-l">${esc(label || WORD[s] || s)}</span></span>`; };

const short = (sha) => (sha ? String(sha).slice(0, 7) : '');
const WF = { 'fast-gate.yml': 'Fast gate', 'ci.yml': 'Full CI' };
const wfName = (w) => WF[w] || String(w || '').replace(/\.ya?ml$/, '').replace(/[-_]/g, ' ').replace(/^./, (c) => c.toUpperCase());
const exact = (t) => { const d = new Date(t); return isNaN(d) ? '' : d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' }); };

const rel = (t) => {
  const d = new Date(t).getTime(); if (!d) return 'not recorded';
  const s = Math.max(0, (Date.now() - d) / 1000);
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  const n = Math.round(s / 86400); return `${n} day${n === 1 ? '' : 's'} ago`;
};
const when = (t) => (t ? `<time datetime="${esc(t)}" title="${esc(exact(t))}">${esc(rel(t))}</time>` : '<span class="dim">not recorded</span>');
const dur = (ms) => {
  if (ms == null || !isFinite(ms) || ms < 0) return '–';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ${s % 60} s`;
  return `${Math.floor(m / 60)} h ${m % 60} min`;
};
const subject = (r) => (r.pr ? `PR ${r.pr}` : `Push to ${r.branch || 'a branch'}`);

const SLOT_TITLE = 'Slots are numbered in the order jobs started.';
const CHANGE_TITLE = 'A change is a pull request that was merged into the branch. One run can check several changes together.';
const machineLink = (name) => `<a class="mono" href="#/machines" title="See the machines that run checks">${esc(name)}</a>`;
const slotLabel = (slot, slots) => `<span title="${SLOT_TITLE}">slot ${esc(slot)} of ${esc(slots)}</span>`;
const slotSquares = (slots, busy) => `<span class="slots" aria-hidden="true">${Array.from({ length: slots }, (_, i) => `<i${i < busy ? ' class="busy"' : ''}></i>`).join('')}</span>`;
const slotsBusy = (m) => `${m.running} of ${m.slots} slots busy`;
const slotCount = (n) => Number.isInteger(n) && n > 0 ? n : 0;
const minutesSince = (time) => Math.max(0, Math.floor((Date.now() - Date.parse(time)) / 60_000));
const waitMinutes = (r) => minutesSince(r.enqueuedAt);
const longWait = (r) => Date.now() - Date.parse(r.enqueuedAt) > 30 * 60_000;

function slotOf(request, allRunning) {
  if (!request.id || !request.claimedBy) return null;
  const jobs = allRunning.filter((job) => job.claimedBy === request.claimedBy);
  if (jobs.some((job) => !Number.isFinite(Date.parse(job.claimedAt)))) return null;
  jobs.sort((a, b) => Date.parse(a.claimedAt) - Date.parse(b.claimedAt) || (a.id < b.id ? -1 : Number(a.id > b.id)));
  const index = jobs.findIndex((job) => job.id === request.id);
  return index < 0 ? null : index + 1;
}

// Heartbeats name jobs by request ID. Use their start time when the queue read has no claim for that job yet.
function runningClaims() {
  const byId = new Map();
  for (const a of D.overview?.agents || []) {
    for (const j of a.jobs || []) {
      if (j.id) byId.set(j.id, { id: j.id, claimedBy: a.name, claimedAt: j.since, label: j.label });
    }
  }
  const q = D.ops?.queue;
  for (const r of [...(q?.queued || []), ...(q?.done || [])]) byId.delete(r.id);
  for (const r of q?.running || []) {
    const j = byId.get(r.id);
    byId.set(r.id, { ...r, claimedAt: r.claimedAt || (j?.claimedBy === r.claimedBy ? j.claimedAt : '') });
  }
  return [...byId.values()];
}

function queueRequest(run) {
  const q = D.ops?.queue;
  const id = run.request || run.id;
  return [...(q?.running || []), ...(q?.queued || []), ...(q?.done || [])]
    .find((r) => r.id === id || r.runId === run.id);
}

const runIdForRequest = (r) => r.runId || (D.overview?.running || []).find((run) => run.request === r.id)?.id || r.id;

function reportedMachines() {
  const machines = new Map();
  for (const a of D.overview?.agents || []) {
    machines.set(a.name, { ...a, state: a.fresh === false ? 'gone' : a.state, running: a.jobs?.length });
  }
  for (const m of D.ops?.policy?.machines || []) {
    machines.set(m.name, { ...machines.get(m.name), ...m, state: m.state !== 'gone' && m.drainAsked ? 'draining' : m.state });
  }
  return [...machines.values()];
}

function lastSeen(m) {
  const minutes = minutesSince(m.lastSeen);
  if (!Number.isFinite(minutes)) return 'last seen time not recorded';
  return `last seen <time datetime="${esc(m.lastSeen)}" title="${esc(exact(m.lastSeen))}">${esc(minutes)} min ago</time>`;
}

function machinePlacement(name, request, claims, machines) {
  if (!name) return null;
  const slots = slotCount(machines.find((m) => m.name === name)?.slots);
  const slot = request ? slotOf(request, claims) : null;
  return { name, slot: slot <= slots ? slot : null, slots };
}

function placementHtml(placement) {
  if (!placement) return '';
  const { name, slot, slots } = placement;
  return machineLink(name) + (slot ? `, ${slotLabel(slot, slots)}` : '');
}

function runMachine(run, request = queueRequest(run)) {
  if (norm(run.status) === 'waiting') return null;
  const name = run.agent || request?.claimedBy;
  if (norm(run.status) !== 'running') return name ? { name } : null;
  const claims = runningClaims();
  const claim = claims.find((r) => r.id === (run.request || request?.id || run.id));
  return machinePlacement(claim?.claimedBy || name, claim, claims, reportedMachines());
}
const runPlacement = (run, request) => placementHtml(runMachine(run, request));

const changeCount = (r) => Math.min(r?.covers?.length || 0, 50);
function coverList(r, result = '') {
  const n = changeCount(r);
  if (n <= 1) return '';
  const coveringSHA = short(r.sha);
  const changes = (items) => items.map((c) => {
    const number = c.number > 0 ? '#' + c.number : '';
    const pr = number && r.repo ? `<a class="mono" href="${esc(`https://github.com/${r.repo}/pull/${c.number}`)}" title="Open pull request ${esc(number)} on GitHub" target="_blank" rel="noopener noreferrer">${esc(number)}</a>` : esc(number);
    const covered = coveringSHA && c.sha && short(c.sha) !== coveringSHA
      ? `<span class="why">covered by run on <span class="mono">${esc(coveringSHA)}</span></span>` : '';
    const change = `${c.sha ? `<span class="mono" title="Commit checked by this run">${esc(short(c.sha))}</span> ` : ''}${pr} ${esc(c.title)}${covered}`;
    return result ? `<tr><td>${change}</td><td>${result}</td></tr>` : `<li>${change}</li>`;
  }).join('');
  const list = (items) => result ? `<table class="tbl"><thead><tr><th>Change</th><th title="The result this run supplies for the change">Result on GitHub</th></tr></thead><tbody>${changes(items)}</tbody></table>`
    : `<ul class="cover-list">${changes(items)}</ul>`;
  const more = n - 6;
  return list(r.covers.slice(0, 6)) + (more > 0 ? `<details id="cover-more-${esc(r.id)}" class="cover-more" data-cover><summary><span>Show ${esc(more)} more</span><span>Show fewer</span></summary>${list(r.covers.slice(6, n))}</details>` : '');
}

function toast(msg) {
  const layer = $('#layer');
  const t = document.createElement('div'); t.className = 'toast'; t.setAttribute('role', 'status'); t.textContent = msg;
  layer.appendChild(t); setTimeout(() => t.remove(), 3400);
}

// The page's Content-Security-Policy forbids style="" attributes in markup, so sizes that depend on data are written
// as data-h / data-w / data-flex and applied here through the element's style object, which the policy allows.
function applyDyn(root) {
  $$('[data-h]', root).forEach((e) => { e.style.height = e.dataset.h + 'px'; });
  $$('[data-w]', root).forEach((e) => { e.style.width = e.dataset.w + '%'; });
  $$('[data-flex]', root).forEach((e) => { e.style.flex = e.dataset.flex; });
}

// A very small Markdown reader for the run summary: headings, bullets, bold, code, links. Everything is escaped first.
function md(src) {
  const inline = (s) => esc(s)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>');
  const out = []; let list = false;
  for (const line of String(src || '').split(/\r?\n/)) {
    const h = /^(#{1,4})\s+(.*)$/.exec(line);
    const b = /^\s*[-*]\s+(.*)$/.exec(line);
    if (b) { if (!list) { out.push('<ul>'); list = true; } out.push(`<li>${inline(b[1])}</li>`); continue; }
    if (list) { out.push('</ul>'); list = false; }
    if (h) out.push(`<h4>${inline(h[2])}</h4>`);
    else if (line.trim()) out.push(`<p>${inline(line)}</p>`);
  }
  if (list) out.push('</ul>');
  return out.join('');
}
