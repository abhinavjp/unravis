// Decides what the fast gate (.github/workflows/fast-gate.yml) must check for one pull request into develop.
//   node tools/ci/fast-scope.mjs [--base HEAD^1] [--head HEAD]     (run from the repo root, after `actions/checkout` fetch-depth 2)
// Prints key=value lines (written to $GITHUB_OUTPUT when set) and a human summary:
//   notes   true  only notes changed (docs outside docs/user, .forge, root *.md, LICENSE, NOTICE): nothing to run
//   all     true  something changed that can break any package (go.mod, go.sum, ci.yml, the baseline, an unknown path): test every package
//   go      true  Go packages are affected
//   web     true  web/ changed: type check, tests, build and the committed-build check
//   gofiles       changed .go files (for the gofmt step)
//   packages      the packages to vet and test: the changed ones plus every package that imports them (directly or not)
// The rule is "when in doubt, widen": a path this file does not know makes `all` true. The self-test is test-fast-scope.mjs.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';
import { touchesWeb } from './web-scope.mjs';

// Same split as the `changes` job of ci.yml. docs/user and the Linux and race baselines are NOT notes: tests read them.
export const HEAVY_DOCS = /^(docs\/user\/|docs\/desktop\/baseline-go-(tests|race)\.txt$)/;
export const NOTES = /^(\.forge\/|docs\/|[^/]+\.md$|LICENSE$|NOTICE$)/;
// Files that can change the result of any test in any package.
const WIDE = /^(go\.mod$|go\.sum$|go\.work(\.sum)?$|\.golangci[^/]*$|build\.ps1$|\.github\/workflows\/ci\.yml$)/;
// Paths that are not Go and not read by the Go tests (the desktop shell has its own workflow; other tooling has its own checks).
// Paths outside web/ that the web tests read: the list lives in web-scope.mjs, which the web job of fast-gate.yml also runs.
// Kept as a name for the self-test: true when the path is one of them (web/ itself excluded).
export const CROSS_WEB = { test: (f) => !f.replace(/\\/g, '/').startsWith('web/') && touchesWeb(f) };
const OUTSIDE_GO = /^(desktop\/|tools\/(ci|e2e|screens|security|desktop|release)\/|\.github\/(?!workflows\/ci\.yml$))/;

/** Path -> what it affects. Pure; `pkgDirs` is the set of directories (relative, '.' for the root) that hold a Go package. */
export function classify(files, pkgDirs) {
  const out = { notes: false, all: false, web: false, dirs: new Set(), gofiles: [], reasons: [] };
  let anyCode = false;
  for (const raw of files) {
    const f = raw.replace(/\\/g, '/');
    if (HEAVY_DOCS.test(f)) {
      anyCode = true;
      if (f.startsWith('docs/user/')) {
        // Read by the internal/investigate tests and embedded in the exe (root package).
        out.dirs.add('internal/investigate'); out.dirs.add('.'); out.web = true; out.reasons.push(`${f}: read by internal/investigate tests and the web tests, embedded in the exe`);
      } else { out.all = true; out.reasons.push(`${f}: changes how every Linux failure is judged, so every package`); }
      continue;
    }
    if (NOTES.test(f)) { out.reasons.push(`${f}: notes`); continue; }
    anyCode = true;
    if (WIDE.test(f)) { out.all = true; out.reasons.push(`${f}: can affect every package`); continue; }
    if (f.endsWith('.go')) out.gofiles.push(f);
    // Tests that read files of another directory (found by reading the tests; extend when a new one appears):
    //   internal/evidence and internal/server read web/src/lib/copy (wordings.ts, importCopy.ts);
    //   cmd/audit-explorer reads cmd/unravis-setup/main.go and internal/setup/*.go (the elevated worker never starts unravis.exe);
    //   the web tests read internal/server/routes.go, internal/blackbox, internal/calltrace and tools/e2e/archive.
    if (f.startsWith('web/src/lib/copy/')) { out.dirs.add('internal/evidence'); out.dirs.add('internal/server'); }
    if (/^(cmd\/unravis-setup\/|internal\/setup\/)/.test(f)) out.dirs.add('cmd/audit-explorer');
    // contracts/ (BL-445): schemas and golden answers, read by internal/server/contracts_test.go and by desktop/ipc-core/tests (promotion only).
    if (f.startsWith('contracts/')) { out.dirs.add('internal/server'); out.reasons.push(`${f}: read by internal/server contract tests`); continue; }
    if (CROSS_WEB.test(f)) { out.web = true; out.reasons.push(`${f}: read by the web tests`); }
    if (f.startsWith('web/dist/')) { out.web = true; out.dirs.add('.'); out.reasons.push(`${f}: committed web build, embedded by the root package`); continue; }
    if (f.startsWith('web/')) { out.web = true; out.reasons.push(`${f}: web`); continue; }
    if (OUTSIDE_GO.test(f) && !f.endsWith('.go')) { out.reasons.push(`${f}: not read by the Go tests`); continue; }
    // Nearest ancestor directory that is a Go package (the root counts only for files directly in the root).
    const parts = f.split('/');
    let dir = null;
    for (let n = parts.length - 1; n >= 0; n--) {
      const d = n === 0 ? '.' : parts.slice(0, n).join('/');
      if (pkgDirs.has(d) && (d !== '.' || parts.length === 1)) { dir = d; break; }
    }
    if (dir) { out.dirs.add(dir); out.reasons.push(`${f}: package ${dir}`); }
    else { out.all = true; out.reasons.push(`${f}: unknown path, so every package`); }
  }
  out.notes = !anyCode;
  return out;
}

/** Packages that import any of `seeds` (directly or through others), plus the seeds. graph: importPath -> [imported import paths]. */
export function withDependents(graph, seeds) {
  const rev = new Map();
  for (const [p, imps] of graph) for (const i of imps) { if (!rev.has(i)) rev.set(i, new Set()); rev.get(i).add(p); }
  const seen = new Set(seeds), queue = [...seeds];
  while (queue.length) for (const d of rev.get(queue.pop()) || []) if (!seen.has(d)) { seen.add(d); queue.push(d); }
  return [...seen].sort();
}

function git(...args) { return execFileSync('git', args, { encoding: 'utf8', maxBuffer: 64 << 20 }); }

function main() {
  const a = process.argv.slice(2);
  const opt = (n, d) => { const i = a.indexOf(n); return i < 0 ? d : a[i + 1]; };
  const base = opt('--base', 'HEAD^1'), head = opt('--head', 'HEAD');
  const files = git('diff', '--no-renames', '--name-only', base, head).split('\n').map((s) => s.trim()).filter(Boolean);
  const out = (k, v) => { console.log(`${k}=${v}`); if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `${k}=${v}\n`); };
  if (files.length === 0) { // an empty diff is a doubt: widen
    console.log('No changed files found: checking everything.');
    for (const [k, v] of [['notes', false], ['all', true], ['go', true], ['web', true], ['gofiles', ''], ['packages', './...']]) out(k, v);
    return;
  }
  // One `go list` gives every package, its directory and its imports (including the imports of its tests).
  // Twice: some imports exist only on Windows (the fast gate also vets for Windows), so the graph is the union of both.
  const listArgs = ['list', '-f', '{{.ImportPath}}|{{.Dir}}|{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}', './...'];
  const goList = (goos) => execFileSync('go', listArgs, { encoding: 'utf8', maxBuffer: 64 << 20, env: { ...process.env, GOOS: goos } }).split('\n').filter(Boolean);
  const rows = [...goList('linux'), ...goList('windows')];
  const root = process.cwd().replace(/\\/g, '/');
  const graph = new Map(), dirOf = new Map(), pkgDirs = new Set();
  for (const r of rows) {
    const [ip, dir, imps] = r.split('|');
    const rel = dir.replace(/\\/g, '/').startsWith(root) ? dir.replace(/\\/g, '/').slice(root.length).replace(/^\//, '') || '.' : dir;
    const list = imps.split(/\s+/).filter(Boolean);
    graph.set(ip, [...new Set([...(graph.get(ip) || []), ...list])]); dirOf.set(rel, ip); pkgDirs.add(rel);
  }
  const c = classify(files, pkgDirs);
  for (const r of c.reasons) console.log(r);
  const seeds = [...c.dirs].map((d) => dirOf.get(d)).filter(Boolean);
  const gofiles = c.gofiles.filter((f) => fs.existsSync(f)); // a deleted file has nothing to format
  let packages = '';
  if (c.all) packages = './...';
  else if (seeds.length) packages = withDependents(graph, seeds).join(' ');
  out('notes', c.notes); out('all', c.all);
  out('go', c.all || seeds.length > 0 || gofiles.length > 0);
  out('web', c.web); out('gofiles', gofiles.join(' ')); out('packages', packages);
  console.log(c.notes ? 'Only notes changed: nothing to run.' : `Scope: ${c.all ? 'all packages' : `${packages ? packages.split(' ').length : 0} packages`}${c.web ? ', web' : ''}.`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
