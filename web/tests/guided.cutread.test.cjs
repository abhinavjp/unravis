const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./guided.fixture.cjs');

// A cut read has two causes: the call limit, or a source that stopped part way (RV-SRV-11). The fixed line on the screen and in the
// saved report must be true for both, so it never says "limit" and leaves the cause to the note the server prints under it.
const { GUIDED_TEXT } = loadTypeScript('src/lib/copy/guidedCopy.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', 'src', p), 'utf8');

test('the cut-read line does not name a cause, and points to the note that does', () => {
  assert.equal(GUIDED_TEXT.readCut, 'This read was cut short, so some calls are not in the figures. The note under it says why.');
  assert.doesNotMatch(GUIDED_TEXT.readCut, /limit|too many|narrow|try/i);
});

test('the guided screen and the saved report both use that line for a cut read', () => {
  assert.match(read('lib/components/guided/StepFacts.svelte'), /\{T\.readCut\}/);
  assert.match(read('lib/components/guided/ReportExport.svelte'), /T\.readCut/);
});
