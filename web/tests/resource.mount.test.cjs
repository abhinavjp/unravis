// BL-495j: the Resource counters panel on the case page shows only for a case that holds counter files, and keeps its own window.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const span = loadTypeScript('src/lib/resourceSpan.ts');

test('resourceFiles keeps the kept counter files and nothing else (IIS and app logs do not make a resource block)', () => {
  const records = [
    { id: 1, status: 'ok', importer: 'iis-w3c' },
    { id: 2, status: 'ok', importer: 'log4net-text' },
    { id: 3, status: 'ok', importer: 'win-counters' },
    { id: 4, status: 'refused', importer: 'win-counters' },
    { id: 5, status: 'ok', importer: 'sql-pdh' },
    { id: 6, importer: 'redis-info' },
    { id: 7, status: 'ok' }
  ];
  assert.deepEqual(span.resourceFiles(records).map((r) => r.id), [3, 5, 6]);
  assert.deepEqual(span.resourceFiles([{ status: 'ok', importer: 'iis-w3c' }]), [], 'a log-only case has none');
  assert.deepEqual(span.resourceFiles(null), []);
});

test('the importer list equals the sources of the server catalogue (internal/resource/metrics.go)', () => {
  const go = fs.readFileSync(path.join(root, '..', 'internal', 'resource', 'metrics.go'), 'utf8');
  const sources = [...go.matchAll(/^\s+source\w+\s+=\s+"([a-z0-9-]+)"/gm)].map((m) => m[1]).sort();
  assert.ok(sources.length >= 10, 'found the source constants');
  assert.deepEqual([...span.RESOURCE_IMPORTERS].sort(), sources);
});

test('the case page mounts the panel only when the case holds counter files, with its own window', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const at = home.indexOf('<ResourcePanel');
  assert.ok(at > 0);
  const before = home.slice(Math.max(0, at - 300), at);
  assert.match(before, /\{#if counterFiles\.length > 0\}/, 'a log-only case mounts nothing');
  assert.match(home.slice(at, at + 260), /\bownWindow\b/, 'the panel does not move the analysis zoom from the case page');
  assert.match(home, /const counterFiles = \$derived\(resourceFiles\(imports\)\)/);
  assert.match(home, /caseSpan\(counterFiles\)/, 'the period offered is the counter files\' own, not the logs\'');
});

test('ownWindow: the panel neither follows nor moves the analysis zoom, and a long host name wraps', () => {
  const panel = read('src/components/ResourcePanel.svelte');
  assert.match(panel, /const usesStore = \$derived\(!ownWindow && /);
  assert.match(panel, /zoomed: !ownWindow && zoomView\.zoomed, slots: ownWindow \? \[\] : zoomView\.slots/);
  assert.match(panel, /\.host-text h4 \{ overflow-wrap: anywhere;/);
});
