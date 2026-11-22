// BL-335: every `uses:` in .github/workflows must be pinned to a 40-hex commit SHA,
// local (./) actions and docker:// digests excepted, and no aquasecurity/ actions.
// Usage: node tools/security/check-action-pins.mjs [file-or-dir ...]   (default .github/workflows)
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

export function checkText(text, name = 'workflow') {
  const bad = [];
  text.split(/\r?\n/).forEach((line, i) => {
    const m = line.match(/^\s*(?:-\s+)?uses:\s*['"]?([^\s'"#]+)/);
    if (!m) return;
    const ref = m[1];
    if (ref.startsWith('./')) return;
    if (ref.startsWith('docker://')) {
      if (!/@sha256:[0-9a-f]{64}$/.test(ref)) bad.push(`${name}:${i + 1}: docker image not pinned by digest: ${ref}`);
      return;
    }
    if (/^aquasecurity\//i.test(ref)) bad.push(`${name}:${i + 1}: aquasecurity actions are banned: ${ref}`);
    if (!/@[0-9a-f]{40}$/.test(ref)) bad.push(`${name}:${i + 1}: not pinned to a 40-hex SHA: ${ref}`);
  });
  return bad;
}

function files(p) {
  if (statSync(p).isDirectory()) {
    return readdirSync(p).filter((f) => /\.ya?ml$/.test(f)).map((f) => join(p, f));
  }
  return [p];
}

if (import.meta.url === `file://${process.argv[1]}` || process.argv[1]?.endsWith('check-action-pins.mjs')) {
  const targets = process.argv.length > 2 ? process.argv.slice(2) : ['.github/workflows'];
  const bad = targets.flatMap(files).flatMap((f) => checkText(readFileSync(f, 'utf8'), f));
  bad.forEach((b) => console.error(b));
  if (bad.length) process.exit(1);
  console.log('action pins ok');
}
