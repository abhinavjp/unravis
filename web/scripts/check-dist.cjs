'use strict';
// BL-031: compares the built web files on disk with the files committed at HEAD, byte for byte.
//
//   node scripts/check-dist.cjs [--repo <dir>]
//
// Exit 0: the three output folders on disk equal what is committed.
// Exit 1: they differ (the first line says what to run). Required on the promotion to master and on the release (ci.yml, release.yml),
// not on pull requests into develop: docs/pipeline/dist-on-promotion.md.
// Exit 2: the check could not run (no git, not a repository, not a sha1 repository).
//
// It compares git blob ids computed here from the raw bytes on disk. It does not use `git diff` or
// `git status`: with core.autocrlf=true those hide a file that differs only in line endings.
// Read only: it reads the working tree and runs `git ls-tree` and `git cat-file` with fixed arguments.

const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { spawnSync } = require('node:child_process');

// The folders `npm run build` writes: the main app, the setup program, Unravis Lite.
const ROOTS = ['web/dist', 'cmd/unravis-setup/ui', 'cmd/unravis-lite/ui'];

class CannotRun extends Error {}

function git(repo, args, opts = {}) {
  const r = spawnSync('git', args, { cwd: repo, maxBuffer: 256 * 1024 * 1024, ...opts });
  if (r.error) throw new CannotRun('git could not be started: ' + r.error.message);
  if (r.status !== 0) {
    const why = String(r.stderr || '').trim().split('\n')[0] || 'exit ' + r.status;
    throw new CannotRun('git ' + args[0] + ' failed: ' + why);
  }
  return r.stdout;
}

function blobId(bytes) {
  const h = crypto.createHash('sha1');
  h.update('blob ' + bytes.length + '\0');
  h.update(bytes);
  return h.digest('hex');
}

// path (slash separated, from the repository top) -> blob id, for everything committed under root.
function committed(repo, root) {
  const out = git(repo, ['ls-tree', '-r', '-z', '--full-tree', 'HEAD', '--', root]).toString('latin1');
  const map = new Map();
  for (const rec of out.split('\0')) {
    if (!rec) continue;
    const tab = rec.indexOf('\t');
    const [mode, type, id] = rec.slice(0, tab).split(' ');
    const name = Buffer.from(rec.slice(tab + 1), 'latin1').toString('utf8');
    if (type !== 'blob' || mode === '160000') continue;
    map.set(name, id);
  }
  return map;
}

// Every entry under dir on disk. Files go to files (relative path with slashes), anything that is
// neither a regular file nor a folder goes to odd. Dotfiles are included.
function walk(top, rel, files, odd) {
  let entries;
  try {
    entries = fs.readdirSync(path.join(top, rel), { withFileTypes: true });
  } catch (e) {
    if (e.code === 'ENOENT' || e.code === 'ENOTDIR') return;
    throw e;
  }
  for (const e of entries) {
    const r = rel ? rel + '/' + e.name : e.name;
    if (e.isSymbolicLink()) odd.push(r);
    else if (e.isDirectory()) walk(top, r, files, odd);
    else if (e.isFile()) files.push(r);
    else odd.push(r);
  }
}

const crlfToLf = (b) => Buffer.from(b.toString('latin1').replace(/\r\n/g, '\n'), 'latin1');

function checkDist({ repo, roots = ROOTS }) {
  if (git(repo, ['rev-parse', '--show-object-format']).toString().trim() !== 'sha1') {
    throw new CannotRun('This repository does not use sha1 object ids, so the check cannot compare files.');
  }
  const res = { missing: [], extra: [], changed: [], eolOnly: [], notFile: [], noIndex: [], total: 0, ok: false };
  for (const root of roots) {
    const want = committed(repo, root);
    if (!want.has(root + '/index.html')) res.noIndex.push(root);
    const files = [];
    const odd = [];
    walk(path.join(repo, root), '', files, odd);
    for (const o of odd) res.notFile.push(root + '/' + o);
    const seen = new Set();
    for (const f of files) {
      const p = root + '/' + f;
      seen.add(p);
      res.total++;
      const id = want.get(p);
      if (id === undefined) {
        res.extra.push(p);
        continue;
      }
      const bytes = fs.readFileSync(path.join(repo, p));
      if (blobId(bytes) === id) continue;
      res.changed.push(p);
      const was = git(repo, ['cat-file', 'blob', id]);
      if (crlfToLf(bytes).equals(crlfToLf(was))) res.eolOnly.push(p);
    }
    for (const p of want.keys()) if (!seen.has(p)) res.missing.push(p);
  }
  for (const k of ['missing', 'extra', 'changed', 'eolOnly', 'notFile', 'noIndex']) res[k].sort();
  res.ok = !(res.missing.length || res.extra.length || res.changed.length || res.notFile.length || res.noIndex.length);
  return res;
}

// File names come from the disk and from git. Strip control characters before they are printed, so a
// crafted name cannot start a line of its own (for example a forged ::error:: annotation).
const clean = (s) => String(s).replace(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/g, '?');

function listLine(label, list, cap = 20) {
  if (!list.length) return label + ' (0)';
  const shown = list.slice(0, cap).map(clean).join(', ');
  return label + ' (' + list.length + '): ' + shown + (list.length > cap ? ', and ' + (list.length - cap) + ' more' : '');
}

function report(res, roots = ROOTS, annotate = !!process.env.GITHUB_ACTIONS) {
  if (res.ok) return 'web build output matches the commit (' + res.total + ' files)';
  const lines = [
    (annotate ? '::error::' : '') +
      'The built web files differ from what is committed (' + roots.join(', ') + '). Pull requests into develop do not commit them: ' +
      'the promotion does (tools/release/Promote-Develop.ps1 rebuilds them; see docs/pipeline/dist-on-promotion.md). ' +
      'On a promotion or a hotfix into master, run `npm run build` in `web` and commit the three folders.',
    listLine('changed', res.changed),
    listLine('extra', res.extra),
    listLine('missing', res.missing),
  ];
  if (res.eolOnly.length) lines.push(listLine('only line endings differ', res.eolOnly) + ' (check .gitattributes)');
  if (res.notFile.length) lines.push(listLine('not a regular file', res.notFile));
  if (res.noIndex.length) lines.push(listLine('no committed index.html', res.noIndex));
  return lines.join('\n');
}

function main(argv) {
  let repo = null;
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--repo' && argv[i + 1]) repo = path.resolve(argv[++i]);
    else {
      console.error('usage: node scripts/check-dist.cjs [--repo <dir>]');
      return 2;
    }
  }
  try {
    if (!repo) repo = git(process.cwd(), ['rev-parse', '--show-toplevel']).toString().trim();
    const res = checkDist({ repo });
    const text = report(res);
    (res.ok ? console.log : console.error)(text);
    return res.ok ? 0 : 1;
  } catch (e) {
    if (!(e instanceof CannotRun)) throw e;
    console.error('Cannot check the built web files: ' + clean(e.message));
    return 2;
  }
}

module.exports = { ROOTS, checkDist, blobId, report };

if (require.main === module) process.exitCode = main(process.argv.slice(2));
