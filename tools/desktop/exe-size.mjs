// BL-422: size of the app with and without the embedded dashboard (`-tags tauri_sidecar`). Builds both into a temp folder, prints the sizes.
// Usage: node tools/desktop/exe-size.mjs   (needs Go; same flags as the release build: -trimpath -ldflags "-s -w")
import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const repo = resolve(import.meta.dirname, '..', '..');
const dir = mkdtempSync(join(tmpdir(), 'unravis-size-'));
const suffix = process.platform === 'win32' ? '.exe' : '';
const build = (name, tags) => {
  const out = join(dir, name + suffix);
  execFileSync('go', ['build', '-trimpath', '-ldflags', '-s -w', ...(tags ? ['-tags', tags] : []), '-o', out, './cmd/audit-explorer'], { cwd: repo, stdio: 'inherit' });
  return statSync(out).size;
};
try {
  const full = build('full', '');
  const sidecar = build('sidecar', 'tauri_sidecar');
  const mib = (n) => (n / 1048576).toFixed(2) + ' MiB';
  console.log(JSON.stringify({ full, sidecar, saved: full - sidecar }));
  console.log(`with the dashboard embedded: ${mib(full)}; without (tauri_sidecar): ${mib(sidecar)}; saved: ${mib(full - sidecar)}`);
  if (sidecar >= full) { console.error('The sidecar build is not smaller: the tag does not remove the embed.'); process.exitCode = 1; }
} finally {
  rmSync(dir, { recursive: true, force: true });
}
