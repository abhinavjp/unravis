const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The log4net block log (importer log4net-blocks): the name guess for its usual file names, the words shown for it, and its size cap.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}

const kinds = loadTypeScript('src/lib/fileKinds.ts');
const copy = loadTypeScript('src/lib/copy/fileKindsCopy.ts');
const importCopy = loadTypeScript('src/lib/copy/importCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('the usual file names of a log4net block log are suggested as an app log', () => {
  for (const name of ['log.txt', 'Log.TXT', 'log.txt.1', 'log.txt.12', 'app-log.txt', 'orders_log.txt', 'api.log.txt', 'logs\\log.txt', 'C:\\Apps\\web-a\\logs\\log.txt', 'logs/log.txt.3']) {
    assert.equal(kinds.detectKind(name), 'app-log', name);
  }
});

test('names that only look a little like a log4net file are left alone', () => {
  for (const name of ['catalog.txt', 'blog.txt', 'log.txt.bak', 'log.txt.gz', 'log.txt.zip', 'logs.txt', 'log.text', 'log.txt.', 'notes.txt', 'log']) {
    assert.equal(kinds.detectKind(name), '', name);
  }
});

test('the older name rules still win over the new one', () => {
  assert.equal(kinds.detectKind('iis-log.txt'), 'iis-w3c');
  assert.equal(kinds.detectKind('httperr-log.txt'), 'iis-httperr');
  assert.equal(kinds.detectKind('sql-errorlog.txt'), 'sql-errorlog');
});

test('an app log is still read by the server, with no importer named', () => {
  assert.equal(kinds.importerFor(kinds.detectKind('log.txt')), '');
});

test('the importer has a plain label, a help line and the 2 GiB cap', () => {
  assert.equal(importCopy.importerLabel('log4net-blocks'), 'Application log (log4net text blocks)');
  assert.match(importCopy.importerHelp('log4net-blocks'), /logs folder/);
  assert.notEqual(importCopy.importerLabel('log4net-blocks'), importCopy.importerLabel('log4net-text'));
  const src = fs.readFileSync(path.join(root, 'src/lib/cases.ts'), 'utf8');
  assert.match(src, /'log4net-blocks': 2 \* GiB,/);
});

test('the app log text names the log4net block layout and the card guide points at the folder the app writes logs to', () => {
  assert.match(copy.KIND_READS['app-log'], /log4net/);
  assert.match(copy.KIND_READS['app-log'], /Server Time/);
  const guide = copy.CARD_GUIDES.find((g) => g.id === 'app');
  assert.ok(guide);
  assert.match(guide.steps.join(' '), /folder your app writes logs to/);
});

test('the new words pass the copy lint', () => {
  assert.deepEqual(lintScreenStrings(copy.fileKindsCopyEntries(), deck.terms), []);
  const entries = [
    { id: 'log4net.blocks.label', text: importCopy.importerLabel('log4net-blocks') },
    { id: 'log4net.blocks.help', text: importCopy.importerHelp('log4net-blocks') }
  ];
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});
