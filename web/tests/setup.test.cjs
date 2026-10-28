const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

// The setup window's pure logic (web/setup/logic.ts): words, rail, routing after a run, the checks the page makes before asking setup,
// and the request it sends.
const root = path.resolve(__dirname, '..');
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');
function loadTypeScript(relativePath) {
  const output = ts.transpileModule(read(relativePath), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  new Function('module', 'exports', output)(module, module.exports);
  return module.exports;
}
const logic = loadTypeScript('setup/logic.ts');

const goodOptions = () => ({
  scope: 'user',
  location: 'C:\\Users\\ana\\AppData\\Local\\Programs\\Unravis',
  startAtSignIn: true,
  closeRunningApp: true,
  blackbox: { name: 'web-01', folder: 'D:\\UnravisBlackbox', keepDays: 7, maxGB: 2, minFreePercent: 10, port: 7788 }
});
const fields = (problems) => problems.map((problem) => problem.field);

test('readiness and step words are the brand words, and pending is never green', () => {
  assert.deepEqual(['ready', 'partly', 'failed'].map(logic.checkWord), ['Ready', 'Partly ready', 'Failed']);
  assert.deepEqual(['waiting', 'running', 'done', 'failed'].map(logic.stepWord), ['Waiting', 'Running', 'Done', 'Failed']);
  assert.equal(logic.stepWord('undone'), 'Put back');
  assert.equal(logic.stepWord('left'), 'Left behind');
  assert.equal(logic.stepWord('skipped'), 'Skipped');
  assert.notEqual(logic.stepTone('waiting'), 'ok');
  assert.equal(logic.stepTone('done'), 'ok');
  assert.equal(logic.stepTone('failed'), 'bad');
  assert.equal(logic.checkTone('failed'), 'bad');
});

test('a failed check blocks; a partly ready one does not', () => {
  assert.equal(logic.blocked([{ id: 'reboot', status: 'partly' }, { id: 'windows', status: 'ready' }]), false);
  assert.equal(logic.blocked([{ id: 'install-folder', status: 'failed' }]), true);
});

test('the rail has Options only for an install, and uninstall starts at Remove', () => {
  assert.deepEqual(logic.railSteps('install').map((step) => step.label), ['Welcome', 'Options', 'Approval', 'Install', 'Done']);
  assert.deepEqual(logic.railSteps('upgrade').map((step) => step.label), ['Welcome', 'Approval', 'Upgrade', 'Done']);
  assert.deepEqual(logic.railSteps('repair').map((step) => step.label), ['Welcome', 'Approval', 'Repair', 'Done']);
  assert.deepEqual(logic.railSteps('uninstall').map((step) => step.label), ['Remove', 'Approval', 'Progress', 'Done']);
});

test('the rail marks Approval as not needed when no administrator rights were asked for', () => {
  assert.deepEqual(logic.railPosition('install', 'progress', false), { current: 'progress', done: ['welcome', 'options'], skipped: ['approval'] });
  assert.deepEqual(logic.railPosition('install', 'progress', true), { current: 'progress', done: ['welcome', 'options', 'approval'], skipped: [] });
  assert.equal(logic.railPosition('install', 'failed', true).current, 'progress');
  assert.deepEqual(logic.railPosition('install', 'done', null).done, ['welcome', 'options', 'approval', 'progress', 'done']);
});

test('headings follow the wireframes', () => {
  assert.equal(logic.welcomeHeading({ mode: 'install', version: '1.0.0' }), 'Install Unravis');
  assert.equal(logic.welcomeHeading({ mode: 'upgrade', version: '1.0.0', installed: { version: '0.9.2' } }), 'Upgrade Unravis 0.9.2 to 1.0.0');
  assert.equal(logic.welcomeHeading({ mode: 'repair', version: '1.0.0' }), 'Repair Unravis 1.0.0');
  assert.equal(logic.welcomeHeading({ mode: 'uninstall', version: '1.0.0' }), 'Remove Unravis');
  assert.equal(logic.doneHeading('install'), 'Unravis is installed');
  assert.equal(logic.failedHeading({ outcome: 'rolled-back', op: 'install' }), 'Install did not finish. Nothing was left behind.');
  assert.equal(logic.failedHeading({ outcome: 'rollback-incomplete', op: 'install' }), 'Install did not finish. Some items are left.');
  assert.equal(logic.failedHeading({ outcome: 'cancelled', op: 'upgrade' }), 'Upgrade was cancelled. Nothing was left behind.');
});

test('events fold into one row per step in first-seen order, keeping the last detail', () => {
  let rows = logic.mergeEvents([], [
    { id: 'a', title: 'A', state: 'waiting' },
    { id: 'b', title: 'B', state: 'waiting' }
  ]);
  rows = logic.mergeEvents(rows, [
    { id: 'a', title: 'A', state: 'running' },
    { id: 'a', title: 'A', state: 'done', detail: 'C:\\x' },
    { id: 'a', title: 'A', state: 'undone' }
  ]);
  assert.deepEqual(rows.map((row) => [row.id, row.state, row.detail]), [['a', 'undone', 'C:\\x'], ['b', 'waiting', undefined]]);
  assert.equal(logic.progressPercent(rows), 50);
  assert.deepEqual(logic.settleSteps(rows).map((row) => row.state), ['undone', 'skipped']);
  assert.equal(logic.progressPercent([]), 0);
});

test('a finished run leads to the right screen, including a cancel at the Windows prompt', () => {
  const to = (outcome, op = 'install') => logic.screenAfterResult({ outcome, op });
  assert.equal(to('done'), 'done');
  assert.equal(to('done-with-leftovers', 'uninstall'), 'done');
  assert.equal(to('cancelled-at-uac'), 'approval');
  assert.equal(to('preflight-failed'), 'welcome');
  assert.equal(to('preflight-failed', 'uninstall'), 'uninstall');
  assert.equal(to('invalid'), 'options');
  assert.equal(to('invalid', 'upgrade'), 'welcome');
  assert.equal(to('rolled-back'), 'failed');
  assert.equal(to('rollback-incomplete'), 'failed');
  assert.equal(to('cancelled'), 'failed');
});

test('the option checks match the Blackbox limits and accept spaces and non-ASCII folders', () => {
  assert.deepEqual(logic.validateOptions(goodOptions()), []);
  const spaced = { ...goodOptions(), location: 'D:\\Mes outils\\Unravis été' };
  assert.deepEqual(logic.validateOptions(spaced), []);
  const edges = goodOptions();
  Object.assign(edges.blackbox, { name: 'a'.repeat(63), keepDays: 365, maxGB: 0.1, minFreePercent: 50, port: 65535 });
  assert.deepEqual(logic.validateOptions(edges), []);
  const bad = goodOptions();
  Object.assign(bad.blackbox, { name: '-web', folder: 'recordings', keepDays: 0, maxGB: 10001, minFreePercent: 51, port: 0 });
  assert.deepEqual(fields(logic.validateOptions(bad)), ['bb-name', 'bb-folder', 'bb-keep', 'bb-max', 'bb-free', 'bb-port']);
  assert.deepEqual(fields(logic.validateOptions({ ...goodOptions(), location: 'Unravis' })), ['location']);
  assert.deepEqual(fields(logic.validateOptions({ ...goodOptions(), location: 'C:\\Un|ravis' })), ['location']);
  const longName = goodOptions();
  longName.blackbox.name = 'a'.repeat(64);
  assert.deepEqual(fields(logic.validateOptions(longName)), ['bb-name']);
});

test('the firewall rule needs the Blackbox and at least one address', () => {
  assert.deepEqual(fields(logic.validateOptions({ ...goodOptions(), firewall: { remoteAddresses: [] } })), ['firewall']);
  assert.deepEqual(fields(logic.validateOptions({ ...goodOptions(), blackbox: undefined, firewall: { remoteAddresses: ['10.0.0.5'] } })), ['firewall']);
  assert.deepEqual(logic.parseAddresses(' 10.0.0.5, 10.0.1.0/24;\n10.0.2.1  '), ['10.0.0.5', '10.0.1.0/24', '10.0.2.1']);
  assert.deepEqual(logic.parseAddresses('  '), []);
});

test('Remove waits for each typed confirmation, which must match exactly', () => {
  assert.equal(logic.uninstallReady({ deleteCases: false, deleteRecordings: false }, 'web-01'), true);
  assert.equal(logic.uninstallReady({ deleteCases: true, confirmCases: 'delete' }, 'web-01'), false);
  assert.equal(logic.uninstallReady({ deleteCases: true, confirmCases: logic.CONFIRM_DELETE_WORD }, 'web-01'), true);
  assert.equal(logic.uninstallReady({ deleteRecordings: true, confirmRecordings: 'Web-01' }, 'web-01'), false);
  assert.equal(logic.uninstallReady({ deleteRecordings: true, confirmRecordings: 'web-01' }, 'web-01'), true);
  assert.equal(logic.uninstallReady({ deleteRecordings: true, confirmRecordings: '' }, ''), false);
  assert.equal(logic.CONFIRM_DELETE_WORD, 'DELETE');
});

test('the request carries a confirmation only for a ticked deletion, and the trimmed folder', () => {
  const options = { ...goodOptions(), location: '  C:\\Apps\\Unravis  ', closeRunningApp: false };
  const kept = logic.buildRequest('uninstall', options, { deleteCases: false, deleteRecordings: false, confirmCases: 'DELETE' });
  assert.deepEqual(kept.uninstall, { deleteCases: false, deleteRecordings: false, closeRunningApp: false });
  assert.equal(kept.options.location, 'C:\\Apps\\Unravis');
  const gone = logic.buildRequest('uninstall', options, { deleteCases: true, confirmCases: 'DELETE', deleteRecordings: true });
  assert.deepEqual(gone.uninstall, { deleteCases: true, deleteRecordings: true, closeRunningApp: false, confirmCases: 'DELETE', confirmRecordings: '' });
  assert.equal('readKeyFile' in gone.options, false, 'the window never asks for a read key file');
});

test('Close and Open Unravis wait for the read key to be saved', () => {
  assert.equal(logic.canFinish({ readKey: 'ubk-x' }, false), false);
  assert.equal(logic.canFinish({ readKey: 'ubk-x' }, true), true);
  assert.equal(logic.canFinish({}, false), true);
  assert.equal(logic.canFinish(null, false), true);
});

test('the approval screen names what needs the rights', () => {
  const o = goodOptions();
  assert.equal(logic.approvalReason('install', o), 'Placing the Blackbox');
  assert.equal(logic.approvalReason('install', { ...o, scope: 'machine' }), 'Installing for everyone on this PC and placing the Blackbox');
  assert.equal(logic.approvalReason('install', { scope: 'machine' }), 'Installing for everyone on this PC');
  assert.equal(logic.approvalReason('uninstall', o, 'machine'), 'Removing Unravis for everyone on this PC');
  assert.equal(logic.approvalReason('upgrade', o, 'machine'), 'Upgrading Unravis for everyone on this PC');
});

test('formatBytes reads like the wireframe', () => {
  assert.equal(logic.formatBytes(3_435_973_837), '3.2 GB');
  assert.equal(logic.formatBytes(1_181_116_006), '1.1 GB');
  assert.equal(logic.formatBytes(512), '512 bytes');
  assert.equal(logic.formatBytes(-1), '');
});

test('the setup page keeps the key out of the address bar and sends it as a header', () => {
  const api = read('setup/api.ts');
  assert.match(api, /history\.replaceState/);
  assert.match(api, /X-Setup-Key/);
  assert.match(api, /try\s*\{[^}]*sessionStorage/s, 'storage is wrapped in try');
  const html = read('setup/index.html');
  assert.doesNotMatch(html, /https?:\/\//, 'the setup page loads nothing from the network');
});

test('the folder and address checks refuse what setup would refuse, in plain words', () => {
  const loc = (location) => fields(logic.validateOptions({ ...goodOptions(), location }));
  assert.deepEqual(loc('C:\\'), ['location']);
  assert.deepEqual(loc('C:\\a\\..\\..\\b'), ['location']);
  assert.deepEqual(loc('C:\\CON'), ['location']);
  assert.deepEqual(loc('C:\\' + 'a'.repeat(250)), ['location']);
  assert.deepEqual(loc('C:\\Mes outils\\Unravis été'), []);
  const wall = (remoteAddresses) => fields(logic.validateOptions({ ...goodOptions(), firewall: { remoteAddresses } }));
  assert.deepEqual(wall(['10.0.0.5', '10.0.1.0/24', 'fe80::1', '2001:db8::/32']), []);
  for (const bad of ['abc', '999.1.1.1', '10.0.0.0/33', '*', 'any', '0.0.0.0', '10.0.0.0/0', '::', '10.0.0.1/24/1']) assert.deepEqual(wall([bad]), ['firewall'], bad);
  assert.deepEqual(wall(Array.from({ length: 51 }, (_, n) => '10.0.0.' + (n + 1))), ['firewall']);
});

test('failed checks come first, with one line that names them', () => {
  const check = (id, status) => ({ id, title: id.toUpperCase(), status, detail: '' });
  const checks = [check('a', 'ready'), check('b', 'failed'), check('c', 'partly'), check('d', 'failed')];
  assert.deepEqual(logic.checksFailedFirst(checks).map((c) => c.id), ['b', 'd', 'c', 'a']);
  assert.equal(logic.blockedLine([check('a', 'ready')]), '');
  assert.match(logic.blockedLine(checks), /^2 checks failed: B, D\./);
  assert.match(logic.blockedLine([check('b', 'failed')]), /^1 check failed: B\. Fix it/);
});

test('Try again is offered only when repeating the run can change the result', () => {
  assert.equal(logic.retryHelps({ outcome: 'rolled-back', fix: 'Free the port and try again.' }), true);
  assert.equal(logic.retryHelps({ outcome: 'rolled-back', fix: 'Pick another port in Options, then press Next.' }), false);
  assert.equal(logic.retryHelps({ outcome: 'rollback-incomplete', fix: '' }), false);
  assert.equal(logic.retryHelps(null), false);
});

test('the setup sticker inks are readable: 4.5:1 on their fill and 3:1 for the edge, in both themes', () => {
  const css = read('src/brand/tokens.css').replace(/\r\n/g, '\n');
  const blocks = { dark: css.slice(css.indexOf(':root {'), css.indexOf('@media (prefers-color-scheme: light)')), light: css.slice(css.indexOf(':root[data-theme="light"] {')) };
  const rgb = (hex) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
  const lum = (c) => c.map((v) => ((v /= 255) <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4)).reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
  const ratio = (a, b) => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
  const mix = (a, b, share) => a.map((v, i) => v * share + b[i] * (1 - share));
  for (const [theme, block] of Object.entries(blocks)) {
    const token = (name) => rgb(block.match(new RegExp(String.raw`${name}:\s*(#[0-9a-fA-F]{6})`))[1]);
    for (const tone of ['ok', 'warn', 'bad', 'run', 'pending', 'neutral']) {
      const ink = token('--setup-' + tone);
      for (const surround of ['--bg', '--surface', '--surface-2']) {
        const base = token(surround);
        const fill = tone === 'neutral' ? token('--surface-2') : mix(ink, token('--surface'), 0.12);
        assert.ok(ratio(ink, fill) >= 4.5, theme + ' ' + tone + ' text on ' + surround + ': ' + ratio(ink, fill).toFixed(2));
        assert.ok(ratio(mix(ink, base, 0.75), base) >= 3, theme + ' ' + tone + ' edge on ' + surround + ': ' + ratio(mix(ink, base, 0.75), base).toFixed(2));
      }
    }
  }
});
