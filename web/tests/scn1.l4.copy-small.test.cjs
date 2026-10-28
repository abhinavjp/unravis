const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');

const autorun = loadTypeScript('src/lib/autorun.ts');
const incomplete = loadTypeScript('src/lib/incomplete.ts');
const overview = loadTypeScript('src/lib/overview.ts');
const time = loadTypeScript('src/lib/time.ts');
const scopeAt = (extra) => ({ ...time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']), ...extra });

test('D-A-38: the hint and the problem say the same sentence for a reference window that ends too late', () => {
  const query = scopeAt({ baselineEnd: scopeAt().end });
  assert.equal(autorun.scopeHint(query, true), 'The reference window must end at or before the current window starts.');
  assert.equal(autorun.scopeProblem(query, true), 'The reference window must end at or before the current window starts.');
});

test('D-A-38: a reference window that ends exactly when the current one starts still runs', () => {
  const base = scopeAt();
  const query = scopeAt({ baselineEnd: base.start });
  assert.equal(autorun.scopeProblem(query, true), null);
  assert.equal(autorun.scopeHint(query, true), '');
});

test('D-A-41: the breakdown intro keeps a space after the full stop in both branches', () => {
  const panel = read('src/components/BreakdownPanel.svelte');
  assert.match(panel, /share\.\{#if bucket\}\{' '\}Rows count requests only\.\{:else\}\{' '\}Choose a row to list matching requests\.\{\/if\}/);
});

test('D-A-43: the stale list heading says the button count covers every age', () => {
  const note = 'the button count covers every age, so this list can be shorter';
  const drill = incomplete.staleDrill(null, 'incomplete', 10);
  assert.equal(drill.note, note);
  assert.match(overview.drillHeading(drill), /\(the button count covers every age, so this list can be shorter\)$/);
  const operation = { controller: 'Payroll', action: 'Run', kind: 'synchronous' };
  const pending = incomplete.staleDrill(operation, 'pending', 2.5);
  assert.equal(pending.note, drill.note);
  const scoped = incomplete.scopeStaleDrill(drill, operation);
  assert.equal(scoped.note, drill.note);
});
