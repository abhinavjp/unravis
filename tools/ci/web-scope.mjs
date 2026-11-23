// Decides whether the web job of the fast gate (.github/workflows/fast-gate.yml) must run for one pull request into develop.
//   node tools/ci/web-scope.mjs [--base HEAD^1] [--head HEAD]     (from the repo root, after `actions/checkout` fetch-depth 2)
// Prints `web=true|false` (also appended to $GITHUB_OUTPUT when set). fast-scope.mjs uses the same rule (touchesWeb).
// The rule: web/ itself, plus every repo file outside web/ that a web test or web build script reads. The list below is checked
// against the tests by test-web-scope.mjs, which scans web/tests and web/scripts and fails when a test reads a path not matched here.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';

// Paths outside web/ that web/tests/*.cjs or web/scripts read. When a web test starts reading another file, add it here
// (test-web-scope.mjs fails until you do). Keep the entries as narrow as the test's read: a wider entry only costs CI time.
export const WEB_READS = [
  /^docs\/user\//,                                   // help pages (helpLinks, helpMarkdown, sweep.f2, p2.cases); also read by the web build (BL-031)
  /^\.gitattributes$/,                               // checkDist.test.cjs
  /^cmd\/unravis-(setup|lite|releases)\/ui\//,       // generated builds (check-dist) and the releases dashboard sources (ui-sweep-f4)
  /^internal\/(server|blackbox|calltrace)\//,        // routes.go (guidedKept), blackbox charts, calltrace types
  /^internal\/audit\/(models|thresholds)\.go$/,      // guided.ui (MaxPageSize), thresholds
  /^internal\/casestore\/plan\.go$/,                 // guided.ui (MaxSavedViews, MaxChecklistItems)
  /^internal\/collect\/(collect\.go|testdata\/wording\.golden\.json)$/, // collect.test
  /^internal\/importer\/reasons\.go$/,               // import.test
  /^tools\/e2e\//,                                   // guidedKept (archive)
];

/** True when this repo-relative path (any separator) can change the result of a web check. */
export function touchesWeb(file) {
  const f = file.replace(/\\/g, '/');
  return f.startsWith('web/') || WEB_READS.some((re) => re.test(f));
}

function main() {
  const a = process.argv.slice(2);
  const opt = (n, d) => { const i = a.indexOf(n); return i < 0 ? d : a[i + 1]; };
  const base = opt('--base', 'HEAD^1'), head = opt('--head', 'HEAD');
  const files = execFileSync('git', ['diff', '--no-renames', '--name-only', base, head], { encoding: 'utf8', maxBuffer: 64 << 20 })
    .split('\n').map((s) => s.trim()).filter(Boolean);
  // An empty diff is a doubt: run it.
  const web = files.length === 0 || files.some(touchesWeb);
  console.log(`web=${web}`);
  if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `web=${web}\n`);
  if (!web) console.log('No web files changed: skipping the web checks.');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
