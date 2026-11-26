// What's new for a release: the pieces shared by compile-notes.mjs (writes web/public/releases/notes.json) and capture.mjs (takes the pictures).
//
// A change that people can see in the app brings a small FRAGMENT with its pull request: docs/whatsnew/fragments/<name>.json.
// At release time the fragments are turned into the entry of the new version in web/public/releases/notes.json (the file the app
// shows in "What's new" and on the Updates page) and into the release notes on GitHub. The pictures are taken from the real app.
//
// Fragment:
//   { "schema": 1,
//     "headline": "One plain line for the whole release (optional; the first fragment, by file name, that has one wins)",
//     "highlights": [ {
//        "label": "Charts",                       optional, up to 40 characters
//        "text": "Plain words for a first-time user.",
//        "alt": "What the picture shows",         optional
//        "image": SHOT,                           optional: a picture of the new thing
//        "before": SHOT                           optional, needs image: how it looked before (shown as before and after)
//     } ],
//     "changes": [ "Extra lines for the full list (the commit subjects are added by the compiler)" ] }
// SHOT is { "screen": "<id in tools/screens/screens.json>" } or { "steps": [ ...steps of tools/screens ] },
// plus optional "clip": "<selector>" (picture of that element only), "theme": "dark" | "light" (default dark) and "wait": ms.
// A highlight with no image is a short note. With image it is a screenshot; with image and before it is a before and after.
// A SHOT may also be the text "/whatsnew/<folder>/<file>" to reuse a picture that is already in the app.
// "before" is taken from the PREVIOUS release (capture.mjs --before-exe), so it shows what people had until now.

import { readdirSync, readFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

export const SCHEMA = 1;
export const NAME_RE = /^[a-z0-9][a-z0-9-]{0,60}$/;
const STEP_KEYS = ['goto', 'click', 'fill', 'select', 'waitFor', 'scrollTo', 'theme', 'aside'];
// The same rule as the app's reader (web/src/lib/updateNotes.ts imagePath): only the app's own /whatsnew/ folder.
const IMAGE_RE = /^\/whatsnew\/[A-Za-z0-9._-]+(\/[A-Za-z0-9._-]+)*$/;

export const isReusePath = (value) => typeof value === 'string' && IMAGE_RE.test(value) && !value.split('/').includes('..');

/** "0.1.0-rc.64" gives "rc64", "0.1.0" gives "v0-1-0": the folder under web/public/whatsnew/ for the pictures of one version. */
export function versionFolder(version) {
  const v = String(version).trim().replace(/^v/i, '');
  const rc = /^\d+\.\d+\.\d+-rc\.(\d+)$/.exec(v);
  if (rc) return 'rc' + rc[1];
  if (/^\d+\.\d+\.\d+$/.test(v)) return 'v' + v.replace(/\./g, '-');
  throw new Error('Version "' + version + '" is not X.Y.Z or X.Y.Z-rc.N.');
}

function checkSteps(steps, where, errors) {
  if (!Array.isArray(steps) || !steps.length) { errors.push(where + ': "steps" must be a list with at least one step.'); return; }
  steps.forEach((step, i) => {
    const keys = step && typeof step === 'object' ? Object.keys(step).filter((k) => k !== 'when') : [];
    if (keys.length !== 1 || !STEP_KEYS.includes(keys[0])) errors.push(where + ': step ' + (i + 1) + ' must be one of ' + STEP_KEYS.join(', ') + '.');
    else if (keys[0] === 'aside') checkSteps(step.aside, where + ' (aside)', errors);
  });
}

function checkShot(shot, where, errors, screenIds) {
  if (isReusePath(shot)) return;
  if (!shot || typeof shot !== 'object' || Array.isArray(shot)) { errors.push(where + ': must be { "screen": id }, { "steps": [...] } or a /whatsnew/... path.'); return; }
  const hasScreen = 'screen' in shot, hasSteps = 'steps' in shot;
  if (hasScreen === hasSteps) errors.push(where + ': give exactly one of "screen" or "steps".');
  else if (hasScreen && (typeof shot.screen !== 'string' || (screenIds && !screenIds.has(shot.screen)))) errors.push(where + ': there is no screen "' + shot.screen + '" in tools/screens/screens.json.');
  else if (hasSteps) checkSteps(shot.steps, where, errors);
  if ('clip' in shot && (typeof shot.clip !== 'string' || !shot.clip.trim())) errors.push(where + ': "clip" must be a selector.');
  if ('theme' in shot && !['dark', 'light'].includes(shot.theme)) errors.push(where + ': "theme" must be dark or light.');
  if ('wait' in shot && !(Number.isInteger(shot.wait) && shot.wait >= 0 && shot.wait <= 5000)) errors.push(where + ': "wait" must be 0 to 5000 milliseconds.');
}

const str = (v, max) => typeof v === 'string' && v.trim().length > 0 && v.trim().length <= max;

/** Checks one fragment. Returns the list of problems (empty when it is fine). `screenIds` is a Set of screen ids, or null to skip that check. */
export function validateFragment(fragment, name, screenIds = null) {
  const errors = [];
  const at = 'fragment ' + name;
  if (!fragment || typeof fragment !== 'object' || Array.isArray(fragment)) return [at + ': must be a JSON object.'];
  if (fragment.schema !== SCHEMA) errors.push(at + ': "schema" must be ' + SCHEMA + '.');
  if ('headline' in fragment && !str(fragment.headline, 200)) errors.push(at + ': "headline" must be 1 to 200 characters.');
  const highlights = fragment.highlights ?? [];
  if (!Array.isArray(highlights) || highlights.length > 20) errors.push(at + ': "highlights" must be a list of at most 20.');
  else highlights.forEach((h, i) => {
    const w = at + ' highlight ' + (i + 1);
    if (!h || typeof h !== 'object' || Array.isArray(h)) { errors.push(w + ': must be an object.'); return; }
    if (!str(h.text, 600)) errors.push(w + ': "text" must be 1 to 600 characters.');
    if ('label' in h && !str(h.label, 40)) errors.push(w + ': "label" must be 1 to 40 characters.');
    if ('alt' in h && !str(h.alt, 300)) errors.push(w + ': "alt" must be 1 to 300 characters.');
    if ('before' in h && !('image' in h)) errors.push(w + ': "before" needs an "image".');
    if ('image' in h) checkShot(h.image, w + ' image', errors, screenIds);
    if ('before' in h) checkShot(h.before, w + ' before', errors, screenIds);
    for (const text of [h.text, h.label, h.alt]) if (typeof text === 'string' && /[<>]/.test(text)) { errors.push(w + ': no < or > in the words (they are shown as plain text).'); break; }
  });
  const changes = fragment.changes ?? [];
  if (!Array.isArray(changes) || changes.length > 200 || changes.some((c) => !str(c, 300))) errors.push(at + ': "changes" must be a list of lines of 1 to 300 characters.');
  return errors;
}

/** Reads every fragment in a folder, sorted by file name. Throws one error that lists every problem. */
export function loadFragments(dir, screenIds = null) {
  if (!existsSync(dir)) return [];
  const names = readdirSync(dir).filter((f) => f.endsWith('.json')).sort();
  const out = [], errors = [];
  for (const file of names) {
    const name = file.slice(0, -5);
    if (!NAME_RE.test(name)) { errors.push('fragment file "' + file + '": use lower case letters, digits and dashes only.'); continue; }
    let data;
    try { data = JSON.parse(readFileSync(join(dir, file), 'utf8').replace(/^﻿/, '')); } catch (e) { errors.push('fragment ' + name + ': not valid JSON (' + e.message + ').'); continue; }
    const problems = validateFragment(data, name, screenIds);
    if (problems.length) errors.push(...problems); else out.push({ name, file, data });
  }
  if (errors.length) throw new Error('The What\'s new fragments have problems:\n  ' + errors.join('\n  '));
  return out;
}

/**
 * The pictures to take, in order, and the app path each one gets. Names are fixed by the fragment, the highlight position and the
 * kind, so compile-notes and capture agree without talking to each other.
 * Returns { shots: [{ file, path, spec, role }], } where role is "image" or "before"; reused /whatsnew/ paths need no shot.
 */
export function planShots(fragments, version) {
  const folder = versionFolder(version);
  const shots = [];
  const pathFor = (fragment, index, role) => '/whatsnew/' + folder + '/' + fragment.name + '-' + (index + 1) + (role === 'before' ? '-before' : '') + '.jpg';
  const resolved = new Map();
  for (const fragment of fragments) {
    (fragment.data.highlights ?? []).forEach((h, i) => {
      for (const role of ['image', 'before']) {
        if (!(role in h)) continue;
        if (isReusePath(h[role])) { resolved.set(h, { ...(resolved.get(h) ?? {}), [role]: h[role] }); continue; }
        const path = pathFor(fragment, i, role);
        shots.push({ file: 'web/public' + path, path, spec: h[role], role, fragment: fragment.name, index: i });
        resolved.set(h, { ...(resolved.get(h) ?? {}), [role]: path });
      }
    });
  }
  return { shots, resolved };
}

/** The text as it is when short enough, otherwise cut at a word end. */
function shorten(text, max) {
  if (text.length <= max) return text;
  return text.slice(0, max).replace(/\s+\S*$/, '').trim() || text.slice(0, max);
}

/** One release entry of notes.json from the fragments and the commit subjects (already filtered by the caller). */
export function buildEntry({ version, date, fragments, subjects = [] }) {
  const { resolved } = planShots(fragments, version);
  const highlights = [];
  const changes = [];
  let headline = '';
  for (const fragment of fragments) {
    if (!headline && fragment.data.headline) headline = fragment.data.headline.trim();
    for (const h of fragment.data.highlights ?? []) {
      const pics = resolved.get(h) ?? {};
      highlights.push({
        ...(h.label ? { label: h.label.trim() } : {}),
        text: h.text.trim(),
        ...(pics.image ? { image: pics.image } : {}),
        ...(pics.before ? { before: pics.before } : {}),
        ...(h.alt ? { alt: h.alt.trim() } : {})
      });
    }
    for (const line of fragment.data.changes ?? []) changes.push(line.trim());
  }
  for (const subject of subjects) {
    const line = String(subject).trim().slice(0, 300);
    if (line && !changes.includes(line)) changes.push(line);
  }
  if (!headline) headline = highlights.length ? shorten(highlights[0].text, 120) : 'Improvements and fixes behind the scenes';
  return { version, date, headline, highlights, changes };
}

/** Commit subjects worth listing for people: no merge lines, no CI-only changes, no pull request numbers at the end. */
export function plainSubjects(lines) {
  const out = [];
  for (const raw of lines) {
    const line = String(raw).replace(/\s*\(#\d+\)\s*$/, '').trim();
    if (!line || /^merge\b/i.test(line) || /^ci:/i.test(line) || /^rebuild web files for promotion/i.test(line) || /^backlog:/i.test(line)) continue;
    if (!out.includes(line)) out.push(line);
  }
  return out;
}

const versionParts = (v) => {
  const m = /^(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$/.exec(String(v).replace(/^v/i, ''));
  return m ? [+m[1], +m[2], +m[3], m[4] === undefined ? Number.MAX_SAFE_INTEGER : +m[4]] : null;
};
export function compareVersions(a, b) {
  const x = versionParts(a), y = versionParts(b);
  if (!x || !y) return 0;
  for (let i = 0; i < 4; i++) if (x[i] !== y[i]) return x[i] < y[i] ? -1 : 1;
  return 0;
}

/**
 * Puts the entry into the notes file: replaces the same version, otherwise adds it; newest first. A version that already has an
 * entry and no fragments to say otherwise is left alone (running the pipeline twice changes nothing).
 */
export function mergeNotes(file, entry) {
  const base = file && typeof file === 'object' && file.schema === SCHEMA && Array.isArray(file.releases) ? file : { schema: SCHEMA, releases: [] };
  const releases = base.releases.filter((r) => String(r.version).replace(/^v/i, '') !== entry.version);
  releases.push(entry);
  releases.sort((a, b) => compareVersions(b.version, a.version));
  return { schema: SCHEMA, releases };
}

/** The shape the app's reader keeps: used by the compiler to refuse a file the app would silently drop parts of. */
export function problemsInEntry(entry, existsPublic) {
  const errors = [];
  if (!versionParts(entry.version)) errors.push('version "' + entry.version + '" is not X.Y.Z or X.Y.Z-rc.N.');
  if (!/^\d{4}-\d{2}-\d{2}$/.test(entry.date)) errors.push('date "' + entry.date + '" is not YYYY-MM-DD.');
  if (!entry.headline) errors.push('no headline.');
  for (const h of entry.highlights) for (const role of ['image', 'before']) {
    if (!h[role]) continue;
    if (!isReusePath(h[role])) errors.push(role + ' "' + h[role] + '" is not a /whatsnew/ path.');
    else if (!existsPublic(h[role])) errors.push(role + ' picture "' + h[role] + '" does not exist in web/public.');
  }
  return errors;
}
