// Fails when a Go test file calls a function that shows a REAL window (a message box). A box shown by a test stays on the person's
// desktop until somebody clicks it, and the local CI runs the Windows tests many times an hour (the "Unravis test" box, 2026-11-26).
//   node tools/ci/no-real-dialogs.mjs            scans every tracked *_test.go
// Tests must inject a fake (internal/setup: askBox; cmd/audit-explorer: showMessage; cmd/unravis-setup: env.notice and env.ask).
// A line that is fine ends with `// dialogok: <reason>` (the reason is the point: the call hits a fake, or the test is opt-in by an
// env var that CI never sets). The same rule at run time: tools/ci/watch-test-windows.ps1 (run by prepush.sh).
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

// A call, not a definition and not a bare reference (`showMessage = showMessageBox`).
export const CALL = /\b(AskOKCancel|ShowNotice|MessageBoxW?|MessageBoxTimeoutW?|showMessageBox|showError)\s*\(/;

export function scanText(file, src) {
  const hits = [];
  src.split('\n').forEach((raw, i) => {
    const line = raw.replace(/\r$/, '');
    if (/^\s*\/\//.test(line)) return; // a comment line
    if (/\/\/\s*dialogok:\s*\S/.test(line)) return;
    let body = line;
    const def = /^\s*func\s+(\([^)]*\)\s*)?\w+\s*\([^{]*\{/.exec(line); // a definition: judge only what follows its `{` (a one-line function can hold a call)
    if (def) body = line.slice(def[0].length);
    else if (/^\s*func\s+(\([^)]*\)\s*)?\w+\s*\(/.test(line)) return; // a definition whose body starts on the next line
    if (/NewProc\(\s*"MessageBox/.test(body.replace(/\/\/.*$/, ''))) { hits.push({ file, line: i + 1, call: 'NewProc("MessageBox…")', text: line.trim().slice(0, 140) }); return; }
    const code = body.replace(/"(?:[^"\\]|\\.)*"/g, '""').replace(/`[^`]*`/g, '``').replace(/\/\/.*$/, '');
    const m = CALL.exec(code);
    if (m) hits.push({ file, line: i + 1, call: m[1], text: line.trim().slice(0, 140) });
  });
  return hits;
}

function main() {
  const files = execFileSync('git', ['ls-files', '*_test.go'], { encoding: 'utf8', maxBuffer: 64 << 20 }).split('\n').filter(Boolean);
  let hits = [];
  for (const f of files) {
    let src;
    try { src = readFileSync(f, 'utf8'); } catch { continue; }
    hits = hits.concat(scanText(f, src));
  }
  if (hits.length === 0) { console.log('no-real-dialogs: no test shows a real window.'); return; }
  console.log(`no-real-dialogs: ${hits.length} call(s) in tests that could show a REAL window on the desktop:`);
  for (const h of hits) console.log(`  ${h.file}:${h.line}  ${h.call}   ${h.text}`);
  console.log('Use a fake (see the header of tools/ci/no-real-dialogs.mjs), or add "// dialogok: <reason>" when the call cannot show a window.');
  process.exit(1);
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) main();
