// Diff coverage (BL-451): of the lines a pull request changes, how many do the tests run?
//   node tools/ci/diff-cover.mjs --base HEAD^1 --head HEAD --go go.cover [--lcov rust.lcov ...] [--min 80] [--min-lines 10] [--out summary.md] [--report-only]
//   node tools/ci/diff-cover.mjs --diff changes.diff --go go.cover ...       (a saved `git diff -U0`, for tests and for a local run)
// Inputs: a Go cover profile (`go test -coverprofile`) and/or lcov files (cargo-llvm-cov --lcov, Vitest/c8 lcov reporter).
// Only lines that the profile knows as code count ("measurable"). A comment, a blank line, a declaration without statements, a file the
// profile does not list (a Windows-only file on Linux, a docs file, a file with no tests run) is not counted. Test files and generated
// files are left out. Old code is never judged: only added or changed lines.
// A blank line or a comment line is not counted even inside a covered block.
// Gate: fails when coverage of measurable changed lines is under --min percent AND there are at least --min-lines of them (a 3-line
// change is not judged on a ratio). --report-only prints the same report and always exits 0 (how the fast gate runs it today).
// Exit 0: pass or report-only. Exit 1: under the threshold. Exit 2: bad input (a missing file, no profile given).
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

/** Undoes git's C-style quoting of a path ("a/\303\251.go", \t, \", \\). */
export function unquote(p) {
  if (!p.startsWith('"')) return p;
  const bytes = [];
  const body = p.slice(1, p.lastIndexOf('"'));
  for (let i = 0; i < body.length; i++) {
    if (body[i] !== '\\') { bytes.push(...Buffer.from(body[i])); continue; }
    const oct = /^[0-7]{3}/.exec(body.slice(i + 1));
    if (oct) { bytes.push(parseInt(oct[0], 8)); i += 3; continue; }
    const map = { n: 10, t: 9, r: 13, '"': 34, '\\': 92, a: 7, b: 8, f: 12, v: 11 };
    bytes.push(map[body[i + 1]] ?? body.charCodeAt(i + 1));
    i++;
  }
  return Buffer.from(bytes).toString('utf8');
}

/** Added lines per file from a unified diff made with -U0: Map<file, Map<line, text>>. Deleted files and pure deletions give nothing.
 *  Hunks are read by their line counts, so an added line that looks like a header ("++ x") is never mistaken for one. */
export function parseDiff(text) {
  const out = new Map();
  let file = null;
  let oldLeft = 0;
  let newLeft = 0;
  let next = 0;
  for (const line of text.split(/\r?\n/)) {
    if (oldLeft > 0 || newLeft > 0) {
      if (line.startsWith('+')) { newLeft--; if (file) out.get(file).set(next, line.slice(1)); next++; continue; }
      if (line.startsWith('-')) { oldLeft--; continue; }
      if (line.startsWith('\\')) continue;
      oldLeft = 0; newLeft = 0; // a short hunk: fall through and read the line as a header
    }
    if (line.startsWith('+++ ')) {
      const p = unquote(line.slice(4).trim());
      file = p === '/dev/null' ? null : p.replace(/^b\//, '');
      continue;
    }
    const m = /^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (m && file) {
      oldLeft = m[1] === undefined ? 1 : Number(m[1]);
      newLeft = m[3] === undefined ? 1 : Number(m[3]);
      next = Number(m[2]);
      if (!out.has(file)) out.set(file, new Map());
    }
  }
  return out;
}

/** A line with no code in it: blank, or only a comment marker (a comment inside a counted block is not a statement). */
export function isNoise(text) {
  const t = text.trim();
  return t === '' || /^(\/\/|\/\*|\*\/?|#)/.test(t);
}

/** Go cover profile -> Map<file, Map<line, covered>>. A line is covered when any block that touches it ran; `modulePath` is stripped from file names. */
export function parseGoProfile(text, modulePath) {
  const out = new Map();
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith('mode:')) continue;
    const m = /^(.+):(\d+)\.\d+,(\d+)\.\d+ (\d+) (\d+)$/.exec(line);
    if (!m) throw new Error(`not a cover profile line: ${line}`);
    let file = m[1];
    if (modulePath && file.startsWith(modulePath + '/')) file = file.slice(modulePath.length + 1);
    const lines = out.get(file) || new Map();
    out.set(file, lines);
    const hit = Number(m[5]) > 0;
    for (let l = Number(m[2]); l <= Number(m[3]); l++) lines.set(l, (lines.get(l) || false) || hit);
  }
  return out;
}

/** lcov text -> Map<file, Map<line, covered>> (several records of one file are merged: covered if any run covered it). */
export function parseLcov(text, root = '') {
  const out = new Map();
  let lines = null;
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line.startsWith('SF:')) {
      let file = line.slice(3).replace(/\\/g, '/');
      if (root && path.isAbsolute(file)) file = path.relative(root, file).replace(/\\/g, '/');
      lines = out.get(file) || new Map();
      out.set(file, lines);
    } else if (line.startsWith('DA:') && lines) {
      const [n, c] = line.slice(3).split(',');
      lines.set(Number(n), (lines.get(Number(n)) || false) || Number(c) > 0);
    } else if (line === 'end_of_record') lines = null;
  }
  return out;
}

/** Files that are not judged: tests and generated code. `read` returns the first lines of a file, or '' when it is gone. */
export function isExcluded(file, read = () => '') {
  const base = path.posix.basename(file);
  if (/_test\.go$/.test(base) || /\.(test|spec)\.[cm]?[jt]sx?$/.test(base)) return true;
  if (/(^|\/)(testdata|node_modules|dist|target)\//.test(file)) return true;
  if (/\.(pb|gen|generated)\.(go|rs|ts)$/.test(base) || /^zz_generated/.test(base)) return true;
  return /^\/\/ Code generated .* DO NOT EDIT\.|@generated/m.test(read(file).split('\n').slice(0, 8).join('\n'));
}

/** Joins diff and coverage. Returns { files: [{file, covered, missed: number[]}], measurable, covered, percent } (percent null with no measurable line). */
export function diffCover(added, coverage, read) {
  const files = [];
  let measurable = 0;
  let covered = 0;
  for (const [file, changed] of [...added.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
    const lines = coverage.get(file);
    if (!lines || isExcluded(file, read)) continue;
    const cov = [];
    const missed = [];
    for (const n of [...changed.keys()].sort((a, b) => a - b)) {
      if (!lines.has(n) || isNoise(changed.get(n))) continue;
      if (lines.get(n)) cov.push(n); else missed.push(n);
    }
    if (cov.length + missed.length === 0) continue;
    measurable += cov.length + missed.length;
    covered += cov.length;
    files.push({ file, covered: cov.length, missed });
  }
  return { files, measurable, covered, percent: measurable === 0 ? null : (covered / measurable) * 100 };
}

/** 1,2,3,7 -> "1-3, 7" */
export function ranges(nums) {
  const out = [];
  for (let i = 0; i < nums.length; i++) {
    let j = i;
    while (j + 1 < nums.length && nums[j + 1] === nums[j] + 1) j++;
    out.push(j > i ? `${nums[i]}-${nums[j]}` : String(nums[i]));
    i = j;
  }
  return out.join(', ');
}

export function render(r, { min, minLines, reportOnly }) {
  const pct = r.percent === null ? null : r.percent.toFixed(1);
  const lines = ['## Diff coverage', ''];
  if (r.measurable === 0) lines.push('No changed line is measurable (no covered code changed, or the changed code is not run on this runner). Nothing to judge.');
  else {
    lines.push(`Changed lines the tests can see: **${r.measurable}**; run by a test: **${r.covered}** (**${pct}%**). Threshold ${min}% from ${minLines} lines${reportOnly ? ' (report only, not blocking)' : ''}.`, '');
    const bad = r.files.filter((f) => f.missed.length);
    if (bad.length) {
      lines.push('| File | Not run by any test |', '| --- | --- |');
      for (const f of bad) lines.push(`| \`${f.file}\` | ${ranges(f.missed)} |`);
    } else lines.push('Every measurable changed line is run by a test.');
  }
  return lines.join('\n') + '\n';
}

/** True when the gate fails. */
export function fails(r, { min, minLines }) {
  return r.percent !== null && r.measurable >= minLines && r.percent < min;
}

function arg(argv, name, many = false) {
  const vals = [];
  for (let i = 0; i < argv.length; i++) if (argv[i] === name) vals.push(argv[i + 1]);
  return many ? vals : vals[0];
}

function main(argv) {
  const goFile = arg(argv, '--go');
  const lcovFiles = arg(argv, '--lcov', true);
  if (!goFile && lcovFiles.length === 0) { console.error('give --go and/or --lcov'); return 2; }
  const min = Number(arg(argv, '--min') ?? 80);
  const minLines = Number(arg(argv, '--min-lines') ?? 10);
  const reportOnly = argv.includes('--report-only');
  if (!Number.isFinite(min) || !Number.isFinite(minLines)) { console.error('--min and --min-lines must be numbers'); return 2; }
  try {
    let diff;
    const saved = arg(argv, '--diff');
    if (saved) diff = fs.readFileSync(saved, 'utf8');
    else {
      const base = arg(argv, '--base');
      if (!base) { console.error('give --diff <file> or --base <ref> [--head <ref>]'); return 2; }
      diff = execFileSync('git', ['-c', 'core.quotepath=false', '-c', 'diff.noprefix=false', '-c', 'diff.mnemonicPrefix=false', 'diff', '--no-ext-diff', '-M', '--diff-filter=d', '-U0', '--no-color', base, arg(argv, '--head') ?? 'HEAD'], { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
    }
    const coverage = new Map();
    const merge = (m) => { for (const [f, l] of m) { const cur = coverage.get(f) || new Map(); for (const [n, c] of l) cur.set(n, (cur.get(n) || false) || c); coverage.set(f, cur); } };
    if (goFile) {
      const mod = /^module\s+(\S+)/m.exec(fs.readFileSync('go.mod', 'utf8'));
      merge(parseGoProfile(fs.readFileSync(goFile, 'utf8'), mod && mod[1]));
    }
    for (const f of lcovFiles) merge(parseLcov(fs.readFileSync(f, 'utf8'), process.cwd()));
    const read = (f) => { try { return fs.readFileSync(f, 'utf8').slice(0, 2000); } catch { return ''; } };
    const r = diffCover(parseDiff(diff), coverage, read);
    const md = render(r, { min, minLines, reportOnly });
    console.log(md);
    const out = arg(argv, '--out') || process.env.GITHUB_STEP_SUMMARY;
    if (out) fs.appendFileSync(out, md);
    if (fails(r, { min, minLines })) {
      console.log(`::${reportOnly ? 'warning' : 'error'}::Diff coverage ${r.percent.toFixed(1)}% is under ${min}% (${r.measurable} changed lines the tests can see).`);
      return reportOnly ? 0 : 1;
    }
    return 0;
  } catch (e) {
    console.error(`diff-cover: ${e.message}`);
    return 2;
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) process.exit(main(process.argv.slice(2)));
