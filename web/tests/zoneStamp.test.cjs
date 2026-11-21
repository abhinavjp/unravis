const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// BL-181: the pickers show and take the wall clock of the chosen zone and keep UTC stamps.
const root = path.resolve(__dirname, '..');
const loaded = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const { utcToWall, wallToUtc } = loadTypeScript('src/lib/zoneStamp.ts');
const bb = loadTypeScript('src/lib/blackbox.ts');

test('UTC is the identity, and text that is not a whole stamp passes through', () => {
  assert.equal(utcToWall('2026-10-20T07:40', 'UTC'), '2026-10-20T07:40');
  assert.equal(wallToUtc('2026-10-20T07:40', 'UTC'), '2026-10-20T07:40');
  for (const text of ['', '2026-10-20', '2026-10-20T7:4']) {
    assert.equal(utcToWall(text, 'Europe/London'), text);
    assert.equal(wallToUtc(text, 'Europe/London'), text);
  }
});

test('London in summer and winter, Dublin, India and Sydney round-trip to the same UTC stamp', () => {
  const cases = [
    ['Europe/London', '2026-10-20T07:40', '2026-10-20T08:40'],
    ['Europe/London', '2026-11-20T07:40', '2026-11-20T07:40'],
    ['Europe/Dublin', '2026-07-01T12:00', '2026-07-01T13:00'],
    ['Asia/Kolkata', '2026-10-20T07:40', '2026-10-20T13:10'],
    ['Australia/Sydney', '2026-10-20T07:40', '2026-10-20T18:40'],
    ['Australia/Sydney', '2026-06-20T07:40', '2026-06-20T17:40']
  ];
  for (const [zone, utc, wall] of cases) {
    assert.equal(utcToWall(utc, zone), wall, zone + ' ' + utc);
    assert.equal(wallToUtc(wall, zone), utc, zone + ' ' + wall);
  }
});

test('a London wall time the clocks skip lands after the gap; one read twice takes the first', () => {
  // 01:30 does not exist on 29 March in London (01:00 GMT jumps to 02:00 BST): it is read as 02:30 BST, which is 01:30 UTC.
  assert.equal(wallToUtc('2026-03-29T01:30', 'Europe/London'), '2026-03-29T01:30');
  assert.equal(utcToWall('2026-03-29T01:30', 'Europe/London'), '2026-03-29T02:30');
  assert.equal(wallToUtc('2026-10-25T01:30', 'Europe/London'), '2026-10-25T00:30');
});

test('Blackbox sample times say the zone they are in', () => {
  assert.equal(bb.shownTime('2026-10-20T09:00:00Z'), '2026-10-20 09:00 UTC');
  assert.match(bb.shownTime('2026-10-20T09:00:00Z', 'Europe/London'), /^2026-10-20 10:00 (BST|GMT\+1)$/);
  assert.equal(bb.shownTime('', 'Europe/London'), '-');
});
