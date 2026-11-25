'use strict';
// Helpers for detail.js, the release detail panel (steps, tests, checks, files, changes, logs) that was written before the
// Homestretch page. They live in one object so their names cannot clash with the page's own helpers in util.js.
// Every class that el() sets gets an "lg-" prefix, so legacy.css can style the panel without touching the new components.
const LegacyCore = (() => {
  const OWN = 'https://github.com/abhinavjp/unravis/';
  const NEXT = 'next';
  const STEPS = ['Preflight', 'Tag', 'Build', 'Sign', 'Publish', 'Feed', 'Verify'];
  const CHANNELS = ['rc', 'stable'];
  const GLYPH = { done: '✓', failed: '✗', running: '▶', pending: '·', unknown: '?' };
  const WORD = { done: 'Done', failed: 'Failed', running: 'Running', pending: 'Waiting', unknown: 'Not recorded here' };
  const STEP_KIND = { done: 'ok', failed: 'bad', running: 'run', pending: 'mut', unknown: 'mut' };

  const $ = (id) => document.getElementById(id);
  const el = (tag, cls, text) => {
    const e = document.createElement(tag);
    if (cls) e.className = String(cls).split(/\s+/).filter(Boolean).map((c) => 'lg-' + c).join(' ');
    if (text != null) e.textContent = text;
    return e;
  };
  // Only links into this project's own GitHub repository are made clickable.
  const link = (url, text) => {
    if (typeof url !== 'string' || !url.startsWith(OWN)) return null;
    const a = el('a', '', text); a.href = url; a.target = '_blank'; a.rel = 'noopener noreferrer'; return a;
  };
  const when = (s, withTime) => {
    if (!s) return '';
    const d = new Date(s); if (isNaN(d)) return '';
    return d.toLocaleString([], withTime === false ? { dateStyle: 'medium' } : { dateStyle: 'medium', timeStyle: 'short' });
  };
  // What the release as a whole is doing, as a word and a colour kind (ok, bad, warn, run, mut).
  const status = (r) => {
    if (r.withdrawn) return ['Withdrawn', 'warn'];
    if (r.verify === 'failed') return ['Verify failed', 'bad'];
    const failed = (r.steps || []).find((s) => s.state === 'failed');
    if (failed) return ['Stopped at ' + failed.name, 'bad'];
    if ((r.steps || []).some((s) => s.state === 'running')) return ['Running', 'run'];
    if (r.verify === 'passed') return [r.onFeed ? 'Live, verified' : 'Verified', 'ok'];
    if (r.verify === 'pending') return ['Verifying', 'warn'];
    if (r.onFeed) return ['On feed', 'ok'];
    if (r.onGitHub) return ['Published', 'mut'];
    return ['', 'mut'];
  };
  return { OWN, NEXT, STEPS, CHANNELS, GLYPH, WORD, STEP_KIND, $, el, link, when, status };
})();
