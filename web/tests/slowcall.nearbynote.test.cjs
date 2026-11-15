const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P6 (C-4, C-7): lane gap wording "no file", one combined Nearby note.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { DRILL_TEXT } = loadTypeScript('src/lib/drill/drillCopy.ts');
const { laneGap, laneGapText } = loadTypeScript('src/lib/drill/callLanes.ts');
const read = (p) => fs.readFileSync(path.join(root, p), 'utf8').replace(/\r\n/g, '\n');

test('C-4: laneGap maps no-data to no-file and laneGapText returns the import prompt', () => {
  assert.equal(laneGap({ state: 'no-data' }), 'no-file');
  assert.equal(laneGapText('no-file'), 'This case has no file for this lane. Import one to see it here.');
  assert.equal(laneGapText('no-file'), DRILL_TEXT.laneNoFile);
});

test('C-7: CallNearby.svelte renders a single three-branch note block so capped and truncated never both show', () => {
  const src = read('src/components/CallNearby.svelte');
  const block = [
    '  {#if capped.capped && truncated}',
    '    <p class="status" data-testid="scall-nearby-capped">{DRILL_TEXT.nearbyCappedAndTruncated(capped.rows.length)}</p>',
    '  {:else if capped.capped}',
    '    <p class="status" data-testid="scall-nearby-capped">{DRILL_TEXT.rowsCapped(capped.rows.length, capped.total)}</p>',
    '  {:else if truncated}',
    '    <p class="status" data-testid="scall-nearby-truncated">{DRILL_TEXT.truncated(capped.rows.length)}</p>',
    '  {/if}'
  ].join('\n');
  const idx = src.indexOf(block);
  assert.ok(idx >= 0, 'expected three-branch note block in CallNearby.svelte');
  const iBoth = src.indexOf('{#if capped.capped && truncated}');
  const iCapped = src.indexOf('{:else if capped.capped}');
  const iTrunc = src.indexOf('{:else if truncated}');
  assert.ok(iBoth >= 0 && iCapped > iBoth && iTrunc > iCapped, 'branches are chained with {:else if}');
  assert.equal(src.indexOf('{#if truncated}'), -1, 'truncated must not be a standalone {#if}');
  assert.equal(
    DRILL_TEXT.nearbyCappedAndTruncated(200),
    'Showing the first 200; more calls exist in this window than can be listed'
  );
});
