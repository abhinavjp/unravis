// Self-test for tools/ci/windows-risk.mjs. Run from anywhere: node tools/ci/test-windows-risk.mjs
import assert from 'node:assert/strict';
import { scanPatch, hasBuildTag } from './windows-risk.mjs';

const patch = (file, ...lines) =>
  [`diff --git a/${file} b/${file}`, `--- a/${file}`, `+++ b/${file}`, '@@ -0,0 +1,' + lines.length + ' @@', ...lines.map((l) => '+' + l)].join('\n');
const ids = (p) => scanPatch(p).map((h) => h.id);

// Each rule fires on the line shape that really failed, and names the right line number.
assert.deepEqual(ids(patch('a/x_test.go', 'want := "/p/data/x.db"')), ['unix-path']);
assert.deepEqual(ids(patch('a/x_test.go', 'if fi.Mode().Perm() != 0o600 {')), ['file-mode']);
assert.deepEqual(ids(patch('a/x_test.go', 'os.Chmod(dir, 0o500)')), ['chmod']);
assert.deepEqual(ids(patch('a/x_test.go', 't.Setenv("HOME", dir)')), ['home-env']);
assert.deepEqual(ids(patch('a/x_test.go', 'c := exec.Command("sh", "-c", "true")')), ['shell-out']);
assert.deepEqual(ids(patch('a/x_test.go', 'if time.Since(start) > 50*time.Millisecond {')), ['timing']);
assert.deepEqual(ids(patch('a/x_test.go', 'b, _ := os.ReadFile("testdata/case.golden")')), ['golden-eol']);
assert.deepEqual(ids(patch('a/x_test.go', 'p := `C:\\Users\\me`')), ['abs-drive']);
assert.deepEqual(ids(patch('a/x.go', 'syscall.Kill(pid, syscall.SIGTERM)')), ['unix-syscall']);
assert.equal(scanPatch(patch('a/x_test.go', 'ok', 'os.Chmod(d, 0o500)'))[0].line, 2);

// Review round 1: shapes that must NOT be reported, and shapes that must.
assert.deepEqual(ids(patch('a/x_test.go', 'c := exec.Command(bin, "config", "set", "x", "true")')), []);
assert.deepEqual(ids(patch('a/x_test.go', 'c := exec.CommandContext(ctx, "sh", "-c", "x")')), ['shell-out']);
assert.deepEqual(ids(patch('a/main.go', 'signal.Notify(c, os.Interrupt, syscall.SIGTERM)', 'u := os.Getuid()')), []);
assert.deepEqual(ids(patch('a/x.go', 'a := &syscall.SysProcAttr{Setpgid: true}')), ['unix-syscall']);
assert.deepEqual(ids(patch('a/x_test.go', 't.Setenv("HOME", d)', 't.Setenv("USERPROFILE", d)')), []);
assert.deepEqual(ids(patch('a/x_test.go', 'time.Sleep(10 * time.Millisecond)')), []);
assert.deepEqual(ids(patch('a/x_test.go', 'time.Sleep(50 * time.Millisecond)')), ['timing']);
assert.deepEqual(ids(patch('a/x_test.go', 'if time.Since(s) < 5*time.Second {')), []);
assert.deepEqual(ids(patch('a/x_test.go', 'b, _ := os.ReadFile("testdata/x.json")')), []);
assert.deepEqual(ids(patch('a/x_test.go', 'b, _ := os.ReadFile(filepath.Join("testdata", "x.txt"))')), ['golden-eol']);
assert.deepEqual(ids(patch('a/x_test.go', 'if fi.Mode()&os.ModePerm != 0o600 {')), ['file-mode']);
assert.deepEqual(ids(patch('a/x_test.go', 'want := "a:\\nb"')), []);

// Test-only rules stay quiet in production code; the syscall rule does not.
assert.deepEqual(ids(patch('a/x.go', 'p := "/tmp/x"')), []);
// Escape hatch needs a reason; a bare marker does not count.
assert.deepEqual(ids(patch('a/x_test.go', 'os.Chmod(d, 0o500) // winok: skipped on Windows above')), []);
assert.deepEqual(ids(patch('a/x_test.go', 'os.Chmod(d, 0o500) // winok:')), ['chmod']);
// Files that already target one OS, and comment lines, are not judged.
assert.deepEqual(ids(patch('a/x_windows_test.go', 'os.Chmod(d, 0o500)')), []);
assert.deepEqual(ids(patch('a/x_unix.go', 'syscall.Kill(1, 9)')), []);
assert.deepEqual(ids(patch('a/x_test.go', '// os.Chmod(d, 0o500) is why this skips')), []);
// Removed lines and context lines are never reported.
assert.deepEqual(ids('--- a/a/x_test.go\n+++ b/a/x_test.go\n@@ -1 +1 @@\n-os.Chmod(d, 0o500)\n+ok := 1'), []);
// Clean test code is clean.
assert.deepEqual(ids(patch('a/x_test.go', 'dir := t.TempDir()', 'p := filepath.Join(dir, "x.db")')), []);

assert.equal(hasBuildTag('//go:build windows\n\npackage a\n'), true);
assert.equal(hasBuildTag('package a\n'), false);
assert.equal(hasBuildTag('//go:build !tauri_sidecar\n\npackage a\n'), false);

console.log('test-windows-risk: ok');
