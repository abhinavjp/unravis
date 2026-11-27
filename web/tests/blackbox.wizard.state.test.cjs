const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// TSK-01 (contracts C-1 and C-5): the wizard state module. Every rule of the Place a Blackbox pop-up that is not
// a pixel lives in blackboxWizard.ts, so these tests drive it the way the components will: one state in, one out.

const w = loadTypeScript('src/lib/blackboxWizard.ts');
const bb = loadTypeScript('src/lib/blackbox.ts');

const drive = (letter, type, system, extra = {}) =>
  ({ letter, label: 'Drive ' + letter, type, system, freeBytes: 100, totalBytes: 200, ...extra });
const FALLBACK = 'D:\\UnravisBlackbox';
// The Recommended and Light answers of the three collector rows (Server facts, Extra Windows counters, Log and config file paths).
const ROWS_ON = { facts: true, counters: false, paths: true };
const ROWS_OFF = { facts: false, counters: false, paths: false };

/** A wizard with a good name, the place chosen, and the folder already placed on a plain drive. */
function ready(where = 'pc', extra = {}) {
  let s = w.initialState();
  s = { ...s, settings: { ...s.settings, name: 'web-a' } };
  s = w.setWhere(s, where);
  return { ...s, ...extra };
}

/** Walks Next from the first step to the step asked for, failing the test when Next refuses on the way. */
function walkTo(s, id) {
  let cur = s;
  for (let i = 0; i < 10 && cur.step !== id; i++) {
    const moved = w.next(cur);
    assert.notEqual(moved, cur, 'next refused at ' + cur.step + ' (' + w.stepProblem(cur, cur.step) + ')');
    cur = moved;
  }
  assert.equal(cur.step, id);
  return cur;
}

/* ------------------------------------------------------------------ the step list */

test('the step list is 7 steps; the SQL step and the Redis step each add one (S-02, B-01)', () => {
  assert.deepEqual(w.steps({ where: null, sqlSeen: false, redisSeen: false }), ['name', 'where', 'record', 'watch', 'keep', 'folder', 'review']);
  assert.deepEqual(w.steps({ where: 'pc', sqlSeen: false, redisSeen: false }), ['name', 'where', 'record', 'watch', 'keep', 'folder', 'review']);
  assert.deepEqual(w.steps({ where: 'pc', sqlSeen: true, redisSeen: false }), ['name', 'where', 'record', 'sql', 'watch', 'keep', 'folder', 'review']);
  assert.deepEqual(w.steps({ where: 'pc', sqlSeen: false, redisSeen: true }), ['name', 'where', 'record', 'redis', 'watch', 'keep', 'folder', 'review']);
  assert.deepEqual(w.steps({ where: 'pack', sqlSeen: false, redisSeen: false }), ['name', 'where', 'record', 'sql', 'redis', 'watch', 'keep', 'folder', 'review']);
  assert.equal(w.steps({ where: 'pack', sqlSeen: true, redisSeen: true }).length, 9);
});

test('stepIndex counts from 1 in the current list, so the "Step X of N" line is honest', () => {
  const s7 = ready('pc');
  assert.equal(w.stepIndex({ ...s7, step: 'name' }), 1);
  assert.equal(w.stepIndex({ ...s7, step: 'watch' }), 4);
  assert.equal(w.stepIndex({ ...s7, step: 'review' }), 7);
  const s9 = ready('pack');
  assert.equal(w.stepIndex({ ...s9, step: 'watch' }), 6);
  assert.equal(w.stepIndex({ ...s9, step: 'review' }), 9);
});

test('the SQL step appears and goes away with the choices, and the rail position follows the step, not the number', () => {
  let s = ready('pc');
  s = walkTo(s, 'keep');
  assert.equal(s.maxSeen, 4); // name, where, record, watch, keep
  const withSql = w.setSqlSeen(s, true);
  assert.equal(withSql.step, 'keep');
  assert.equal(withSql.maxSeen, 5, 'keep is now one place further along');
  assert.equal(w.canVisit(withSql, 'sql'), true);
  const gone = w.setSqlSeen(withSql, false);
  assert.equal(gone.maxSeen, 4);
  assert.equal(gone.step, 'keep');
  // the current step itself disappearing lands on a neighbour
  const onSql = { ...withSql, step: 'sql' };
  assert.equal(w.setSqlSeen(onSql, false).step, 'watch');
});

/* ------------------------------------------------------------------ the rail */

test('the rail is locked beyond maxSeen and open up to it (S-04)', () => {
  let s = ready('pc');
  assert.equal(w.canVisit(s, 'name'), true);
  assert.equal(w.canVisit(s, 'where'), false);
  s = walkTo(s, 'record');
  assert.equal(s.maxSeen, 2);
  assert.equal(w.canVisit(s, 'name'), true);
  assert.equal(w.canVisit(s, 'record'), true);
  assert.equal(w.canVisit(s, 'watch'), false);
  assert.equal(w.canVisit(s, 'sql'), false, 'a step that is not in the list cannot be visited');
  assert.equal(w.goTo(s, 'review'), s, 'a locked step is refused with the same state');
  const back2 = w.goTo(s, 'name');
  assert.equal(back2.step, 'name');
  assert.equal(back2.maxSeen, 2, 'going back does not lock what was reached');
  assert.equal(w.canVisit(back2, 'record'), true);
});

test('goTo forward is refused while the step being left has a problem, and back is always free', () => {
  let s = walkTo(ready('pc'), 'record');
  s = w.goTo(s, 'name');
  s = { ...s, settings: { ...s.settings, name: 'bad name' } };
  assert.equal(w.goTo(s, 'record'), s, 'the name is wrong, so the rail does not carry the owner forward');
  const onRecord = { ...s, step: 'record', settings: { ...s.settings, intervalSeconds: 7 } };
  assert.equal(w.goTo(onRecord, 'name').step, 'name');
});

test('back moves one step and stops at the first', () => {
  const s = walkTo(ready('pc'), 'watch');
  const b = w.back(s);
  assert.equal(b.step, 'record');
  assert.equal(b.maxSeen, s.maxSeen);
  const first = w.back(w.back(w.back(b)));
  assert.equal(first.step, 'name');
  assert.equal(w.back(first), first);
});

/* ------------------------------------------------------------------ next and the step rules */

test('next refuses while the step has a problem and moves on once it is fixed', () => {
  const s0 = w.initialState();
  assert.equal(w.stepProblem(s0, 'name'), 'name-empty');
  assert.equal(w.next(s0), s0);
  const bad = { ...s0, settings: { ...s0.settings, name: 'has space' } };
  assert.equal(w.stepProblem(bad, 'name'), 'name-chars');
  const long = { ...s0, settings: { ...s0.settings, name: 'a'.repeat(64) } };
  assert.equal(w.stepProblem(long, 'name'), 'name-long');
  const good = { ...s0, settings: { ...s0.settings, name: 'web-a' } };
  assert.equal(w.stepProblem(good, 'name'), null);
  const moved = w.next(good);
  assert.equal(moved.step, 'where');
  assert.equal(moved.maxSeen, 1);
  // where must be answered
  assert.equal(w.stepProblem(moved, 'where'), 'where-missing');
  assert.equal(w.next(moved), moved);
  assert.equal(w.next(w.setWhere(moved, 'pc')).step, 'record');
});

test('record, keep and watch problems come from the same rules the server repeats', () => {
  const s = ready('pc');
  const set = (patch) => ({ ...s, settings: { ...s.settings, ...patch } });
  assert.equal(w.stepProblem(set({ intervalSeconds: 10 }), 'record'), 'interval');
  assert.equal(w.stepProblem(set({}), 'record'), null);
  assert.equal(w.stepProblem({ ...s, logPathsText: 'relative\\x' }, 'record'), 'logs');
  assert.equal(w.stepProblem({ ...s, logPathsText: 'C:\\logs\nD:\\more\n\n' }, 'record'), null, 'blank lines are dropped');
  const twentyOne = Array.from({ length: 21 }, (_, i) => 'C:\\l' + i).join('\n');
  assert.equal(w.stepProblem({ ...s, logPathsText: twentyOne }, 'record'), 'logs', 'more than 20 log folders');
  // the log box is hidden when the catalogue is off, so it cannot block
  const off = { ...s, logPathsText: 'relative\\x', settings: { ...s.settings, record: { ...s.settings.record, logsCatalogue: false } } };
  assert.equal(w.stepProblem(off, 'record'), null);

  assert.equal(w.stepProblem(set({ keepDays: 0 }), 'keep'), 'keep');
  assert.equal(w.stepProblem(set({ keepDays: 366 }), 'keep'), 'keep');
  assert.equal(w.stepProblem(set({ keepDays: 365 }), 'keep'), null);
  assert.equal(w.stepProblem(set({ maxGB: 0.05 }), 'keep'), 'size');
  assert.equal(w.stepProblem(set({ maxGB: 0.1 }), 'keep'), null);
  assert.equal(w.stepProblem(set({ minFreePercent: 51 }), 'keep'), 'free');

  const dup = { kind: 'service', name: 'EmailSvc' };
  const watch = (targets, watchMode = 'some') => ({ ...s, watchMode, settings: { ...s.settings, targets } });
  assert.equal(w.stepProblem(watch([dup, { kind: 'service', name: 'emailsvc' }]), 'watch'), 'targets-duplicate');
  assert.equal(w.stepProblem(watch([{ kind: 'service', name: 'a/b' }]), 'watch'), 'targets-chars');
  assert.equal(w.stepProblem(watch([{ kind: 'service', name: '' }]), 'watch'), 'targets-empty');
  assert.equal(w.stepProblem(watch([{ kind: 'queue', name: 'x' }]), 'watch'), 'targets-kind');
  assert.equal(w.stepProblem(watch(Array.from({ length: 33 }, (_, i) => ({ kind: 'service', name: 's' + i }))), 'watch'), 'targets-many');
  assert.equal(w.stepProblem(watch([dup]), 'watch'), null);
  assert.equal(w.stepProblem(watch([dup, dup], 'none'), 'watch'), null, 'watch nothing: the list is not used');
});

test('the folder step: unsafe and not-full paths, the C: tick, port and bind', () => {
  const s = ready('pc');
  const at = (folder, extra = {}) => ({ ...s, ...extra, settings: { ...s.settings, folder, ...(extra.settings ?? {}) } });
  assert.equal(w.stepProblem(at('D:\\UnravisBlackbox'), 'folder'), null);
  assert.equal(w.stepProblem(at('relative\\x'), 'folder'), 'folder-invalid');
  assert.equal(w.stepProblem(at(''), 'folder'), 'folder-invalid');
  assert.equal(w.stepProblem(at('D:\\'), 'folder'), 'folder-unsafe');
  assert.equal(w.stepProblem(at('D:\\Windows\\x'), 'folder'), 'folder-unsafe');
  assert.equal(w.stepProblem(at('\\\\srv\\share'), 'folder'), 'folder-unsafe', 'a share root');
  // Changed on purpose (BL-137): the recorder refuses a network share at install (newFolderProblem in
  // internal/blackbox/service_folder.go), so the wizard refuses it too instead of letting the install fail.
  assert.equal(w.stepProblem(at('\\\\srv\\share\\bb'), 'folder'), 'folder-not-local', 'a folder on a share');
  // the system drive needs the tick
  assert.equal(w.stepProblem(at('C:\\UnravisBlackbox'), 'folder'), 'folder-system-drive');
  // Changed on purpose (browser pass, defect E): forward slashes are refused now, with their own code, instead of being
  // accepted as a Windows drive path. The tick question therefore only comes up for a backslash path.
  assert.equal(w.stepProblem(at('c:/UnravisBlackbox'), 'folder'), 'folder-slash');
  assert.equal(w.stepProblem(at('c:\\UnravisBlackbox'), 'folder'), 'folder-system-drive');
  assert.equal(w.stepProblem(at('//srv/share/bb'), 'folder'), 'folder-slash');
  for (const ch of ['<', '>', '"', '|', '?', '*', ':']) {
    assert.equal(w.stepProblem(at('D:\\a' + ch + 'b'), 'folder'), 'folder-chars', ch);
  }
  assert.equal(w.stepProblem(at('C:\\UnravisBlackbox', { confirmSystemDrive: true }), 'folder'), null);
  // Windows on another drive: that one needs the tick, and C: does not
  assert.equal(w.stepProblem(at('E:\\UnravisBlackbox', { systemDrive: 'E' }), 'folder'), 'folder-system-drive');
  assert.equal(w.stepProblem(at('C:\\UnravisBlackbox', { systemDrive: 'E' }), 'folder'), null);
  assert.equal(w.stepProblem(at('D:\\bb', { settings: { port: 0 } }), 'folder'), 'port');
  assert.equal(w.stepProblem(at('D:\\bb', { settings: { bind: 'not an address!' } }), 'folder'), 'bind');
});

test('the folder step refuses a USB or network drive of this PC (BL-137), and only on this PC', () => {
  const s = ready('pc');
  const at = (folder, extra = {}) => ({ ...s, ...extra, settings: { ...s.settings, folder } });
  const known = { nonLocalDrives: ['E', 'Z'] };
  assert.equal(w.stepProblem(at('E:\\UnravisBlackbox', known), 'folder'), 'folder-not-local');
  assert.equal(w.stepProblem(at('z:\\bb', known), 'folder'), 'folder-not-local', 'case ignored');
  assert.equal(w.stepProblem(at('D:\\UnravisBlackbox', known), 'folder'), null);
  assert.equal(w.stepProblem(at('E:\\UnravisBlackbox'), 'folder'), null, 'no drives list: nothing known');
  // the pack path is another server: this PC's drive letters say nothing about it
  const pack = { ...ready('pack'), nonLocalDrives: ['E'], settings: { ...ready('pack').settings, folder: 'E:\\bb' } };
  assert.equal(w.stepProblem(pack, 'folder'), null);
  // but a network path is refused there too, the recorder checks it where it is installed
  assert.equal(w.stepProblem({ ...pack, settings: { ...pack.settings, folder: '\\\\srv\\share\\bb' } }, 'folder'), 'folder-not-local');
});

test('applyDefaultFolder records the USB and network drives of the list, even after the owner typed a folder', () => {
  const drives = [
    { letter: 'C', label: 'OS', type: 'fixed', system: true, freeBytes: 1, totalBytes: 2 },
    { letter: 'D', label: 'Data', type: 'fixed', system: false, freeBytes: 1, totalBytes: 2 },
    { letter: 'e', label: 'Stick', type: 'removable', system: false, freeBytes: 1, totalBytes: 2 },
    { letter: 'Z', label: 'Share', type: 'network', system: false, freeBytes: null, totalBytes: null }
  ];
  const s = ready('pc');
  const fresh = w.applyDefaultFolder(s, drives, 'D:\\UnravisBlackbox');
  assert.deepEqual(fresh.nonLocalDrives, ['E', 'Z']);
  assert.equal(fresh.settings.folder, 'D:\\UnravisBlackbox', 'the default never lands on USB or network');
  const typed = w.applyDefaultFolder(w.setFolder(s, 'E:\\bb'), drives, 'D:\\UnravisBlackbox');
  assert.deepEqual(typed.nonLocalDrives, ['E', 'Z']);
  assert.equal(typed.settings.folder, 'E:\\bb', 'a typed folder is kept');
  assert.equal(w.stepProblem(typed, 'folder'), 'folder-not-local');
  assert.deepEqual(w.applyDefaultFolder(s, null, 'D:\\UnravisBlackbox').nonLocalDrives, undefined, 'no list: nothing known');
});

test('next on a step with no choice (SQL) always moves on, and Review is the last step', () => {
  const s = { ...walkTo(ready('pack'), 'sql') };
  assert.equal(w.stepProblem(s, 'sql'), null);
  assert.equal(w.next(s).step, 'redis');
  assert.equal(w.stepProblem({ ...s, step: 'redis' }, 'redis'), null);
  assert.equal(w.next({ ...s, step: 'redis' }).step, 'watch');
  const last = { ...s, step: 'review' };
  assert.equal(w.next(last), last);
});

/* ------------------------------------------------------------------ returnTo */

test('a Review "Change" link comes straight back to Review after Next (S-05)', () => {
  let s = ready('pc');
  s = w.useDefaultsToReview(s);
  assert.equal(s.step, 'review');
  const changing = w.changeStep(s, 'keep');
  assert.equal(changing.step, 'keep');
  assert.equal(changing.returnTo, 'review');
  const done = w.next(changing);
  assert.equal(done.step, 'review');
  assert.equal(done.returnTo, null, 'the detour is over');
  // a problem still stops the detour
  const broken = { ...changing, settings: { ...changing.settings, keepDays: 0 } };
  assert.equal(w.next(broken), broken);
  // Review itself cannot be "changed" and locked steps cannot be reached
  assert.equal(w.changeStep(s, 'review'), s);
  assert.equal(w.changeStep(ready('pc'), 'folder').step, 'name');
  // going back or using the rail ends the detour
  assert.equal(w.back(changing).returnTo, null);
  assert.equal(w.goTo(changing, 'name').returnTo, null);
});

/* ------------------------------------------------------------------ presets */

test('presets are worked out from the three record switches (B-03)', () => {
  assert.equal(w.presetOf({ processes: true, logsCatalogue: true, copyLogsOnSnapshot: false }, ROWS_ON), 'recommended');
  assert.equal(w.presetOf({ processes: false, logsCatalogue: false, copyLogsOnSnapshot: false }, ROWS_OFF), 'light');
  assert.equal(w.presetOf({ processes: true, logsCatalogue: false, copyLogsOnSnapshot: false }, ROWS_ON), 'custom');
  assert.equal(w.presetOf({ processes: false, logsCatalogue: true, copyLogsOnSnapshot: false }, ROWS_ON), 'custom');
  assert.equal(w.presetOf({ processes: true, logsCatalogue: true, copyLogsOnSnapshot: true }, ROWS_ON), 'custom');
  assert.equal(w.presetOf({ processes: false, logsCatalogue: false, copyLogsOnSnapshot: true }, ROWS_ON), 'custom');
  // the recommended preset is what the recorder ships with
  assert.equal(w.presetOf(bb.defaultSettings().record, w.initialState().collectorRows), 'recommended');
});

test('applyPreset sets the switches, and changing any one switch afterwards reads as Custom (S-07)', () => {
  const s = ready('pc');
  const light = w.applyPreset(s, 'light');
  assert.deepEqual(light.settings.record, { processes: false, logsCatalogue: false, copyLogsOnSnapshot: false });
  assert.equal(w.presetOf(light.settings.record, light.collectorRows), 'light');
  const rec = w.applyPreset(light, 'recommended');
  assert.deepEqual(rec.settings.record, { processes: true, logsCatalogue: true, copyLogsOnSnapshot: false });
  assert.equal(w.presetOf(rec.settings.record, rec.collectorRows), 'recommended');
  for (const key of ['processes', 'logsCatalogue', 'copyLogsOnSnapshot']) {
    for (const base of [rec, light]) {
      const changed = { ...base.settings.record, [key]: !base.settings.record[key] };
      assert.equal(w.presetOf(changed, base.collectorRows), 'custom', key + ' flipped from ' + w.presetOf(base.settings.record, base.collectorRows));
    }
  }
  // the original state is untouched
  assert.equal(w.presetOf(s.settings.record, s.collectorRows), 'recommended');
  assert.equal(w.applyPreset(s, 'light') !== s, true);
});

/* ------------------------------------------------------------------ use defaults */

test('"Use defaults, go to review" fills the record, SQL and watch answers and lands on Review with every step reached', () => {
  let s = ready('pack');
  s = {
    ...s,
    watchMode: 'some',
    logPathsText: 'E:\\other',
    settings: { ...s.settings, intervalSeconds: 60, record: { processes: false, logsCatalogue: false, copyLogsOnSnapshot: true }, targets: [{ kind: 'service', name: 'x' }], keepDays: 30 }
  };
  const r = w.useDefaultsToReview(s);
  assert.equal(r.step, 'review');
  assert.equal(r.returnTo, null);
  assert.equal(r.maxSeen, 8, 'nine steps on the pack path: all reached');
  assert.equal(w.presetOf(r.settings.record, r.collectorRows), 'recommended');
  assert.equal(r.settings.intervalSeconds, 15);
  assert.deepEqual(r.settings.logPaths, ['C:\\inetpub\\logs\\LogFiles']);
  assert.equal(r.logPathsText, 'C:\\inetpub\\logs\\LogFiles');
  assert.equal(r.watchMode, 'none');
  assert.deepEqual(r.settings.targets, []);
  assert.equal(r.settings.keepDays, 30, 'the keep answers are not part of the shortcut');
  assert.equal(r.settings.name, 'web-a');
  for (const id of w.steps(r)) assert.equal(w.canVisit(r, id), true, id);
  assert.equal(s.step, 'name', 'the state given is not changed');
});

test('"Use defaults" from the Keep step (resetKeep) also puts back the days, size and free space (defect I)', () => {
  const s = ready('pc');
  const changed = { ...s, step: 'keep', settings: { ...s.settings, keepDays: 30, maxGB: 5, minFreePercent: 20 } };
  const d = bb.defaultSettings('web-a');
  const r = w.useDefaultsToReview(changed, true);
  assert.equal(r.step, 'review');
  assert.equal(r.settings.keepDays, d.keepDays);
  assert.equal(r.settings.maxGB, d.maxGB);
  assert.equal(r.settings.minFreePercent, d.minFreePercent);
  assert.equal(w.useDefaultsToReview(changed).settings.keepDays, 30, 'without the flag the keep answers stay');
});

test('whereChoices says which cards work, why none does, and when the scan failed (defect F)', () => {
  const pc = { available: true, scanned: true, installed: false };
  assert.deepEqual(w.whereChoices(pc, true), { pc: true, pack: true, none: null, scanFailed: false });
  assert.equal(w.whereChoices({ ...pc, scanned: false }, true).scanFailed, true, 'asked but no answer: say so');
  assert.equal(w.whereChoices({ ...pc, scanned: false }, true).pc, true, 'and the owner can still choose');
  assert.equal(w.whereChoices({ available: false, scanned: false, installed: false }, false).none, 'helper');
  assert.equal(w.whereChoices({ available: false, scanned: false, installed: false }, false).scanFailed, false, 'no helper: the helper line says it');
  assert.equal(w.whereChoices({ ...pc, installed: true }, false).none, 'other');
  assert.equal(w.whereChoices({ ...pc, installed: true }, true).none, null, 'a pack can still be built');
});

test('"Use defaults" is refused until the name and the place are answered', () => {
  const s0 = w.initialState();
  assert.equal(w.useDefaultsToReview(s0), s0);
  const named = { ...s0, settings: { ...s0.settings, name: 'web-a' } };
  assert.equal(w.useDefaultsToReview(named), named, 'no place yet');
  assert.equal(w.useDefaultsToReview(w.setWhere(named, 'pc')).step, 'review');
});

test('Review reports the first problem left in the shortcut, such as the C: tick', () => {
  let s = ready('pc');
  s = { ...s, settings: { ...s.settings, folder: 'C:\\UnravisBlackbox' } };
  const r = w.useDefaultsToReview(s);
  assert.equal(r.step, 'review');
  assert.equal(w.stepProblem(r, 'review'), 'folder-system-drive');
  assert.equal(w.stepProblem({ ...r, confirmSystemDrive: true }, 'review'), null);
});

/* ------------------------------------------------------------------ default folder */

test('default folder with a D: drive is D:\\UnravisBlackbox with no tick (S-11)', () => {
  const got = w.defaultFolder([drive('C', 'fixed', true), drive('D', 'fixed', false)], FALLBACK);
  assert.deepEqual(got, { folder: 'D:\\UnravisBlackbox', needsSystemTick: false });
});

test('default folder takes the first plain fixed drive in letter order, ignoring USB and network drives', () => {
  const got = w.defaultFolder([drive('Z', 'network', false), drive('F', 'removable', false), drive('E', 'fixed', false), drive('C', 'fixed', true), drive('D', 'fixed', false)], FALLBACK);
  assert.deepEqual(got, { folder: 'D:\\UnravisBlackbox', needsSystemTick: false });
  const lower = w.defaultFolder([drive('d', 'fixed', false)], FALLBACK);
  assert.equal(lower.folder, 'D:\\UnravisBlackbox', 'the letter is upper case');
});

test('default folder without a D: drive uses the next plain fixed drive', () => {
  const got = w.defaultFolder([drive('C', 'fixed', true), drive('E', 'fixed', false)], FALLBACK);
  assert.deepEqual(got, { folder: 'E:\\UnravisBlackbox', needsSystemTick: false });
});

test('default folder with only C: is on the system drive and needs the tick', () => {
  const got = w.defaultFolder([drive('C', 'fixed', true)], FALLBACK);
  assert.deepEqual(got, { folder: 'C:\\UnravisBlackbox', needsSystemTick: true });
  // USB and network drives are never the default, even when they are all there is beside C:
  const withUsb = w.defaultFolder([drive('C', 'fixed', true), drive('F', 'removable', false), drive('Z', 'network', false)], FALLBACK);
  assert.deepEqual(withUsb, { folder: 'C:\\UnravisBlackbox', needsSystemTick: true });
  // Windows on another letter
  const onE = w.defaultFolder([drive('E', 'fixed', true)], FALLBACK);
  assert.deepEqual(onE, { folder: 'E:\\UnravisBlackbox', needsSystemTick: true });
});

test('default folder with no drives list (pack path, or the route failed) is the fallback, ticked only when it is on C:', () => {
  assert.deepEqual(w.defaultFolder(null, FALLBACK), { folder: 'D:\\UnravisBlackbox', needsSystemTick: false });
  assert.deepEqual(w.defaultFolder(null, 'C:\\UnravisBlackbox'), { folder: 'C:\\UnravisBlackbox', needsSystemTick: true });
  assert.deepEqual(w.defaultFolder([], FALLBACK), { folder: 'D:\\UnravisBlackbox', needsSystemTick: false });
  // a network fallback has no drive letter and never needs the tick
  assert.deepEqual(w.defaultFolder(null, '\\\\srv\\share\\bb'), { folder: '\\\\srv\\share\\bb', needsSystemTick: false });
  // only USB listed: nothing safe to pick, the fallback stands
  assert.deepEqual(w.defaultFolder([drive('F', 'removable', false)], FALLBACK), { folder: 'D:\\UnravisBlackbox', needsSystemTick: false });
});

test('driveOfPath gives the drive letter, upper case, and null for a network path or text that is not a path', () => {
  assert.equal(w.driveOfPath('D:\\x'), 'D');
  assert.equal(w.driveOfPath('e:/data'), 'E');
  assert.equal(w.driveOfPath(' c:\\x'), 'C');
  assert.equal(w.driveOfPath('D:'), 'D');
  assert.equal(w.driveOfPath('\\\\srv\\share\\x'), null);
  assert.equal(w.driveOfPath('//srv/share/x'), null);
  assert.equal(w.driveOfPath('relative\\x'), null);
  assert.equal(w.driveOfPath(''), null);
});

test('applyDefaultFolder places the default until the owner types, then leaves the folder alone', () => {
  const drives = [drive('C', 'fixed', true), drive('E', 'fixed', false)];
  let s = ready('pc');
  s = w.applyDefaultFolder(s, drives, FALLBACK);
  assert.equal(s.settings.folder, 'E:\\UnravisBlackbox');
  assert.equal(s.systemDrive, 'C');
  assert.equal(s.folderTouched, false);
  const typed = w.setFolder(s, 'F:\\mine');
  assert.equal(typed.folderTouched, true);
  const again = w.applyDefaultFolder(typed, [drive('C', 'fixed', true), drive('D', 'fixed', false)], FALLBACK);
  assert.equal(again.settings.folder, 'F:\\mine', 'what the owner typed is never replaced');
  const reset = w.resetFolder(again, [drive('C', 'fixed', true), drive('D', 'fixed', false)], FALLBACK);
  assert.equal(reset.settings.folder, 'D:\\UnravisBlackbox');
  assert.equal(reset.folderTouched, false);
  // only C: listed: the tick is asked for
  const onlyC = w.applyDefaultFolder(ready('pc'), [drive('C', 'fixed', true)], FALLBACK);
  assert.equal(onlyC.settings.folder, 'C:\\UnravisBlackbox');
  assert.equal(onlyC.confirmSystemDrive, false);
  assert.equal(w.stepProblem(onlyC, 'folder'), 'folder-system-drive');
});

test('setFolder clears the C: tick when the path moves to another drive, and keeps it on the same drive', () => {
  let s = { ...ready('pc'), settings: { ...ready('pc').settings, folder: 'C:\\a' }, confirmSystemDrive: true };
  assert.equal(w.setFolder(s, 'C:\\b').confirmSystemDrive, true);
  assert.equal(w.setFolder(s, 'D:\\b').confirmSystemDrive, false);
  assert.equal(w.setFolder(s, '\\\\srv\\share\\b').confirmSystemDrive, false);
});

/* ------------------------------------------------------------------ the payload */

test('buildSettings has exactly the keys of defaultSettings(), probe is { enabled: false } and nothing else slips in (C-5)', () => {
  const keysOf = (o) => Object.keys(o).sort();
  const base = bb.defaultSettings('web-a');
  const s = ready('pc');
  const out = w.buildSettings(s);
  assert.deepEqual(keysOf(out), keysOf(base));
  assert.deepEqual(keysOf(out.record), keysOf(base.record));
  assert.deepEqual(out.probe, { enabled: false });
  assert.deepEqual(keysOf(out.probe), ['enabled']);
  assert.deepEqual(out, base, 'untouched answers give the recorder defaults');

  // junk smuggled into the state is not copied out
  const dirty = {
    ...s,
    settings: { ...s.settings, extra: 1, probe: { enabled: true, serverCertificate: 'x' }, record: { ...s.settings.record, junk: true }, targets: [{ kind: 'service', name: 'EmailSvc', path: 'C:\\x', pid: 4 }] },
    watchMode: 'some'
  };
  const dirtyOut = w.buildSettings(dirty);
  assert.deepEqual(keysOf(dirtyOut), keysOf(base));
  assert.deepEqual(keysOf(dirtyOut.record), keysOf(base.record));
  assert.deepEqual(dirtyOut.probe, { enabled: false });
  assert.deepEqual(dirtyOut.targets, [{ kind: 'service', name: 'EmailSvc' }]);
  assert.deepEqual(keysOf(dirtyOut.targets[0]), ['kind', 'name']);
});

test('buildSettings carries each step\'s answers into the right field', () => {
  let s = ready('pc');
  s = {
    ...s,
    watchMode: 'some',
    logPathsText: ' C:\\logs\\a \r\n\nD:\\logs\\b\n',
    settings: {
      ...s.settings, name: 'iis-prod', intervalSeconds: 30, keepDays: 30, maxGB: 0.5, minFreePercent: 15,
      folder: ' E:\\UnravisBlackbox ', port: 7799, bind: ' 0.0.0.0 ',
      record: { processes: false, logsCatalogue: true, copyLogsOnSnapshot: true },
      targets: [{ kind: 'appPool', name: 'DefaultAppPool' }, { kind: 'service', name: 'W3SVC' }]
    }
  };
  const out = w.buildSettings(s);
  assert.equal(out.name, 'iis-prod');
  assert.equal(out.intervalSeconds, 30);
  assert.deepEqual(out.record, { processes: false, logsCatalogue: true, copyLogsOnSnapshot: true });
  assert.deepEqual(out.logPaths, ['C:\\logs\\a', 'D:\\logs\\b']);
  assert.deepEqual(out.targets, [{ kind: 'appPool', name: 'DefaultAppPool' }, { kind: 'service', name: 'W3SVC' }]);
  assert.equal(out.keepDays, 30);
  assert.equal(out.maxGB, 0.5);
  assert.equal(out.minFreePercent, 15);
  assert.equal(out.folder, 'E:\\UnravisBlackbox', 'surrounding spaces are cut');
  assert.equal(out.port, 7799);
  assert.equal(out.bind, '0.0.0.0');
  assert.equal(bb.settingsProblem(out), null);
});

test('buildSettings sends no targets when the owner chose to watch nothing, even if some were ticked earlier', () => {
  const s = ready('pc');
  const ticked = { ...s, watchMode: 'none', settings: { ...s.settings, targets: [{ kind: 'service', name: 'x' }] } };
  assert.deepEqual(w.buildSettings(ticked).targets, []);
  assert.deepEqual(w.buildSettings({ ...ticked, watchMode: 'some' }).targets, [{ kind: 'service', name: 'x' }]);
});

test('buildSettings with the logs catalogue off keeps the default log folder when the hidden box holds rubbish', () => {
  const s = ready('pc');
  const off = { ...s, logPathsText: 'not a path', settings: { ...s.settings, record: { processes: true, logsCatalogue: false, copyLogsOnSnapshot: false } } };
  assert.deepEqual(w.buildSettings(off).logPaths, ['C:\\inetpub\\logs\\LogFiles']);
  assert.equal(bb.settingsProblem(w.buildSettings(off)), null);
});

test('buildSettings does not change the state and shares no object with it', () => {
  const s = { ...ready('pc'), watchMode: 'some' };
  s.settings.targets = [{ kind: 'service', name: 'x' }];
  const before = JSON.stringify(s);
  const out = w.buildSettings(s);
  assert.equal(JSON.stringify(s), before);
  out.record.processes = false;
  out.targets[0].name = 'changed';
  out.targets.push({ kind: 'service', name: 'y' });
  assert.equal(JSON.stringify(s), before);
});

test('every payload the wizard can reach by walking Next passes settingsProblem (the server check) on both paths', () => {
  for (const where of ['pc', 'pack']) {
    let s = w.applyDefaultFolder(ready(where), where === 'pc' ? [drive('C', 'fixed', true), drive('D', 'fixed', false)] : null, FALLBACK);
    s = walkTo(s, 'review');
    assert.equal(w.stepProblem(s, 'review'), null, where);
    const out = w.buildSettings(s);
    assert.equal(bb.settingsProblem(out), null, where);
    assert.deepEqual(out.probe, { enabled: false });
    assert.equal(out.folder, 'D:\\UnravisBlackbox');
  }
  // a busy path: Light preset, some targets, a network folder, other keep numbers
  let s = ready('pc');
  s = w.applyPreset(s, 'light');
  s = { ...s, watchMode: 'some', folderTouched: true, settings: { ...s.settings, targets: [{ kind: 'appPool', name: 'Pool1' }], folder: 'E:\\Recordings\\bb', keepDays: 90, maxGB: 12.5, minFreePercent: 20 } };
  s = walkTo(s, 'review');
  const out = w.buildSettings(s);
  assert.equal(bb.settingsProblem(out), null);
  assert.deepEqual(out.probe, { enabled: false });
});

test('the review step reports a problem when the payload would not pass, so Place stays off', () => {
  const s = walkTo(ready('pc'), 'review');
  assert.equal(w.stepProblem(s, 'review'), null);
  assert.equal(w.stepProblem({ ...s, settings: { ...s.settings, name: '' } }, 'review'), 'name-empty');
  assert.equal(w.stepProblem({ ...s, where: null }, 'review'), 'where-missing');
  assert.equal(w.stepProblem({ ...s, settings: { ...s.settings, port: 70000 } }, 'review'), 'port');
});

test('initialState starts from the recorder defaults on the first step', () => {
  const s = w.initialState();
  assert.equal(s.step, 'name');
  assert.equal(s.maxSeen, 0);
  assert.equal(s.returnTo, null);
  assert.equal(s.where, null);
  assert.equal(s.watchMode, 'none');
  assert.equal(s.folderTouched, false);
  assert.equal(s.confirmSystemDrive, false);
  assert.equal(s.sqlSeen, false);
  assert.equal(s.logPathsText, 'C:\\inetpub\\logs\\LogFiles');
  assert.deepEqual(s.settings, bb.defaultSettings());
  assert.deepEqual(s.collectorRows, { facts: true, counters: false, paths: true });
  assert.equal(s.countersText, '\\Process(w3wp)\\Page Faults/sec\n\\Memory\\Pool Nonpaged Bytes');
  // two fresh states share nothing
  s.settings.record.processes = false;
  assert.equal(w.initialState().settings.record.processes, true);
});

/* ------------------------------------------------------------------ review fixes: duplicates, Where change, rail */

test('a log folder typed twice is kept once, case-insensitively, with the first spelling and order', () => {
  const s = { ...ready('pc'), logPathsText: 'C:\\a\nC:\\A\nC:\\b\n  c:\\B  \n\n' };
  assert.deepEqual(w.buildSettings(s).logPaths, ['C:\\a', 'C:\\b']);
  assert.equal(w.stepProblem(s, 'record'), null);
  assert.equal(w.buildSettings({ ...s, logPathsText: 'C:\\a\nC:\\A\nC:\\b' }).logPaths.length, 2);
});

test('changing Where from Review ends the detour, so Next walks through Folder again and the C: tick is cleared', () => {
  let s = walkTo(ready('pc'), 'review');
  s = { ...s, confirmSystemDrive: true };
  s = w.changeStep(s, 'where');
  assert.equal(s.returnTo, 'review');
  s = w.setWhere(s, 'pack');
  assert.equal(s.returnTo, null);
  assert.equal(s.confirmSystemDrive, false);
  const moved = w.next(s);
  assert.notEqual(moved.step, 'review');
  // the untouched folder is still the automatic default, so Review stays locked until Folder was visited again
  assert.equal(w.canVisit(s, 'review'), false);
  assert.equal(w.canVisit(s, 'folder'), true);
  assert.equal(w.goTo(s, 'review').step, 'where');
  assert.equal(walkTo(s, 'review').step, 'review', 'walking Next reaches Review through Folder');
});

test('choosing the same Where again changes nothing; a touched folder keeps Review reachable', () => {
  const base = walkTo(ready('pc'), 'review');
  const detour = w.changeStep(base, 'where');
  const same = w.setWhere(detour, 'pc');
  assert.equal(same.returnTo, 'review');
  assert.equal(same.maxSeen, detour.maxSeen);
  const touched = w.setWhere({ ...detour, folderTouched: true, confirmSystemDrive: true }, 'pack');
  assert.equal(touched.returnTo, null);
  assert.equal(touched.confirmSystemDrive, false);
  assert.equal(w.canVisit(touched, 'review'), true);
});

test('when the step list shrinks, a step the owner never visited does not unlock', () => {
  let s = walkTo(ready('pack'), 'sql');
  assert.equal(s.maxSeen, 3);
  s = w.goTo(s, 'where');
  s = w.setWhere(s, 'pc');
  assert.deepEqual(w.steps(s), ['name', 'where', 'record', 'watch', 'keep', 'folder', 'review']);
  assert.equal(w.canVisit(s, 'record'), true);
  assert.equal(w.canVisit(s, 'watch'), false, 'watch was never visited');
  assert.equal(s.maxSeen, 2);
  // a step further on that was visited keeps its place
  let t = walkTo(ready('pack'), 'keep');
  t = w.goTo(t, 'where');
  t = w.setWhere(t, 'pc');
  assert.equal(t.maxSeen, w.steps(t).indexOf('keep'));
});

test('nonLocalLetters: every drive that is not a local hard drive is refused (USB, network, CD or RAM disk), the Windows drive is left to its own guard', () => {
  const list = [drive('C', 'fixed', true), drive('D', 'fixed', false), drive('E', 'removable', false), drive('F', 'other', false), drive('Z', 'network', false)];
  assert.deepEqual(w.nonLocalLetters(list), ['E', 'F', 'Z']);
});

/* ------------------------------------------------------------------ SQL and Redis choices */

test('a plain install writes no sql or redis block; only a real choice is written', () => {
  const s = ready('pack');
  const plain = w.buildSettings(s);
  assert.equal('sql' in plain, false);
  assert.equal('redis' in plain, false);
  const deep = w.setSqlText(w.setSqlProfile(s, 'deep'), true);
  assert.deepEqual(w.buildSettings(deep).sql, { profile: 'deep', queryText: true, queryTextMaxChars: 1000 });
  const every = w.setSqlProfile(s, 'everything');
  assert.deepEqual(w.buildSettings(every).sql, { profile: 'everything' });
});

test('query text exists only in the deep profile: leaving deep drops the tick, and the tick is refused elsewhere', () => {
  const s = ready('pack');
  assert.equal(w.setSqlText(s, true), s, 'refused on standard');
  const deep = w.setSqlText(w.setSqlProfile(s, 'deep'), true);
  assert.equal(deep.sqlChoice.text, true);
  const back = w.setSqlProfile(deep, 'everything');
  assert.equal(back.sqlChoice.text, false);
  assert.equal('queryText' in (w.buildSettings(back).sql ?? {}), false);
});

test('the SQL and Redis blocks are left out when their step does not apply on this PC', () => {
  let s = ready('pc');
  s = w.setSqlProfile(s, 'deep');
  s = w.setRedisProfile(s, 'everything');
  const out = w.buildSettings(s);
  assert.equal('sql' in out, false);
  assert.equal('redis' in out, false);
  s = w.setRedisSeen(s, true);
  assert.deepEqual(w.buildSettings(s).redis, { profile: 'everything' });
});

test('Redis rows follow the starting point and keep only real differences as overrides', () => {
  let s = ready('pack');
  assert.equal(w.redisRowOn(s.redisChoice, 'slowlog'), true);
  assert.equal(w.redisRowOn(s.redisChoice, 'latency'), false);
  assert.equal(w.redisPresetNow(s.redisChoice), 'standard');
  s = w.setRedisRow(s, 'latency', true);
  assert.equal(w.redisPresetNow(s.redisChoice), 'custom');
  assert.deepEqual(w.buildSettings(s).redis, { latency: true });
  s = w.setRedisRow(s, 'latency', false);
  assert.equal(w.redisPresetNow(s.redisChoice), 'standard', 'back to the starting point: no override left');
  assert.equal('redis' in w.buildSettings(s), false);
  s = w.setRedisProfile(w.setRedisRow(s, 'clients', true), 'everything');
  assert.deepEqual(s.redisChoice.rows, {}, 'a new starting point clears the overrides');
  assert.equal(w.redisRowOn(s.redisChoice, 'clients'), true);
  s = w.setRedisRow(s, 'slowlog', false);
  assert.deepEqual(w.buildSettings(s).redis, { profile: 'everything', slowlog: false });
  s = w.setRedisRow(w.setRedisProfile(s, 'standard'), 'slowArgs', true);
  assert.deepEqual(w.buildSettings(s).redis, { slowArgs: true });
});

test('"Use defaults, go to review" puts the SQL and Redis choices back to Standard', () => {
  let s = ready('pack');
  s = w.setRedisProfile(w.setSqlProfile(s, 'deep'), 'everything');
  const r = w.useDefaultsToReview(s);
  assert.deepEqual(r.sqlChoice, { profile: 'standard', text: false });
  assert.deepEqual(r.redisChoice, { profile: 'standard', rows: {} });
});

/* ------------------------------------------------------------------ collector rows (BL-698 / B9) */

const withRows = (s, rows, countersText) => ({ ...s, collectorRows: { ...s.collectorRows, ...rows }, ...(countersText === undefined ? {} : { countersText }) });

test('presets also depend on the three collector rows; anything else reads as custom', () => {
  const rec = { processes: true, logsCatalogue: true, copyLogsOnSnapshot: false };
  const light = { processes: false, logsCatalogue: false, copyLogsOnSnapshot: false };
  assert.equal(w.presetOf(rec, ROWS_ON), 'recommended');
  assert.equal(w.presetOf(light, ROWS_OFF), 'light');
  for (const key of ['facts', 'counters', 'paths']) {
    assert.equal(w.presetOf(rec, { ...ROWS_ON, [key]: !ROWS_ON[key] }), 'custom', 'recommended with ' + key + ' flipped');
    assert.equal(w.presetOf(light, { ...ROWS_OFF, [key]: true }), 'custom', 'light with ' + key + ' on');
  }
  // A host placed with Light before this change has no collectors block, so facts and paths are really on there.
  assert.equal(w.presetOf(light, ROWS_ON), 'custom');
  assert.equal(w.presetOf(rec, ROWS_OFF), 'custom');
});

test('applyPreset sets the collector rows: Recommended facts and paths on, counters off; Light all off', () => {
  const s = withRows(ready('pc'), { counters: true });
  assert.deepEqual(w.applyPreset(s, 'recommended').collectorRows, ROWS_ON);
  assert.deepEqual(w.applyPreset(s, 'light').collectorRows, ROWS_OFF);
  assert.equal(w.presetOf(w.applyPreset(s, 'light').settings.record, w.applyPreset(s, 'light').collectorRows), 'light');
  assert.deepEqual(s.collectorRows, { facts: true, counters: true, paths: true }, 'the state given is not changed');
});

test('buildSettings: Recommended writes no collectors and no counters key', () => {
  const out = w.buildSettings(ready('pc'));
  assert.equal('collectors' in out, false);
  assert.equal('counters' in out, false);
});

test('buildSettings writes only what is switched off, and never everySec', () => {
  const s = ready('pc');
  assert.deepEqual(w.buildSettings(withRows(s, { facts: false })).collectors, { 'host.facts': { on: false } });
  assert.deepEqual(w.buildSettings(withRows(s, { paths: false })).collectors, { 'paths.files': { on: false } });
  assert.deepEqual(w.buildSettings(withRows(s, { facts: false, paths: false })).collectors, { 'host.facts': { on: false }, 'paths.files': { on: false } });
  for (const rows of [{ facts: false }, { paths: false }, { facts: false, paths: false }]) {
    const out = w.buildSettings(withRows(s, rows));
    assert.equal('counters' in out, false);
    assert.equal(JSON.stringify(out).includes('everySec'), false);
  }
  const light = w.buildSettings(w.applyPreset(s, 'light'));
  assert.equal(JSON.stringify(light.collectors), '{"host.facts":{"on":false},"paths.files":{"on":false}}');
  assert.equal('counters' in light, false);
});

test('buildSettings: the counters row on writes the lines; off writes nothing even with text left over', () => {
  const s = ready('pc');
  const on = w.buildSettings(withRows(s, { counters: true }, '  \\Process(w3wp)\\Page Faults/sec \r\n\n\\Memory\\Pool Nonpaged Bytes\n'));
  assert.deepEqual(on.counters, { extra: ['\\Process(w3wp)\\Page Faults/sec', '\\Memory\\Pool Nonpaged Bytes'] });
  assert.equal('collectors' in on, false, 'facts and paths stay on, so nothing is written for them');
  assert.equal('counters' in w.buildSettings(s), false, 'prefill text is there but the row is off');
  assert.equal('counters' in w.buildSettings(withRows(s, { counters: false }, '\\A\\b')), false);
  // exact JSON for one combination
  const both = w.buildSettings(withRows(s, { facts: false, counters: true }, '\\Process(w3wp)\\Page Faults/sec'));
  assert.equal(JSON.stringify({ collectors: both.collectors, counters: both.counters }), '{"collectors":{"host.facts":{"on":false}},"counters":{"extra":["\\\\Process(w3wp)\\\\Page Faults/sec"]}}');
});

test('the counters problem: empty, bad shape, over 50, over 200 characters, a repeat in another case', () => {
  const s = withRows(ready('pc'), { counters: true });
  const problem = (text) => w.stepProblem({ ...s, countersText: text }, 'record');
  assert.equal(problem(w.initialState().countersText), null, 'the prefill is valid');
  assert.equal(problem(''), 'counters');
  assert.equal(problem(' \n\n  '), 'counters', 'blank lines only');
  assert.equal(problem('Process(w3wp)\\Page Faults/sec'), 'counters', 'no leading backslash');
  assert.equal(problem('\\Process(w3wp)'), 'counters', 'no counter name');
  assert.equal(problem('\\Process(*)\\Page Faults/sec'), 'counters', 'wildcard instance');
  assert.equal(problem('\\*\\Page Faults/sec'), 'counters', 'wildcard object');
  assert.equal(problem('\\\\server\\Process(w3wp)\\Page Faults/sec'), 'counters', 'computer name');
  assert.equal(problem('\\Process(w3wp)\\Page Faults/sec\nnot a counter'), 'counters', 'one bad line is enough');
  assert.equal(problem('\\Process(w3wp)\\Page Faults/sec\n\\process(W3WP)\\page faults/SEC'), 'counters', 'duplicate differing only in case');
  assert.equal(problem('\\Process(w3wp)\\Page Faults/sec\n\\Process(w3wp)\\Page Faults/sec'), 'counters', 'an exact repeat is not hidden');
  const fifty = Array.from({ length: 50 }, (_, i) => '\\Object\\Counter ' + i);
  assert.equal(problem(fifty.join('\n')), null, '50 is allowed');
  assert.equal(problem([...fifty, '\\Object\\Counter 50'].join('\n')), 'counters', '51');
  const base = '\\Object\\';
  assert.equal(problem(base + 'a'.repeat(200 - base.length)), null, '200 characters is allowed');
  assert.equal(problem(base + 'a'.repeat(201 - base.length)), 'counters', '201 characters');
});

test('the counters problem is hidden while the row is off, and settingsProblem repeats the check', () => {
  const s = ready('pc');
  assert.equal(w.stepProblem({ ...s, countersText: '' }, 'record'), null);
  assert.equal(w.stepProblem({ ...s, countersText: 'bad' }, 'record'), null);
  assert.equal(w.next(walkTo(s, 'record')).step, 'watch');
  const bad = withRows(s, { counters: true }, 'bad');
  assert.equal(w.next({ ...bad, step: 'record' }).step, 'record', 'Next is refused');
  assert.equal(bb.settingsProblem(w.buildSettings(bad)), 'counters');
  assert.equal(w.stepProblem({ ...bad, step: 'review' }, 'review'), 'counters');
  assert.equal(bb.settingsProblem(bb.defaultSettings('web-a')), null, 'no counters block, no check');
  assert.equal(bb.settingsProblem({ ...bb.defaultSettings('web-a'), counters: { extra: ['\\Memory\\Pool Nonpaged Bytes'] } }), null);
  assert.equal(bb.settingsProblem({ ...bb.defaultSettings('web-a'), counters: { extra: [] } }), null, 'the server accepts an empty list; the wizard refuses it on Record instead');
  assert.equal(w.stepProblem({ ...withRows(s, { counters: true }, ''), step: 'record' }, 'record'), 'counters');
  assert.equal(bb.stepOfSetting('counters'), 'record');
});

test('"Use defaults, go to review" puts the three collector rows and the counters text back', () => {
  let s = ready('pc');
  s = { ...withRows(s, { facts: false, counters: true, paths: false }, '\\A\\b'), };
  const r = w.useDefaultsToReview(s);
  assert.deepEqual(r.collectorRows, ROWS_ON);
  assert.equal(r.countersText, w.initialState().countersText);
  assert.equal('collectors' in w.buildSettings(r), false);
  assert.equal('counters' in w.buildSettings(r), false);
});
