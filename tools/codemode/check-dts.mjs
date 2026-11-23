// BL-364: compile the committed code-mode declarations (internal/codemode/bindings/testdata/unravis.d.ts) with the TypeScript compiler the
// web build already pins. The compiler is a CI test tool only; it is never shipped. The golden file is checked against the Go generator by
// `go test ./internal/codemode/bindings`; this script checks that the file is valid TypeScript and that it types a script as intended:
// scripts/ok.ts must compile, every file under scripts/bad/ must be refused.
//
// Run from anywhere after `npm ci` in web:  node tools/codemode/check-dts.mjs
import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdtempSync, readdirSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const data = join(root, 'internal', 'codemode', 'bindings', 'testdata');
const tsc = join(root, 'web', 'node_modules', 'typescript', 'bin', 'tsc');
if (!existsSync(tsc)) {
  console.error('typescript is not installed: run npm ci in web first');
  process.exit(2);
}

const work = mkdtempSync(join(tmpdir(), 'unravis-dts-'));
copyFileSync(join(data, 'unravis.d.ts'), join(work, 'unravis.d.ts'));
writeFileSync(
  join(work, 'tsconfig.json'),
  JSON.stringify({ compilerOptions: { strict: true, noEmit: true, target: 'ES2022', lib: ['ES2022'], types: [], skipLibCheck: false }, files: ['unravis.d.ts', 'script.ts'] }),
);

function compile(scriptPath) {
  copyFileSync(scriptPath, join(work, 'script.ts'));
  try {
    execFileSync(process.execPath, [tsc, '-p', work], { stdio: 'pipe' });
    return { ok: true, output: '' };
  } catch (e) {
    return { ok: false, output: String(e.stdout || e.message) };
  }
}

let failed = false;
try {
  const good = compile(join(data, 'scripts', 'ok.ts'));
  if (!good.ok) {
    failed = true;
    console.error('scripts/ok.ts does not compile:\n' + good.output);
  }
  const badDir = join(data, 'scripts', 'bad');
  const bad = readdirSync(badDir).filter((f) => f.endsWith('.ts'));
  if (bad.length < 5) {
    failed = true;
    console.error(`only ${bad.length} scripts under scripts/bad`);
  }
  for (const f of bad) {
    if (compile(join(badDir, f)).ok) {
      failed = true;
      console.error(`scripts/bad/${f} was accepted by the compiler`);
    }
  }
} finally {
  rmSync(work, { recursive: true, force: true });
}
if (failed) process.exit(1);
console.log('unravis.d.ts compiles and types the sample scripts as intended');
