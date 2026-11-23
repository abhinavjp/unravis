// Self-test of tools/ci/check-backlog-ids.mjs, and the real file. Run: node tools/ci/test-backlog-ids.mjs
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { findDuplicateIds } from './check-backlog-ids.mjs';

const ids = (t) => findDuplicateIds(t).map((d) => `${d.id}@${d.lines.join(',')}`);
assert.deepEqual(ids('- [ ] **BL-001** a\n- [x] **BL-002** b\n'), []);
assert.deepEqual(ids('- [ ] **BL-001** a\n- [x] **BL-001** b\n'), ['BL-001@1,2']);
// A lettered id is a different id; a prefix is not a clash.
assert.deepEqual(ids('- [ ] **BL-441** a\n- [ ] **BL-441a** b\n- [ ] **BL-441b** c\n'), []);
// A renumbered item names its old id as a reference only.
assert.deepEqual(ids('- [ ] **BL-503** (was BL-461) a\n- [ ] **BL-461** b\n'), []);
// A heading that starts with an id counts as a definition; a heading that only mentions one does not.
assert.deepEqual(ids('### BL-010 Lane\n- [ ] **BL-010** x\n'), ['BL-010@1,2']);
assert.deepEqual(ids('### Blackbox wizard (BL-130)\n- [ ] **BL-130** x\n'), []);
// Sweep ids; indented lines; a fenced example is skipped.
assert.deepEqual(ids('- [ ] **SWP-1021-1** a\n- [ ] **SWP-1021-1** b\n'), ['SWP-1021-1@1,2']);
assert.deepEqual(ids('- [ ] **BL-001** a\n  - [ ] **BL-001** b\n'), ['BL-001@1,2']);
assert.deepEqual(ids('```\n- [ ] **BL-001** a\n- [ ] **BL-001** b\n```\n'), []);
assert.deepEqual(ids('- [ ] **BL-001** a\r\n- [ ] **BL-001** b\r\n'), ['BL-001@1,2']);
// The real file.
const real = findDuplicateIds(fs.readFileSync(new URL('../../docs/BACKLOG.md', import.meta.url), 'utf8'));
assert.deepEqual(real, [], 'docs/BACKLOG.md defines an id twice');
console.log('test-backlog-ids ok');
