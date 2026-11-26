const test = require('node:test');
const assert = require('node:assert/strict');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

const m = loadTypeScript('src/lib/codeMode.ts');
const copyMod = loadTypeScript('src/lib/copy/codeModeCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const apiMod = loadTypeScript('src/lib/codeModeApi.ts');
const storeMod = loadTypeScript('src/lib/codeModeStore.svelte.ts');
const flowMod = loadTypeScript('src/lib/codeModeFlow.svelte.ts');
const { CODE_TEXT } = copyMod;

const T0 = Date.parse('2026-11-26T10:00:00Z');
const iso = (ms) => new Date(ms).toISOString().replace(/\.\d{3}Z$/, 'Z');
const raw = (over = {}) => ({ enabled: true, allowDegraded: false, keepResults: false, allowIdentifiers: false, cliOpenUntil: '', damaged: false, ...over });
const settings = (over = {}) => m.parseSettings(raw(over));

/* ------------------------------------------------------------------ the words */

test('the words pass the copy checks', () => {
  const findings = lintCopy(copyMod.codeModeLintEntries(), []);
  assert.deepEqual(findings.filter((f) => f.rule !== 'undefined-term').map((f) => f.id + ': ' + f.message), []);
  assert.ok(copyMod.codeModeLintEntries().length > 150);
  assert.equal(copyMod.NO_LIMIT_PHRASE, 'no time limit');
});

/* ------------------------------------------------------------------ reading the settings */

test('the settings are read as the server sent them, and cliUnlimited only counts when the key is there', () => {
  const old = settings({ cliOpenUntil: iso(T0 + 90_000) });
  assert.equal(old.enabled, true);
  assert.equal(old.cliOpenUntil, T0 + 90_000);
  assert.equal(old.cliUnlimited, false);
  assert.equal(old.hasUnlimited, false);
  const next = settings({ cliUnlimited: true });
  assert.equal(next.cliUnlimited, true);
  assert.equal(next.hasUnlimited, true);
  assert.equal(settings({ cliUnlimited: false }).hasUnlimited, true);
  assert.equal(m.parseSettings(null), null);
  assert.equal(m.parseSettings([]), null);
  assert.equal(m.parseSettings({ enabled: 'yes' }), null);
  assert.equal(settings({ cliOpenUntil: 'not a time' }).cliOpenUntil, 0);
});

test('the clock offset is the server clock minus this one, and 0 without a usable Date header', () => {
  assert.equal(m.clockOffsetMs('Thu, 26 Nov 2026 10:00:05 GMT', T0), 5000);
  assert.equal(m.clockOffsetMs(null, T0), 0);
  assert.equal(m.clockOffsetMs('soon', T0), 0);
});

/* ------------------------------------------------------------------ the command line and the badge */

test('the command line is shut, open or unlimited by the server answer and the corrected clock', () => {
  const open = settings({ cliOpenUntil: iso(T0 + 120_000) });
  assert.deepEqual(m.cliState(open, T0, 0), { kind: 'open', leftMs: 120_000 });
  // The browser clock is 30 s behind the server: the server says 90 s are left.
  assert.deepEqual(m.cliState(open, T0 - 30_000, 30_000), { kind: 'open', leftMs: 120_000 });
  assert.equal(m.cliState(open, T0, 120_000).kind, 'shut');
  assert.equal(m.cliState(settings(), T0, 0).kind, 'shut');
  assert.equal(m.cliState(settings({ enabled: false, cliOpenUntil: iso(T0 + 120_000) }), T0, 0).kind, 'off');
  assert.equal(m.cliState(settings({ cliUnlimited: true }), T0, 0).kind, 'unlimited');
  assert.equal(m.cliState(null, T0, 0).kind, 'off');
  assert.equal(m.fmtClock(0), '00:00');
  assert.equal(m.fmtClock(1), '00:01');
  assert.equal(m.fmtClock(59_001), '01:00');
  assert.equal(m.fmtClock(3_600_000), '60:00');
});

test('the badge is hidden when off and says the full state otherwise', () => {
  assert.equal(m.badgeModel(null, T0, 0), null);
  assert.equal(m.badgeModel(settings({ enabled: false }), T0, 0), null);

  const shut = m.badgeModel(settings(), T0, 0);
  assert.equal(shut.tone, 'on');
  assert.equal(shut.full + ' · ' + shut.detail, 'Code mode on · command line shut');
  assert.equal(shut.short, 'On');
  assert.match(shut.aria, /Code mode is on and the command line is shut/);

  const open = m.badgeModel(settings({ cliOpenUntil: iso(T0 + 14 * 60_000 + 32_000) }), T0, 0);
  assert.equal(open.tone, 'on');
  assert.equal(open.detail, '14:32 left');
  assert.equal(open.isTime, true);
  assert.equal(open.short, '14:32');
  assert.match(open.aria, /15 minutes left/);

  const last = m.badgeModel(settings({ cliOpenUntil: iso(T0 + 8_000) }), T0, 0);
  assert.equal(last.tone, 'warn');
  assert.equal(last.detail, '00:08 left');
  const exactlyAMinute = m.badgeModel(settings({ cliOpenUntil: iso(T0 + 60_000) }), T0, 0);
  assert.equal(exactlyAMinute.tone, 'on');

  const danger = m.badgeModel(settings({ cliUnlimited: true }), T0, 0);
  assert.equal(danger.tone, 'danger');
  assert.equal(danger.icon, 'alert');
  assert.equal(danger.full + ' · ' + danger.detail, 'Code mode on · no time limit');
  assert.equal(danger.short, 'No limit');

  const damaged = m.badgeModel(settings({ enabled: false, damaged: true }), T0, 0);
  assert.equal(damaged.tone, 'warn');
  assert.equal(damaged.full + ' · ' + damaged.detail, 'Code mode off · settings file damaged');
  assert.equal(damaged.short, 'Off');
});

/* ------------------------------------------------------------------ the minutes and the typed phrase */

test('minutes are whole numbers from 1 to 60, and 0 is a way to say shut', () => {
  assert.equal(m.parseMinutes('15'), 15);
  assert.equal(m.parseMinutes(' 7 '), 7);
  for (const bad of ['', 'ten', '1.5', '-5', '+5', '1e1', '0x10']) assert.ok(Number.isNaN(m.parseMinutes(bad)), bad);
  assert.equal(m.minutesError('15'), '');
  assert.equal(m.minutesError('0'), '');
  assert.equal(m.minutesError('61'), CODE_TEXT.minutesTooBig);
  assert.equal(m.minutesError('99999999999999'), CODE_TEXT.minutesBad);
  assert.equal(m.minutesError(''), CODE_TEXT.minutesBad);
  const d = (mode, minsRaw) => ({ enabled: true, keep: false, deg: false, mode, minsRaw, typed: '' });
  assert.equal(m.canNext(d('timed', '60'), 4, true), true);
  assert.equal(m.canNext(d('timed', '61'), 4, true), false);
  assert.equal(m.canNext(d('timed', '0'), 4, true), false);
  assert.equal(m.canNext(d('timed', 'x'), 4, true), false);
  assert.equal(m.canNext(d('timed', 'x'), 3, true), true, 'only the command-line step ever turns Next off');
});

test('no time limit needs the typed phrase, ignoring case and outer spaces, and a server that has it', () => {
  const d = (typed) => ({ enabled: true, keep: false, deg: false, mode: 'unlimited', minsRaw: '15', typed });
  assert.equal(m.canNext(d(''), 4, true), false);
  assert.equal(m.canNext(d('no time'), 4, true), false);
  assert.equal(m.canNext(d('no time limit'), 4, true), true);
  assert.equal(m.canNext(d('  No Time Limit '), 4, true), true);
  assert.equal(m.canNext(d('no time limit'), 4, false), false, 'a server without cliUnlimited never lets Next go on');
});

test('the steps skip 3 to 5 while code mode stays off', () => {
  const on = { enabled: true, keep: false, deg: false, mode: 'shut', minsRaw: '15', typed: '' };
  const off = { ...on, enabled: false };
  assert.equal(m.nextStep(on, 1), 2);
  assert.equal(m.nextStep(off, 1), 5);
  assert.equal(m.backStep(off, 5), 1);
  assert.equal(m.backStep(on, 5), 4);
  assert.equal(m.stepSkipped(off, 3), true);
  assert.equal(m.stepSkipped(on, 3), false);
  assert.equal(m.stepSkipped(off, 1), false);
});

/* ------------------------------------------------------------------ what is sent */

const draft = (over = {}) => ({ enabled: true, keep: false, deg: false, mode: 'shut', minsRaw: '15', typed: '', ...over });

test('the save carries only keys the server knows, and cliUnlimited only when the GET had it', () => {
  const old = settings();
  const next = settings({ cliUnlimited: false });
  assert.deepEqual(m.buildPatch(old, draft()), { enabled: true, allowDegraded: false, keepResults: false, cliMinutes: 0 });
  assert.ok(!('cliUnlimited' in m.buildPatch(old, draft())));
  assert.ok(!('cliUnlimited' in m.buildPatch(old, draft({ mode: 'timed', minsRaw: '30' }))));
  assert.deepEqual(m.buildPatch(old, draft({ mode: 'timed', minsRaw: '30', keep: true, deg: true })), { enabled: true, allowDegraded: true, keepResults: true, cliMinutes: 30 });
  assert.deepEqual(m.buildPatch(old, draft({ mode: 'keep' })), { enabled: true, allowDegraded: false, keepResults: false });
  assert.deepEqual(m.buildPatch(old, draft({ enabled: false, mode: 'timed' })), { enabled: false }, 'off sends no window key: the server closes it');
  assert.deepEqual(m.buildPatch(next, draft()), { enabled: true, allowDegraded: false, keepResults: false, cliMinutes: 0, cliUnlimited: false });
  const unlimited = m.buildPatch(next, draft({ mode: 'unlimited', typed: 'no time limit' }));
  assert.deepEqual(unlimited, { enabled: true, allowDegraded: false, keepResults: false, cliUnlimited: true });
  assert.ok(!('cliMinutes' in unlimited), 'never minutes above 0 together with cliUnlimited');
  const allowed = new Set(['enabled', 'allowDegraded', 'keepResults', 'cliMinutes', 'cliUnlimited']);
  for (const p of [m.buildPatch(next, draft()), unlimited, m.quickPatch(next, 'closecli'), m.quickPatch(next, 'tofifteen')]) {
    for (const k of Object.keys(p)) assert.ok(allowed.has(k), k);
  }
});

test('a save that cannot be sent is refused before any call', () => {
  assert.throws(() => m.buildPatch(settings(), draft({ mode: 'unlimited', typed: 'no time limit' })), m.PatchError);
  assert.throws(() => m.buildPatch(settings({ cliUnlimited: false }), draft({ mode: 'unlimited', typed: 'no' })), m.PatchError);
  assert.throws(() => m.buildPatch(settings(), draft({ mode: 'timed', minsRaw: '61' })), m.PatchError);
  assert.throws(() => m.buildPatch(settings(), draft({ mode: 'timed', minsRaw: '' })), m.PatchError);
  assert.deepEqual(m.buildPatch(settings(), draft({ mode: 'timed', minsRaw: '0' })).cliMinutes, 0);
});

test('the buttons on the Result step send one small save each', () => {
  assert.deepEqual(m.quickPatch(settings(), 'off'), { enabled: false });
  assert.deepEqual(m.quickPatch(settings(), 'tofifteen'), { cliMinutes: 15 });
  assert.deepEqual(m.quickPatch(settings(), 'closecli'), { cliMinutes: 0 });
  assert.deepEqual(m.quickPatch(settings({ cliUnlimited: true }), 'closecli'), { cliMinutes: 0, cliUnlimited: false });
});

test('a reply that does not match what was asked is not reported as done', () => {
  const now = T0;
  const open = settings({ cliOpenUntil: iso(T0 + 900_000) });
  assert.equal(m.checkApplied({ enabled: true, cliMinutes: 15 }, open, now, 0), 'ok');
  assert.equal(m.checkApplied({ enabled: true, cliMinutes: 15 }, settings(), now, 0), 'notApplied');
  assert.equal(m.checkApplied({ cliMinutes: 0 }, open, now, 0), 'notApplied');
  assert.equal(m.checkApplied({ cliMinutes: 0 }, settings({ cliUnlimited: true }), now, 0), 'noLimitStillOn');
  assert.equal(m.checkApplied({ cliMinutes: 15 }, settings({ cliUnlimited: true }), now, 0), 'noLimitStillOn');
  assert.equal(m.checkApplied({ cliUnlimited: true }, settings(), now, 0), 'notApplied');
  assert.equal(m.checkApplied({ cliUnlimited: true }, settings({ cliUnlimited: true }), now, 0), 'ok');
  assert.equal(m.checkApplied({ enabled: false }, settings({ enabled: false }), now, 0), 'ok');
  assert.equal(m.checkApplied({ enabled: false }, settings(), now, 0), 'notApplied');
  assert.equal(m.checkApplied({ enabled: true, keepResults: true }, settings(), now, 0), 'notApplied');
});

/* ------------------------------------------------------------------ the assistants */

test('assistants are read from AI access: allowed ones are those approved with code mode, refused ones are left out', () => {
  const p = (id, over) => ({ id, state: 'active', expired: false, hostName: id.toUpperCase(), exe: id + '.exe', code: false, lastCallAt: '', ...over });
  const list = {
    now: '2026-11-26T10:00:00Z',
    profiles: [p('a', { code: true, lastCallAt: '2026-11-26T09:58:00Z' }), p('b'), p('c', { state: 'pending' }), p('d', { state: 'revoked' }), p('e', { state: 'denied' }), p('f', { expired: true, code: true })]
  };
  const rows = m.assistantRows(list);
  assert.deepEqual(rows.map((r) => r.id + ':' + r.tone), ['a:allowed', 'b:notAllowed', 'c:waiting', 'd:gone', 'f:gone']);
  assert.equal(rows[0].meta, 'a.exe · last used 2 minutes ago');
  assert.equal(rows[1].meta, 'b.exe · never used');
});

/* ------------------------------------------------------------------ the calls */

const realFetch = globalThis.fetch;
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });
const sessionStore = { getItem: () => 'code-screen-token', setItem() {}, removeItem() {} };
function fake(status, body, headers = {}) {
  const calls = [];
  globalThis.sessionStorage = sessionStore;
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), method: init.method, headers: init.headers, body: init.body, credentials: init.credentials });
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } });
  };
  return calls;
}

test('the read is a GET with the session token and brings back the server clock', async () => {
  const calls = fake(200, raw(), { Date: 'Thu, 26 Nov 2026 10:00:00 GMT' });
  const reply = await apiMod.codeApi.get();
  assert.equal(calls[0].url, '/api/code/settings');
  assert.equal(calls[0].method, 'GET');
  assert.equal(calls[0].headers['X-Audit-Token'], 'code-screen-token');
  assert.equal(calls[0].body, undefined);
  assert.equal(reply.settings.enabled, true);
  assert.equal(reply.serverDate, 'Thu, 26 Nov 2026 10:00:00 GMT');
});

test('the save is a PUT of exactly the patch', async () => {
  const calls = fake(200, raw({ enabled: false }));
  await apiMod.codeApi.put({ enabled: false });
  assert.equal(calls[0].method, 'PUT');
  assert.equal(calls[0].headers['Content-Type'], 'application/json');
  assert.deepEqual(JSON.parse(calls[0].body), { enabled: false });
});

test('a refusal brings the server words and code; 503 is "not available in this run"', async () => {
  fake(400, { code: 'bad_request', message: 'The command line can be allowed for 1 to 60 minutes, or 0 to close it.' });
  await assert.rejects(apiMod.codeApi.put({ cliMinutes: 61 }), (e) => e.status === 400 && e.code === 'bad_request' && /1 to 60 minutes/.test(e.message));
  fake(503, { code: 'code_mode_unavailable', message: 'Code mode is not available.' });
  await assert.rejects(apiMod.codeApi.get(), (e) => e.code === apiMod.UNAVAILABLE_CODE);
  fake(200, { nonsense: true });
  await assert.rejects(apiMod.codeApi.get(), (e) => e.code === 'unreadable');
  globalThis.sessionStorage = { getItem: () => '', setItem() {}, removeItem() {} };
  await assert.rejects(apiMod.codeApi.get(), (e) => e.code === 'no_session');
});

/* ------------------------------------------------------------------ the store */

function clockHarness() {
  const h = { now: T0, timers: [], focus: [], gets: 0, puts: [], reply: null, putReply: null, getError: null, putError: null, held: null };
  h.deps = {
    now: () => h.now,
    every: (fn, ms) => { const t = { fn, ms, live: true }; h.timers.push(t); return t; },
    cancel: (t) => { t.live = false; },
    onFocus: (fn) => { h.focus.push(fn); return () => { h.focus = h.focus.filter((f) => f !== fn); }; },
    api: {
      get: async () => {
        h.gets++;
        if (h.held) { const gate = h.held; h.held = null; const answer = h.heldAnswer; return gate.then(() => ({ settings: answer, serverDate: new Date(h.now).toUTCString() })); }
        if (h.getError) throw h.getError;
        return { settings: h.reply, serverDate: new Date(h.now).toUTCString() };
      },
      put: async (patch) => {
        h.puts.push(patch);
        if (h.putError) throw h.putError;
        return { settings: h.putReply ?? h.reply, serverDate: new Date(h.now).toUTCString() };
      }
    }
  };
  h.fire = (ms) => h.timers.filter((t) => t.ms === ms && t.live).forEach((t) => t.fn());
  return h;
}
const tick = () => new Promise((r) => setImmediate(r));

test('the store reads at start, every 30 seconds and on focus, and stops cleanly', async () => {
  const h = clockHarness();
  h.reply = settings();
  const s = new storeMod.CodeModeStore(h.deps);
  s.start();
  s.start();
  await tick();
  assert.equal(h.gets, 1);
  assert.equal(s.status, 'ready');
  assert.deepEqual(h.timers.map((t) => t.ms).sort((a, b) => a - b), [1000, 30000]);
  h.fire(30000); await tick();
  assert.equal(h.gets, 2);
  h.focus[0](); await tick();
  assert.equal(h.gets, 3);
  s.stop();
  assert.ok(h.timers.every((t) => !t.live));
  assert.equal(h.focus.length, 0);
});

test('the countdown runs on the corrected clock and the settings are read again once at zero', async () => {
  const h = clockHarness();
  h.reply = settings({ cliOpenUntil: iso(T0 + 3_000) });
  const s = new storeMod.CodeModeStore(h.deps);
  s.start(); await tick();
  assert.equal(s.badge.detail, '00:03 left');
  h.now += 1000; s.tick();
  assert.equal(s.badge.detail, '00:02 left');
  assert.equal(s.badge.tone, 'warn');
  h.now += 2000; s.tick(); await tick();
  assert.equal(h.gets, 2, 'one read when the window ends by the clock');
  h.now += 1000; s.tick(); await tick();
  assert.equal(h.gets, 2, 'not one read per tick');
  assert.equal(s.cli.kind, 'shut');
  // The server answers that the window is closed.
  h.reply = settings();
  h.fire(30000); await tick();
  assert.equal(s.badge.detail, 'command line shut');
});

test('a browser clock behind the server is corrected by the Date header', async () => {
  const h = clockHarness();
  h.reply = settings({ cliOpenUntil: iso(T0 + 600_000) });
  h.deps.api.get = async () => ({ settings: h.reply, serverDate: new Date(T0 + 60_000).toUTCString() });
  const s = new storeMod.CodeModeStore(h.deps);
  await s.refresh();
  assert.equal(s.offsetMs, 60_000);
  assert.equal(s.badge.detail, '09:00 left');
});

test('a failed read keeps the last good answer (a danger badge must not vanish) and says what happened', async () => {
  const h = clockHarness();
  h.reply = settings({ cliUnlimited: true });
  const s = new storeMod.CodeModeStore(h.deps);
  await s.refresh();
  assert.equal(s.badge.tone, 'danger');
  h.getError = new apiMod.CodeApiError('Unravis could not be reached. Check that it is still running.', 0, 'unreachable');
  await s.refresh();
  assert.equal(s.status, 'ready');
  assert.equal(s.badge.tone, 'danger');
  assert.match(s.error, /could not be reached/);
  h.getError = null;
  await s.refresh();
  assert.equal(s.error, '');
});

test('with no earlier answer a failed read is an error, and 503 is "not available" with no badge', async () => {
  const h = clockHarness();
  h.getError = new apiMod.CodeApiError('x', 0, 'unreachable');
  const s = new storeMod.CodeModeStore(h.deps);
  await s.refresh();
  assert.equal(s.status, 'error');
  assert.equal(s.badge, null);
  const h2 = clockHarness();
  h2.getError = new apiMod.CodeApiError('x', 503, apiMod.UNAVAILABLE_CODE);
  const s2 = new storeMod.CodeModeStore(h2.deps);
  await s2.refresh();
  assert.equal(s2.status, 'unavailable');
  assert.equal(s2.badge, null);
});

test('a save shows the reply at once, then reads again; an older read cannot overwrite it', async () => {
  const h = clockHarness();
  h.reply = settings();
  const s = new storeMod.CodeModeStore(h.deps);
  await s.refresh();
  // A read is on its way (it will answer "off")...
  let release;
  h.held = new Promise((r) => { release = r; });
  const stale = settings({ enabled: false });
  h.heldAnswer = stale;
  const slow = s.refresh();
  // ...and a save lands first.
  h.putReply = settings({ cliOpenUntil: iso(T0 + 300_000) });
  h.reply = h.putReply;
  const getsBefore = h.gets;
  await s.save({ cliMinutes: 5 });
  assert.equal(s.cli.kind, 'open');
  assert.equal(h.gets, getsBefore + 1, 'the save is followed by a new read');
  release(); await slow; await tick();
  assert.equal(s.settings.enabled, true, 'the read that started before the save was dropped');
  assert.deepEqual(h.puts, [{ cliMinutes: 5 }]);
});

test('a refused save throws the server words and leaves what is shown as it was', async () => {
  const h = clockHarness();
  h.reply = settings();
  const s = new storeMod.CodeModeStore(h.deps);
  await s.refresh();
  h.putError = new apiMod.CodeApiError('The settings could not be saved. Nothing was changed.', 500, 'internal_error');
  await assert.rejects(s.save({ enabled: false }), /Nothing was changed/);
  assert.equal(s.settings.enabled, true);
  assert.equal(s.status, 'ready');
});

/* ------------------------------------------------------------------ the popup logic */

function flowOf(rawSettings, over = {}) {
  const h = clockHarness();
  h.reply = rawSettings;
  const store = new storeMod.CodeModeStore(h.deps);
  const hosts = over.hosts ?? { now: iso(T0), profiles: [] };
  const flow = new flowMod.CodeModeFlow(store, { listHosts: async () => hosts });
  return { h, store, flow };
}

test('the popup opens on the first step when code mode is off, and on Result when it is on or the file is damaged', async () => {
  let t = flowOf(settings({ enabled: false }));
  await t.store.refresh(); t.flow.open();
  assert.equal(t.flow.step, 0);
  assert.equal(t.flow.reachable(6), false);
  t = flowOf(settings());
  await t.store.refresh(); t.flow.open();
  assert.equal(t.flow.step, 6);
  t = flowOf(settings({ enabled: false, damaged: true }));
  await t.store.refresh(); t.flow.open();
  assert.equal(t.flow.step, 6);
});

test('nothing is saved while going through the steps; the one save is at the end', async () => {
  const { h, store, flow } = flowOf(settings({ enabled: false }));
  await store.refresh(); flow.open();
  flow.next();
  flow.setEnabled(true);
  flow.next();            // 2 Assistants
  flow.next();            // 3 Safety
  flow.setKeep(true);
  flow.next();            // 4 Command line
  flow.setMode('timed');
  flow.setMinutes('20');
  flow.next();            // 5 Review
  assert.equal(flow.step, 5);
  assert.equal(h.puts.length, 0, 'no PUT before the confirm');
  assert.equal(flow.confirmLabel, 'Turn on code mode');
  assert.deepEqual(flow.cliSummary, { value: 'Open for 20 minutes', sub: 'Starts when you press the button.', danger: false });
  h.putReply = settings({ keepResults: true, cliOpenUntil: iso(T0 + 1_200_000) });
  h.reply = h.putReply;
  assert.equal(await flow.commit(), true);
  assert.deepEqual(h.puts, [{ enabled: true, allowDegraded: false, keepResults: true, cliMinutes: 20 }]);
  assert.equal(flow.step, 6);
  assert.equal(flow.notice, CODE_TEXT.savedOn);
  assert.equal(flow.saveError, '');
});

test('keeping it off skips steps 3 to 5 and goes straight to Review', async () => {
  const { store, flow } = flowOf(settings({ enabled: false }));
  await store.refresh(); flow.open();
  flow.next();
  flow.next();
  assert.equal(flow.step, 5);
  assert.equal(flow.confirmLabel, 'Finish, keep it off');
  assert.equal(flow.skipped(3), true);
  flow.back();
  assert.equal(flow.step, 1);
});

test('typing 0 minutes means shut; a wrong number keeps Next off', async () => {
  const { store, flow } = flowOf(settings());
  await store.refresh(); flow.open(); flow.go(4);
  flow.setMode('timed');
  assert.equal(flow.nextOn, true);
  flow.setMinutes('61');
  assert.equal(flow.minsError, CODE_TEXT.minutesTooBig);
  assert.equal(flow.nextOn, false);
  assert.equal(flow.setMinutes('0'), true);
  assert.equal(flow.draft.mode, 'shut');
  assert.equal(flow.nextOn, true);
});

test('no time limit stays unreachable on a server that does not have it, and needs the phrase on one that does', async () => {
  const old = flowOf(settings());
  await old.store.refresh(); old.flow.open(); old.flow.go(4);
  old.flow.setMode('unlimited');
  assert.notEqual(old.flow.draft.mode, 'unlimited', 'the choice is ignored');
  assert.equal(old.flow.hasUnlimited, false);

  const next = flowOf(settings({ cliUnlimited: false }));
  await next.store.refresh(); next.flow.open(); next.flow.go(4);
  next.flow.setMode('unlimited');
  assert.equal(next.flow.nextOn, false);
  next.flow.setTyped('no time limit');
  assert.equal(next.flow.nextOn, true);
  next.flow.setMode('shut');
  assert.equal(next.flow.draft.typed, '', 'the phrase is forgotten when the choice changes');
  next.flow.setMode('unlimited');
  assert.equal(next.flow.nextOn, false);
});

test('a refused save keeps the step, shows the server words and changes nothing shown', async () => {
  const { h, store, flow } = flowOf(settings());
  await store.refresh(); flow.open(); flow.go(3); flow.setKeep(true); flow.go(5);
  h.putError = new apiMod.CodeApiError('The settings could not be saved. Nothing was changed.', 500, 'internal_error');
  assert.equal(await flow.commit(), false);
  assert.equal(flow.step, 5);
  assert.match(flow.saveError, /Nothing was changed/);
  assert.equal(flow.saving, false);
  assert.equal(store.settings.keepResults, false);
});

test('Close the command line now on a no-limit window is not reported as done when the server leaves it open', async () => {
  const { h, store, flow } = flowOf(settings({ cliUnlimited: true }));
  await store.refresh(); flow.open();
  h.putReply = settings({ cliUnlimited: true });
  assert.equal(await flow.quick('closecli'), false);
  assert.deepEqual(h.puts, [{ cliMinutes: 0, cliUnlimited: false }]);
  assert.equal(flow.saveError, CODE_TEXT.notAppliedNoLimit);
  assert.equal(flow.notice, '');
  h.putReply = settings({ cliUnlimited: false });
  h.reply = h.putReply;
  assert.equal(await flow.quick('closecli'), true);
  assert.equal(flow.notice, CODE_TEXT.closedCli);
  assert.equal(store.cli.kind, 'shut');
});

test('Turn off code mode is one save of enabled false', async () => {
  const { h, store, flow } = flowOf(settings({ cliOpenUntil: iso(T0 + 600_000) }));
  await store.refresh(); flow.open();
  h.putReply = settings({ enabled: false });
  h.reply = h.putReply;
  assert.equal(await flow.quick('off'), true);
  assert.deepEqual(h.puts, [{ enabled: false }]);
  assert.equal(store.badge, null);
  assert.equal(flow.notice, CODE_TEXT.turnedOff);
});

test('the Result step counts the assistants that may run scripts', async () => {
  const hosts = { now: iso(T0), profiles: [{ id: 'a', state: 'active', expired: false, hostName: 'Claude Desktop', exe: 'Claude.exe', code: true, lastCallAt: '' }, { id: 'b', state: 'active', expired: false, hostName: 'Cursor', exe: 'Cursor.exe', code: false, lastCallAt: '' }] };
  const { store, flow } = flowOf(settings(), { hosts });
  await store.refresh(); flow.open(); await tick();
  assert.equal(flow.assistantsStatus, 'ready');
  assert.deepEqual(flow.allowedAssistants.map((a) => a.name), ['Claude Desktop']);
  const bad = flowOf(settings());
  bad.flow = new flowMod.CodeModeFlow(bad.store, { listHosts: async () => { throw new Error('x'); } });
  await bad.store.refresh(); bad.flow.open(); await tick();
  assert.equal(bad.flow.assistantsStatus, 'error');
});
