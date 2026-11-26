// Windows-risk scan of the Go lines a change ADDS. A report, not a gate (exit 0) unless --strict.
//   node tools/ci/windows-risk.mjs [--base origin/develop] [--head HEAD] [--strict]
// Why it exists: the fast gate runs on Linux, so a test that only breaks on Windows merges into develop and turns the push run red
// (docs/ci/CI-FAILURE-ANALYSIS-2026-11.md, cause 1). Every rule below comes from a test that really failed that way.
// A line is skipped when it ends with `// winok: <reason>` (the reason is the point: say why it holds on Windows).
// Files named *_windows*, *_linux*, *_unix* and files with a //go:build line are skipped: they already run on one OS only.
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

// [id, test-only, regex, why and the fix]
export const RULES = [
  ['unix-path', true, /["`]\/(p|tmp|home|usr|var|etc|opt|root|mnt)\//,
    'a Unix path literal: on Windows it becomes \\p\\... (TestInstallDir, TestAppProgramOnDisk). Build paths with t.TempDir() and filepath.Join, or compare with filepath.ToSlash.'],
  ['file-mode', true, /\.(Mode\(\)\.Perm\(\)|Perm\(\))|Mode\(\)\s*&\s*0?o?[0-7]{3}\b/,
    'a file permission check: Windows reports 0666 or 0777 whatever was asked (TestOpenSetsFileModeAndFolder, TestStoreFilesAndFolderAreOwnerOnly). Guard with runtime.GOOS or test the ACL in a _windows test.'],
  ['chmod', true, /\bos\.Chmod\(/,
    'os.Chmod does not make a read-only folder or a private file on Windows. Use a _windows file for the ACL, or skip on Windows.'],
  ['home-env', true, /(Setenv|Getenv)\(\s*"(HOME|XDG_[A-Z_]+|TMPDIR)"/,
    'HOME, XDG_* and TMPDIR mean nothing on Windows (USERPROFILE, LOCALAPPDATA, TEMP). Set the Windows variable as well, or use t.TempDir().'],
  ['shell-out', true, /exec\.Command(Context)?\([^)]*"(sh|bash|\/bin\/[a-z]+|cat|ls|sleep|true|false)"/,
    'a Unix program: not on a Windows PC. Use the test binary itself (os.Args[0] with a helper-process env var) or skip on Windows.'],
  ['unix-syscall', false, /\bsyscall\.(Kill|SIGTERM|SIGKILL|SIGUSR[12]|Setpgid|Getuid|Getgid|Umask|Mkfifo|Stat_t)\b|\bos\.(Getuid|Getgid)\(/,
    'a Unix-only call: it does not compile on Windows, or behaves differently. Move it to a _unix.go or _linux.go file with a _windows.go twin.'],
  ['timing', true, /time\.Since\([^)]*\)\s*[<>]=?\s*[0-9a-zA-Z_.*s]*(Millisecond|Second)|time\.Sleep\(\s*[0-9]+\s*\*\s*time\.Millisecond\)/,
    'a wall-clock budget or a short sleep: it flakes on a loaded runner (TestMaskLongNamesAreLinear, TestLockWaitHonoursContext). Wait on a channel or a condition with a generous timeout; if you must time it, count work, not seconds.'],
  ['golden-eol', true, /(ReadFile|Open)\([^)]*(golden|\.golden|testdata\/[a-z0-9_.\/-]*\.(txt|json|md|ts|js))/,
    'a text fixture compared byte for byte: a Windows checkout may turn LF into CRLF (TestGoldenFixture, TestGoldenDts). Normalise "\\r\\n" to "\\n" at the read point, or mark the file eol=lf in .gitattributes.'],
  ['abs-drive', true, /["`][A-Za-z]:\\\\?/,
    'a drive-letter path in a test that also runs on Linux: it is a relative name there. Put it in a _windows test or build it from t.TempDir().'],
];

const SKIP_FILE = /_(windows|linux|unix|darwin)(_test)?\.go$|_windows_|_linux_|_unix_/;

export function scanPatch(patch) {
  // patch: output of `git diff -U0 --no-renames base head -- '*.go'`
  const out = [];
  let file = null, line = 0;
  for (const raw of patch.split('\n')) {
    const h = /^\+\+\+ b\/(.+)$/.exec(raw);
    if (h) { file = h[1]; continue; }
    const hunk = /^@@ -\S+ \+(\d+)(?:,\d+)? @@/.exec(raw);
    if (hunk) { line = Number(hunk[1]); continue; }
    if (!file || !raw.startsWith('+') || raw.startsWith('+++')) continue;
    const text = raw.slice(1);
    const here = line++;
    if (SKIP_FILE.test(file) || /\/\/\s*winok:\s*\S/.test(text)) continue;
    if (/^\s*\/\//.test(text)) continue; // a comment line
    const isTest = file.endsWith('_test.go');
    for (const [id, testOnly, re, why] of RULES) {
      if (testOnly && !isTest) continue;
      if (re.test(text)) out.push({ file, line: here, id, text: text.trim().slice(0, 140), why });
    }
  }
  return out;
}

// A //go:build line at the top means the file already targets one OS (or a tag): not judged.
export function hasBuildTag(src) { return /^\/\/go:build /m.test(src.split('\n').slice(0, 12).join('\n')); }

function arg(name, dflt) { const i = process.argv.indexOf(name); return i > 0 ? process.argv[i + 1] : dflt; }

function main() {
  const base = arg('--base', 'origin/develop'), head = arg('--head', 'HEAD');
  const git = (...a) => execFileSync('git', a, { encoding: 'utf8', maxBuffer: 64 << 20 });
  const mb = git('merge-base', base, head).trim();
  const patch = git('diff', '-U0', '--no-renames', '--diff-filter=d', mb, head, '--', '*.go');
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
