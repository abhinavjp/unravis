const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const file = path.resolve(__dirname, '../src/lib/sourceWizard.ts');
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
const mod = { exports: {} };
new Function('module', 'exports', out)(mod, mod.exports);
const { railGroups, canFinish } = mod.exports;

test('rail groups keep the order each type first appears and several of one kind', () => {
  const list = [
    { kind: 'redis', ref: 'a', label: 'A' }, { kind: 'file', ref: 'b', label: 'B' }, { kind: 'redis', ref: 'c', label: 'C' }
  ];
  const groups = railGroups(list, (s) => s.kind);
  assert.deepEqual(groups.map((g) => [g.title, g.items.map((i) => i.ref)]), [['redis', ['a', 'c']], ['file', ['b']]]);
});

test('an empty list has no groups', () => assert.deepEqual(railGroups([], () => 'x'), []));

test('last button rules', () => {
  assert.equal(canFinish(true, 0, false, false), false);
  assert.equal(canFinish(true, 2, false, false), true);
  assert.equal(canFinish(true, 2, false, true), false);
  assert.equal(canFinish(false, 0, false, false), true);
  assert.equal(canFinish(false, 1, true, false), false);
});
