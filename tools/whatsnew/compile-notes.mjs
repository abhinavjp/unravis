// Writes the What's new entry of a version into web/public/releases/notes.json from the fragments in docs/whatsnew/fragments.
//
//   node tools/whatsnew/compile-notes.mjs --version 0.1.0-rc.64 [--date 2026-11-27] [--since-tag v0.1.0-rc.63] [--consume] [--no-check-files] [--dry-run]
//
// - The pictures named by the fragments must already exist (capture.mjs takes them first); --no-check-files skips that check.
// - --since-tag adds the commit subjects since that tag (plain words, no merges, no CI-only lines) to the full change list.
// - --consume deletes the fragments that were used, so the next release starts clean (git history keeps them).
// - Run again for a version that already has an entry and has no fragments left: nothing changes.
// Exit 0 ok, 1 a fragment or the result is wrong, 2 cannot run.

import { existsSync, readFileSync, writeFileSync, unlinkSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { buildEntry, loadFragments, mergeNotes, plainSubjects, problemsInEntry } from './lib.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '..', '..');

function parse(argv) {
  const o = { version: '', date: '', sinceTag: '', consume: false, checkFiles: true, dry: false, repo, fragments: '', notes: '' };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const val = () => { if (i + 1 >= argv.length) throw new Error(a + ' needs a value.'); return argv[++i]; };
    if (a === '--version') o.version = val().replace(/^v/i, '');
    else if (a === '--date') o.date = val();
    else if (a === '--since-tag') o.sinceTag = val();
    else if (a === '--consume') o.consume = true;
    else if (a === '--no-check-files') o.checkFiles = false;
    else if (a === '--dry-run') o.dry = true;
    else if (a === '--repo') o.repo = resolve(val());
    else if (a === '--fragments') o.fragments = resolve(val());
    else if (a === '--notes') o.notes = resolve(val());
    else throw new Error('Unknown option ' + a + '.');
  }
  if (!o.version) throw new Error('--version is required, for example --version 0.1.0-rc.64.');
  if (!o.date) o.date = new Date().toISOString().slice(0, 10);
  o.fragments ||= join(o.repo, 'docs', 'whatsnew', 'fragments');
  o.notes ||= join(o.repo, 'web', 'public', 'releases', 'notes.json');
  return o;
}

function gitSubjects(repoDir, tag) {
  const r = spawnSync('git', ['-C', repoDir, 'log', tag + '..HEAD', '--no-merges', '--reverse', '--pretty=format:%s'], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error('git log ' + tag + '..HEAD failed: ' + (r.stderr || '').trim());
  return plainSubjects(r.stdout.split(/\r?\n/));
}

export function screenIdsOf(repoDir) {
  const file = join(repoDir, 'tools', 'screens', 'screens.json');
  if (!existsSync(file)) return null;
  return new Set(JSON.parse(readFileSync(file, 'utf8')).screens.map((s) => s.id));
}

export function compile(o) {
  const fragments = loadFragments(o.fragments, screenIdsOf(o.repo));
  const existing = existsSync(o.notes) ? JSON.parse(readFileSync(o.notes, 'utf8').replace(/^﻿/, '')) : null;
  const has = existing?.releases?.some((r) => String(r.version).replace(/^v/i, '') === o.version);
  if (!fragments.length && has) return { changed: false, message: 'Version ' + o.version + ' already has its notes and there are no fragments: nothing to do.' };
  const subjects = o.sinceTag ? gitSubjects(o.repo, o.sinceTag) : [];
  const entry = buildEntry({ version: o.version, date: o.date, fragments, subjects });
  const problems = problemsInEntry(entry, (p) => !o.checkFiles || existsSync(join(o.repo, 'web', 'public', p)));
  if (problems.length) throw new Error('The notes for ' + o.version + ' are not right:\n  ' + problems.join('\n  '));
  const merged = mergeNotes(existing, entry);
  const text = JSON.stringify(merged, null, 2) + '\n';
  if (!o.dry) {
    writeFileSync(o.notes, text);
    if (o.consume) for (const f of fragments) unlinkSync(join(o.fragments, f.file));
  }
  return { changed: true, entry, fragments: fragments.length, message: (o.dry ? 'Would write' : 'Wrote') + ' notes for ' + o.version + ': ' + entry.highlights.length + ' highlight(s), ' + entry.highlights.filter((h) => h.image).length + ' with a picture, ' + entry.changes.length + ' change line(s).' };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const result = compile(parse(process.argv.slice(2)));
    console.log(result.message);
  } catch (e) {
    console.error(e.message);
    process.exit(/^(--|Unknown|The What|The notes)/.test(e.message) ? 1 : 2);
  }
}
