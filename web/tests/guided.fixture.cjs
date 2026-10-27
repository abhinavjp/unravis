// Shared by the guided tests: a TypeScript loader and a small, valid engine answer that each test bends.
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const ts = require('typescript');

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

const change = (refMs, incMs, extra = {}) => {
  const deltaMs = incMs - refMs;
  const deltaPct = refMs > 0 ? (deltaMs / refMs) * 100 : undefined;
  const meaningful = deltaMs >= 100 && deltaPct >= 30 || deltaMs <= -100 && deltaPct <= -30;
  return { known: true, refMs, incMs, deltaMs, deltaPct, meaningful, direction: deltaMs > 0 ? 'up' : deltaMs < 0 ? 'down' : 'flat', markPercent: 30, markMs: 100, ...extra };
};
const unknown = () => ({ known: false, meaningful: false, direction: 'flat', markPercent: 30, markMs: 100 });

function step(number, readiness = 'Ready', extra = {}) {
  return { number, name: String(number), readiness, reasons: [], nutshell: '', findings: [], ...extra };
}

function suspect(id, colour, extra = {}) {
  const result = { red: 'Consistent', orange: 'Partly consistent', green: 'Contradicted', grey: 'Cannot test yet' }[colour];
  return { id, group: id[0], title: 'Suspect ' + id, result, colour, why: 'It fits.', evidence: [], nextCheck: 'Look at ' + id + '.', overturn: 'If ' + id + ' is flat.', rank: 1, ...extra };
}

/** A small engine answer: 10 minutes of slow calls against a normal hour. */
function result(extra = {}) {
  const base = {
    version: 'test', thresholds: { version: 't', changeMinPercent: 30, changeMinMs: 100 }, headline: 'The slow end rose.',
    facts: {
      counts: { read: 1000, noStart: 0, incomplete: 0, anomalous: 0, incident: 400, reference: 600, outside: 0, used: 1000 },
      incident: { meanMs: 900, perSec: 2, testableMinutes: 10 },
      reference: { meanMs: 300, perSec: 2, testableMinutes: 10 },
      routes: [
        { route: 'A.One', incident: { n: 300, p95: 3000 }, reference: { n: 300, p95: 500 }, p95Change: change(500, 3000) },
        { route: 'B.Two', incident: { n: 100, p95: 410 }, reference: { n: 300, p95: 400 }, p95Change: change(400, 410) }
      ],
      change: { p50: change(200, 300), p95: change(500, 3000), mean: change(300, 900), known: true, meaningful: true },
      onset: { status: 'found', t0: '2026-10-17T10:42:00Z', why: '' }, shape: { kind: 'step', why: '' }, scope: { kind: 'one', testable: 1, changed: 1, why: '' },
      parts: {
        items: [
          { name: 'A', incN: 1, refN: 1, incMeanMs: 100, refMeanMs: 90, change: change(90, 100), tail: unknown(), tested: true, share: 0.1 },
          { name: 'C', incN: 1, refN: 1, incMeanMs: 700, refMeanMs: 100, change: change(100, 700), tail: unknown(), tested: true, share: 0.8 },
          { name: 'O', incN: 0, refN: 0, change: unknown(), tail: unknown(), tested: false, why: 'No IIS source.' }
        ],
        dominant: 'C', dominantWhy: '', oTested: false, oWhy: '', iisMatched: 0
      },
      load: { planningPerSec: 2, incPerSec: 2, refPerSec: 2, incWms: 900, refWms: 300, incL: 1.8, refL: 0.6, incPeak: 9, refPeak: 3, lambdaDirection: 'flat', wDirection: 'up', lDirection: 'up', flatTop: false, reading: '' },
      retries: { known: false, incRetried: 0, refRetried: 0, direction: 'flat' }, errors: { known: false, incServer: 0, refServer: 0 },
      clock: { checked: 100, anomalous: 0, share: 0, anomaly: 'no' }, coverage: { iisRows: 0, dip: 'unknown' }, gaps: [], tiers: []
    },
    signals: { flags: {}, split: { shares: [], dominant: 'C' }, pool: { entered: false, wouldFill: 'unknown', atLimit: 'unknown', reading: '', why: '' }, changes: { entered: false, items: [], verdict: 'unknown', why: '', caveat: '' } },
    steps: Array.from({ length: 11 }, (_, i) => step(i)),
    suspects: [suspect('L1', 'red', { rank: 1 }), suspect('D1', 'orange', { rank: 2 }), suspect('M1', 'grey', { rank: 3, needs: 'a MongoDB source' }), suspect('H1', 'green', { rank: 4 })],
    notApplicable: [],
    origin: { status: 'named', tier: 'app', label: 'Application', at: '2026-10-17T10:42:00Z', roles: [{ tier: 'app', label: 'Application', role: 'Moved first', at: '2026-10-17T10:42:00Z', why: 'The slow end rose.' }], note: '', clockNote: '' },
    confidence: { word: 'Medium', why: '', sourcesUsed: ['Audit'], sourcesMissing: ['MongoDB'], notTested: ['M1'], overturn: '' },
    seriesMinutes: 0
  };
  return { ...base, ...extra };
}

module.exports = { loadTypeScript, change, unknown, step, suspect, result };
