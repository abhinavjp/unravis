const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

const { RECORD_GROUPS } = loadTypeScript('src/lib/blackboxCatalogue.ts');
const { BBW_TEXT, blackboxWizardCopyEntries } = loadTypeScript('src/lib/copy/blackboxWizardCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const figures = RECORD_GROUPS.flatMap((group) => group.figures);
const sql = RECORD_GROUPS.find((group) => group.sqlHost === true);

function catalogueEntries() {
  const entries = [];
  for (const group of RECORD_GROUPS) {
    entries.push({ id: 'catalogue.group.' + group.id, text: group.title });
    for (const figure of group.figures) {
      entries.push({ id: 'catalogue.figure.' + figure.id + '.label', text: figure.label });
      for (const part of ['what', 'why', 'watch', 'stored']) {
        entries.push({ id: 'catalogue.figure.' + figure.id + '.' + part, text: figure.details[part] });
      }
    }
  }
  return entries;
}

test('the SQL group has exactly the 13 SQLHostInstance counter fields, once each', () => {
  assert.ok(sql, 'the SQL host group exists');
  assert.equal(sql.toggle, undefined, 'SQL counters are automatic, not a settings switch');
  assert.deepEqual(sql.figures.map((figure) => figure.id), [
    'sqlHost.batchRequestsPerSec',
    'sqlHost.userConnections',
    'sqlHost.lockWaitsPerSec',
    'sqlHost.processesBlocked',
    'sqlHost.memoryGrantsPending',
    'sqlHost.pageLifeExpectancy',
    'sqlHost.freeListStallsPerSec',
    'sqlHost.compilationsPerSec',
    'sqlHost.reCompilationsPerSec',
    'sqlHost.deadlocksPerSec',
    'sqlHost.lockWaitTimeMs',
    'sqlHost.logFlushWaitTimeMs',
    'sqlHost.forwardedRecordsPerSec'
  ]);
});

test('figure ids are unique and every figure has all four Details parts', () => {
  assert.ok(figures.length > 13, 'the catalogue covers more than SQL');
  assert.equal(new Set(figures.map((figure) => figure.id)).size, figures.length);
  for (const figure of figures) {
    assert.deepEqual(Object.keys(figure.details).sort(), ['stored', 'watch', 'what', 'why'], figure.id);
    for (const part of ['what', 'why', 'watch', 'stored']) {
      assert.equal(typeof figure.details[part], 'string', figure.id + '.' + part);
      assert.ok(figure.details[part].trim().length > 0, figure.id + '.' + part);
    }
  }
});

test('the catalogue has no VM group and no deeper checks as items', () => {
  assert.equal(RECORD_GROUPS.some((group) => group.id.toLowerCase() === 'vm' || /\bvm\b/i.test(group.title)), false);
  assert.equal(figures.some((figure) => /deeper checks/i.test(figure.label)), false);
  // Changed on purpose (browser pass, defect J): the old line, "Deeper checks are not part of this release.", was release
  // jargon a first-time investigator cannot act on. The note now says what is true: no queries, no query text.
  assert.match(BBW_TEXT['sql.deeperChecks'], /never runs queries/);
  assert.match(BBW_TEXT['sql.deeperChecks'], /never reads query text/);
  for (const key of ['sql.deeperChecks', 'sql.singleCounters']) {
    assert.doesNotMatch(BBW_TEXT[key], /release|deeper checks/i, key);
  }
});

test('beginner words: checkboxes are not called switches, and no one is told about housekeeping (defect J)', () => {
  for (const [key, value] of Object.entries(BBW_TEXT)) {
    if (typeof value !== 'string') continue;
    assert.doesNotMatch(value, /switch|housekeeping/i, key);
  }
  assert.match(BBW_TEXT['record.lightHelp'], /checkboxes/);
  assert.match(BBW_TEXT['record.customHelp'], /checkboxes/);
  assert.doesNotMatch(BBW_TEXT['folder.confirmCHelp'], /housekeeping/i);
});

test('the pack Review line never treats the Blackbox name as a server name (defect J)', () => {
  const line = BBW_TEXT['review.packWill']('unravis-blackbox-web-a.zip');
  assert.match(line, /unravis-blackbox-web-a\.zip/);
  assert.match(line, /Copy it to the server/);
  assert.equal(BBW_TEXT['review.packWill'].length, 1, 'it takes the file name only');
  assert.doesNotMatch(line, /Copy it to web-a/);
});

test('the SQL lead says "this PC" on the PC path and "the server" on the pack path (defect J)', () => {
  assert.match(BBW_TEXT['step.sql.lead'], /runs on this PC/);
  assert.match(BBW_TEXT['step.sql.leadPack'], /if SQL Server runs on the server/);
  assert.equal(BBW_TEXT['step.sql.leadPack'], BBW_TEXT['sql.automaticPack']);
  assert.match(BBW_TEXT['step.folder.leadPack'], /Type the full folder on the other server/);
});

test('the name, folder and where messages say what is wrong (defects D, E, F, G)', () => {
  assert.equal(BBW_TEXT['name.space'], 'No spaces. Use a dash instead.');
  assert.match(BBW_TEXT['name.limit'], /Up to 63 characters/);
  assert.match(BBW_TEXT['folder.errFull'], /not a full folder path/);
  assert.match(BBW_TEXT['folder.errSlash'], /backslashes/);
  assert.match(BBW_TEXT['folder.errSlash'], /D:\\Blackbox/);
  assert.match(BBW_TEXT['folder.errChars'], /character/);
  assert.match(BBW_TEXT['folder.errSystem'], /system folder/);
  assert.match(BBW_TEXT['where.scanFailed'], /could not check/);
  assert.match(BBW_TEXT['where.noneHelper'], /helper program/);
  assert.match(BBW_TEXT['where.noneHelper'], /beside the app/);
  assert.doesNotMatch(BBW_TEXT['where.pcUnavailable'], /pack/, 'the "build a pack" hint is added only when a pack can be built');
});

test('only the three real record booleans have catalogue switches', () => {
  assert.deepEqual(RECORD_GROUPS.filter((group) => group.toggle).map((group) => [group.id, group.toggle]), [
    ['processes', 'processes'],
    ['logsCatalogue', 'logsCatalogue'],
    ['copyLogsOnSnapshot', 'copyLogsOnSnapshot']
  ]);
  const machine = RECORD_GROUPS.find((group) => group.id === 'machine');
  assert.equal(machine.always, true);
  assert.equal(machine.toggle, undefined);
});

test('the required beginner hints and folder notices are present', () => {
  assert.match(BBW_TEXT['step.name.help'], /Letters, numbers and dashes\. No spaces\. Start with a letter or number\./);
  assert.doesNotMatch(BBW_TEXT['step.name.help'], /spaces and dashes are fine/i);
  assert.match(BBW_TEXT['watch.noneText'], /electricity meter/);
  assert.match(BBW_TEXT['watch.noneText'], /appliance/);
  assert.match(BBW_TEXT['watch.someText'], /electricity meter/);
  assert.match(BBW_TEXT['notice.systemDrive']('C'), /C:/);
  assert.match(BBW_TEXT['notice.systemDrive']('E'), /E: is the Windows drive/);
  assert.doesNotMatch(BBW_TEXT['notice.systemDrive']('E'), /C:/);
  assert.match(BBW_TEXT['folder.confirmC']('E'), /\(E:\)/);
  assert.doesNotMatch(BBW_TEXT['folder.confirmC']('E'), /C:/);
  assert.match(BBW_TEXT['folder.confirmCError']('E'), /keep E:/);
  assert.match(BBW_TEXT['folder.noOtherDrive']('E'), /only E:/);
  assert.match(BBW_TEXT['notice.unlistedDrive']('E'), /E:/);
  assert.match(BBW_TEXT['folder.errNotLocal'], /local hard drive/);
  assert.match(BBW_TEXT['folder.notLocalReason'], /local hard drive/);
});

test('copy entries cover the flat copy object, including every function sample', () => {
  const entries = blackboxWizardCopyEntries();
  assert.equal(entries.length, Object.keys(BBW_TEXT).length);
  assert.equal(new Set(entries.map((entry) => entry.id)).size, entries.length);
  for (const value of Object.values(BBW_TEXT)) {
    assert.ok(typeof value === 'string' || typeof value === 'function', 'BBW_TEXT stays flat');
  }
});

test('all wizard and catalogue copy passes the screen-string lint and uses no em dashes', () => {
  const entries = [...blackboxWizardCopyEntries(), ...catalogueEntries()];
  assert.ok(entries.length > 150, 'the lint sees all wizard and catalogue copy');
  for (const entry of entries) assert.equal(entry.text.includes('\u2014'), false, entry.id + ' contains an em dash');
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});
