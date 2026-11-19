const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// TSK-03: the folder and keep rules of the Place a Blackbox wizard. The client accepts the same folders as
// the server (internal/blackbox/config.go): a folder on a drive or on a network share, never a drive root,
// a share root or a Windows system folder. The keep ranges are the server's too.

const bb = loadTypeScript('src/lib/blackbox.ts');

const folderProblem = (folder) => bb.settingsProblem({ ...bb.defaultSettings('web-a'), folder });

test('the wizard accepts a drive folder and a network share folder, and refuses roots and system folders', () => {
  assert.equal(folderProblem('D:\\x'), null, 'a folder on a drive');
  assert.equal(folderProblem('\\\\srv\\share\\x'), null, 'a folder on a share (B-04)');
  assert.equal(folderProblem('\\\\srv\\share'), 'folder', 'a share root');
  assert.equal(folderProblem('C:\\'), 'folder', 'a drive root');
  assert.equal(folderProblem('C:\\Windows\\x'), 'folder', 'a Windows system folder');
  assert.equal(folderProblem('relative\\x'), 'folder', 'a path that is not full');
});

test('the refused and accepted folders mirror unsafeFolder and its cases in config.go', () => {
  for (const folder of ['D:\\', 'C:\\', 'C:', '\\\\files01\\share', 'D:\\Windows', 'C:\\Windows\\System32\\blackbox',
    'C:\\Program Files', 'C:\\Program Files\\Unravis', 'C:\\Program Files (x86)\\x', 'C:\\Users', 'D:\\ProgramData',
    '\\\\files01\\share\\Users', '\\\\files01\\share\\Windows\\x']) {
    assert.equal(folderProblem(folder), 'folder', folder);
  }
  for (const folder of ['D:\\UnravisBlackbox', 'C:\\UnravisBlackbox', 'E:\\data\\bb', 'C:\\Users\\svc\\bb',
    'C:\\ProgramData\\Unravis\\bb', '\\\\files01\\share\\bb', '\\\\files01\\share\\bb\\web-a', '\\\\files01\\share\\Users\\svc\\bb']) {
    assert.equal(folderProblem(folder), null, folder);
  }
});

// Changed on purpose (browser pass, defect E): the owner asked the pop-up to refuse forward slashes and the characters
// Windows forbids in a folder name. The server still accepts a forward-slash path; the client is stricter and says why.
test('forward slashes and forbidden characters are refused, each with its own plain fault', () => {
  for (const folder of ['//files01/share/bb', 'd:/UnravisBlackbox', 'D:/x\\y']) {
    assert.equal(folderProblem(folder), 'folder', folder);
    assert.equal(bb.folderFault(folder), 'slash', folder);
  }
  for (const ch of ['<', '>', '"', '|', '?', '*']) {
    assert.equal(bb.folderFault('D:\\bad' + ch + 'name'), 'chars', ch);
    assert.equal(folderProblem('D:\\bad' + ch + 'name'), 'folder', ch);
  }
  // a colon is allowed only as the drive prefix
  assert.equal(bb.folderFault('D:\\a:b'), 'chars');
  assert.equal(bb.folderFault('D:\\a\\b:'), 'chars');
  assert.equal(bb.folderFault('\\\\srv\\sh:are\\x'), 'chars');
  assert.equal(bb.folderFault('D:\\Blackbox'), null);
  assert.equal(bb.folderFault('d:\\Blackbox'), null);
  assert.equal(bb.folderFault('\\\\srv\\share\\x'), null, 'a network path stays accepted (B-04)');
  assert.equal(bb.folderFault('C:\\Windows\\x'), 'system');
  assert.equal(bb.folderFault('D:\\'), 'system');
  assert.equal(bb.folderFault('relative\\x'), 'full');
  assert.equal(bb.folderFault(''), 'full');
  // a path that is not full is reported as such before a slash or a character is blamed
  assert.equal(bb.folderFault('C:'), 'full');
});

test('a network path needs its share: a server alone or bare slashes is a share root', () => {
  assert.equal(bb.unsafeFolder('\\\\files01'), true);
  assert.equal(bb.unsafeFolder('\\\\'), true);
  assert.equal(bb.unsafeFolder('\\\\files01\\'), true);
  assert.equal(bb.unsafeFolder('\\\\files01\\share\\'), true);
  assert.equal(bb.unsafeFolder('\\\\files01\\share\\bb'), false);
  assert.equal(folderProblem('\\\\files01'), 'folder');
  assert.equal(folderProblem('\\\\'), 'folder');
});

test('folderKind says which folder notice a path needs: a drive, a network share, or neither', () => {
  assert.equal(bb.folderKind('D:\\x'), 'drive');
  assert.equal(bb.folderKind('e:/data'), 'drive');
  assert.equal(bb.folderKind(' D:\\x '), 'drive'); // surrounding spaces are ignored, as elsewhere
  assert.equal(bb.folderKind('\\\\srv\\share\\x'), 'unc');
  assert.equal(bb.folderKind('\\\\srv\\share'), 'unc'); // still a share path; the field problem says the folder is not safe
  assert.equal(bb.folderKind('//srv/share/x'), 'unc');
  assert.equal(bb.folderKind('C:'), 'invalid'); // no slash yet
  assert.equal(bb.folderKind('relative\\x'), 'invalid');
  assert.equal(bb.folderKind(''), 'invalid');
  assert.equal(bb.folderKind(' \t '), 'invalid');
  assert.equal(bb.folderKind('\\\\srv'), 'invalid'); // no share
});

test('the keep ranges are the server ranges in config.go: 1 to 365 whole days, 0.1 to 10000 GB, 1 to 50 percent free', () => {
  const good = bb.defaultSettings('web-a');
  for (const keepDays of [1, 7, 365]) assert.equal(bb.settingsProblem({ ...good, keepDays }), null, String(keepDays));
  for (const keepDays of [0, 366, -1, 2.5]) assert.equal(bb.settingsProblem({ ...good, keepDays }), 'keep', String(keepDays));
  for (const maxGB of [0.1, 2, 10000]) assert.equal(bb.settingsProblem({ ...good, maxGB }), null, String(maxGB));
  for (const maxGB of [0, 0.09, 10001, Number.NaN]) assert.equal(bb.settingsProblem({ ...good, maxGB }), 'size', String(maxGB));
  for (const minFreePercent of [1, 10, 50]) assert.equal(bb.settingsProblem({ ...good, minFreePercent }), null, String(minFreePercent));
  for (const minFreePercent of [0, 51, 10.5]) assert.equal(bb.settingsProblem({ ...good, minFreePercent }), 'free', String(minFreePercent));
});
