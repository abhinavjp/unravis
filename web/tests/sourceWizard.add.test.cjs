const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

test('add mode: the flow reuses the source step and runs against the open case without making a new one', () => {
  const flow = read('src/components/sourcewiz/AddSourcesFlow.svelte');
  assert.match(flow, /<SourceStep adding \{caseId\} \{closed\} \{closedNote\} \{existing\} \{onFiles\} \/>/);
  assert.match(flow, /createView\.submit\.caseId = target/);
  assert.match(flow, /lastButton\(true, wizard\.sources\.length, createView\.running\)/);
  assert.match(flow, /pending\.length === 0/, 'the ack, file and computer checks are made here, because createNow skips them once a case is set');
  assert.match(flow, /listSourceTags\(caseId/);
});

test('add mode: the Add data tab of the case hub mounts the flow and returns to the list when done', () => {
  const hub = read('src/components/sources/SourcesHub.svelte');
  assert.match(hub, /<AddSourcesFlow \{caseId\} \{readOnly\} onFiles=\{onAddFiles\} onDone=\{\(\) => \{ tab = 'list'; \}\} \/>/);
});
