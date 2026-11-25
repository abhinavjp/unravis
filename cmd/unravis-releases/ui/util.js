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
