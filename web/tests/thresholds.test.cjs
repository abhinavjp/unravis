const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

// Value of `const <name> = <integer>` in a source file.
const constant = (source, pattern, label) => {
  const match = source.match(pattern);
  assert.ok(match, 'could not find ' + label);
  return Number(match[1]);
};

test('web sample thresholds equal the Go audit thresholds', () => {
  const go = read('../internal/audit/thresholds.go');
  const ts = read('src/lib/thresholds.ts');
  assert.equal(constant(ts, /export const MIN_SAMPLE = (\d+);/, 'MIN_SAMPLE'), constant(go, /\bMinSampleCount\s*=\s*(\d+)/, 'MinSampleCount'));
  assert.equal(constant(ts, /export const P99_MIN_SAMPLE = (\d+);/, 'P99_MIN_SAMPLE'), constant(go, /\bP99MinSampleCount\s*=\s*(\d+)/, 'P99MinSampleCount'));
});
