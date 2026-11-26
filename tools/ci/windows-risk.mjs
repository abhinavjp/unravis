// Windows-risk scan of the Go lines a change ADDS. A report, not a gate (exit 0) unless --strict.
//   node tools/ci/windows-risk.mjs [--base origin/develop] [--head HEAD] [--strict]
// Why it exists: the fast gate runs on Linux, so a test that only breaks on Windows merges into develop and turns the push run red
// (docs/ci/CI-FAILURE-ANALYSIS-2026-11.md, cause 1). Every rule below comes from a test that really failed that way.
// A line is skipped when it ends with `// winok: <reason>` (the reason is the point: say why it holds on Windows).
// Files named *_windows*, *_linux*, *_unix* and files with a //go:build line are skipped: they already run on one OS only
// (a file tagged only `!tauri_sidecar` is still judged: it runs on Windows).
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

const PROG = 'sh|bash|\\/bin\\/[a-z]+|cat|ls|sleep|true|false';

// [id, test-only, regex, why and the fix]
export const RULES = [
  ['unix-path', true, /["`]\/(p|tmp|home|usr|var|etc|opt|root|mnt)\//,
    'a Unix path literal: on Windows it becomes \\p\\... (TestInstallDir, TestAppProgramOnDisk). Build paths with t.TempDir() and filepath.Join, or compare with filepath.ToSlash.'],
  ['file-mode', true, /\.(Mode\(\)\.Perm\(\)|Perm\(\))|Mode\(\)\s*&\s*(0?o?[0-7]{3}\b|os\.ModePerm)/,
    'a file permission check: Windows reports 0666 or 0777 whatever was asked (TestOpenSetsFileModeAndFolder, TestStoreFilesAndFolderAreOwnerOnly). Guard with runtime.GOOS or test the ACL in a _windows test.'],
  ['chmod', true, /\bos\.Chmod\(/,
    'os.Chmod does not make a read-only folder or a private file on Windows. Use a _windows file for the ACL, or skip on Windows.'],
  ['home-env', true, /(Setenv|Getenv)\(\s*"(HOME|XDG_[A-Z_]+|TMPDIR)"/,
    'HOME, XDG_* and TMPDIR mean nothing on Windows (USERPROFILE, LOCALAPPDATA, TEMP). Set the Windows variable as well (a file that sets USERPROFILE too is not reported), or use t.TempDir().'],
  // Only the program (first argument) counts: exec.Command(bin, "set", "x", "true") is fine.
  ['shell-out', true, new RegExp(`exec\\.Command\\(\\s*"(${PROG})"|exec\\.CommandContext\\([^,()]+,\\s*"(${PROG})"`),
    'a Unix program: not on a Windows PC. Use the test binary itself (os.Args[0] with a helper-process env var) or skip on Windows.'],
  // SIGTERM, SIGKILL, Getuid and Getgid compile on Windows, so they are not listed.
  ['unix-syscall', false, /\bsyscall\.(Kill|SIGUSR[12]|Setpgid|Getpgid|Flock|Umask|Mkfifo|Stat_t)\b|\bSetpgid:|\bSetsid:/,
    'a Unix-only call: it does not compile on Windows. Move it to a _unix.go or _linux.go file with a _windows.go twin.'],
  // A budget in milliseconds, or a sleep of 20 ms or more (a 10 ms poll interval is fine).
  ['timing', true, /time\.Since\([^)]*\)\s*[<>]=?\s*[0-9a-zA-Z_.*\s]*Millisecond|time\.Sleep\(\s*([2-9][0-9]|[1-9][0-9]{2})\s*\*\s*time\.Millisecond\)/,
    'a wall-clock budget or a short sleep: it flakes on a loaded runner (TestMaskLongNamesAreLinear, TestLockWaitHonoursContext). Wait on a channel or a condition with a generous timeout; if you must time it, count work, not seconds.'],
  // Text fixtures compared byte for byte (not .json, which is parsed).
  ['golden-eol', true, /(ReadFile|Open)\([^)]*(golden|testdata[^)]*\.(txt|md|ts|js)\b)/,
    'a text fixture compared byte for byte: a Windows checkout may turn LF into CRLF (TestGoldenFixture, TestGoldenDts). Normalise "\\r\\n" to "\\n" at the read point, or mark the file eol=lf in .gitattributes.'],
  // A capital drive letter and a real path: not "a:\n".
  ['abs-drive', true, /["`][A-Z]:\\{1,2}[A-Za-z]{2}/,
    'a drive-letter path in a test that also runs on Linux: it is a relative name there. Put it in a _windows test or build it from t.TempDir().'],
];

const SKIP_FILE = /_(windows|linux|unix|darwin)(_test)?\.go$|_windows_|_linux_|_unix_/;

export function scanPatch(patch) {
  // patch: output of `git diff -U0 -M base head -- '*.go'`
  const out = [];
  const winEnv = new Set(); // files whose added lines also set USERPROFILE
  let file = null, line = 0;
  for (const raw of patch.split('\n')) {
    const h = /^\+\+\+ b\/(.+)$/.exec(raw);
    if (h) { file = h[1]; continue; }
    const hunk = /^@@ -\S+ \+(\d+)(?:,\d+)? @@/.exec(raw);
    if (hunk) { line = Number(hunk[1]); continue; }
    if (!file || !raw.startsWith('+') || raw.startsWith('+++')) continue;
    const text = raw.slice(1);
    const here = line++;
    if (/Setenv\(\s*"USERPROFILE"/.test(text)) winEnv.add(file);
    if (SKIP_FILE.test(file) || /\/\/\s*winok:\s*\S/.test(text)) continue;
    if (/^\s*\/\//.test(text)) continue; // a comment line
    const isTest = file.endsWith('_test.go');
    for (const [id, testOnly, re, why] of RULES) {
      if (testOnly && !isTest) continue;
      if (re.test(text)) out.push({ file, line: here, id, text: text.trim().slice(0, 140), why });
    }
  }
  return out.filter((h) => !(h.id === 'home-env' && winEnv.has(h.file)));
}

// A //go:build line at the top means the file already targets one OS (or a tag): not judged,
// except `!tauri_sidecar`, which still builds on Windows.
export function hasBuildTag(src) {
  const m = /^\/\/go:build (.+)$/m.exec(src.split('\n').slice(0, 12).join('\n'));
  return !!m && m[1].trim() !== '!tauri_sidecar';
}

function arg(name, dflt) { const i = process.argv.indexOf(name); return i > 0 ? process.argv[i + 1] : dflt; }

function main() {
  const base = arg('--base', 'origin/develop'), head = arg('--head', 'HEAD');
  const git = (...a) => execFileSync('git', a, { encoding: 'utf8', maxBuffer: 64 << 20 });
  const mb = git('merge-base', base, head).trim();
  const patch = git('diff', '-U0', '-M', '--diff-filter=d', mb, head, '--', '*.go');
  let hits = scanPatch(patch);
  // Drop files that carry a //go:build line (read from the committed text).
  const tagged = new Map();
  hits = hits.filter((h) => {
    if (!tagged.has(h.file)) { let s = ''; try { s = git('show', `${head}:${h.file}`); } catch { /* deleted */ } tagged.set(h.file, hasBuildTag(s)); }
    return !tagged.get(h.file);
  });
  if (hits.length === 0) { console.log('windows-risk: no Windows-fragile lines added.'); return; }
  console.log(`windows-risk: ${hits.length} line(s) added that tend to pass on Linux and fail on Windows (add "// winok: <reason>" when a line is fine):`);
  for (const h of hits) console.log(`  ${h.file}:${h.line}  [${h.id}]  ${h.text}\n      ${h.why}`);
  if (process.argv.includes('--strict')) process.exit(1);
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) main();
