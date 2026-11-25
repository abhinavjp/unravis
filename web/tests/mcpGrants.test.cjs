const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-347: the AI access screen. The model (what a card sends, what each state says), the calls (token, always the revision of the card),
// the words, and what the screen's source must never contain (markup from host text, inline style or script).
const g = loadTypeScript('src/lib/mcpGrants.ts');
const api = loadTypeScript('src/lib/mcpGrantsApi.ts');
const { MCP_TEXT, TOOL_LABELS, mcpLintEntries, toolLabel } = loadTypeScript('src/lib/copy/mcpGrantsCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

const AGG = ['audit_get_guide', 'audit_list_sources', 'audit_compare', 'audit_get_groups', 'audit_get_timeline', 'audit_compare_timelines', 'audit_get_breakdown'];
const EVI = [...AGG, 'audit_get_requests', 'audit_get_slowest', 'audit_get_activity'];
const tiers = [{ id: 'aggregate', tools: AGG }, { id: 'evidence', tools: EVI }];
const limits = { ...g.EMPTY_LIMITS };
const profile = (extra = {}) => ({
  id: 'claude', state: 'pending', expired: false, rev: 3, enrolled: true, contested: 0, hostName: 'Claude Desktop', hostVersion: '1.4', exe: 'C:\\a\\claude.exe', ppid: 5,
  tier: '', tools: [], sources: [], identifiers: false, identifiersUntil: '', requestedIdentifiers: false, code: false, idleMinutes: 0, expiresAt: '', approvedAt: '', createdAt: '2026-11-24T10:00:00Z', lastCallAt: '', claimed: false, ...extra
});
const text = { notConnected: 'nc', noTools: 'nt', noSources: 'ns', contested: 'ct', notWaiting: 'nw' };

test('the list is read strictly: an unknown mode or a row with no id or a made-up state is dropped, never guessed', () => {
  assert.equal(g.parseList(null), null);
  assert.equal(g.parseList({ mode: 'maybe' }), null);
  const list = g.parseList({ mode: 'on', stopped: true, now: 'n', tiers: [{ id: 'aggregate', tools: AGG }, { id: 'root', tools: ['x'] }], profiles: [{ id: 'a', state: 'pending', rev: 2 }, { id: '', state: 'pending' }, { id: 'b', state: 'superuser' }, 7, null] });
  assert.equal(list.mode, 'on');
  assert.equal(list.stopped, true);
  assert.deepEqual(list.tiers.map((t) => t.id), ['aggregate']);
  assert.deepEqual(list.profiles.map((p) => p.id), ['a']);
  assert.deepEqual(g.parseList({ mode: 'legacy' }).profiles, []);
  assert.equal(g.parseList({ mode: 'off', reason: 'sealing' }).reason, 'sealing');
  assert.equal(g.parseList({ mode: 'off', reason: '<b>x</b>' }).reason, '', 'a reason the screen has no words for is dropped');
  // host text is kept as a string and never changed
  assert.equal(g.parseProfile({ id: 'a', state: 'pending', hostName: '<img src=x onerror=alert(1)>' }).hostName, '<img src=x onerror=alert(1)>');
  assert.equal(g.parseProfile({ id: 'a', state: 'pending', hostName: { toString: 1 } }).hostName, '');
});

test('waiting hosts come first, oldest first; approved and ended are newest first', () => {
  const groups = g.groupProfiles([
    profile({ id: 'b', createdAt: '2026-11-24T11:00:00Z' }), profile({ id: 'a', createdAt: '2026-11-24T10:00:00Z' }),
    profile({ id: 'c', state: 'active', createdAt: '2026-11-20T00:00:00Z' }), profile({ id: 'd', state: 'active', createdAt: '2026-11-21T00:00:00Z' }),
    profile({ id: 'e', state: 'revoked' }), profile({ id: 'f', state: 'denied' })
  ]);
  assert.deepEqual(groups.pending.map((p) => p.id), ['a', 'b']);
  assert.deepEqual(groups.active.map((p) => p.id), ['d', 'c']);
  assert.equal(groups.ended.length, 2);
});

test('an approval always carries the revision of the card, and cannot be built without one', () => {
  const draft = g.defaultDraft(tiers, limits, ['s1', 's2']);
  const body = g.approveBody(profile({ rev: 7 }), draft, tiers);
  assert.equal(body.expectRev, 7);
  assert.equal(body.tier, 'aggregate');
  assert.deepEqual(body.tools, AGG);
  assert.deepEqual(body.sources, ['s1', 's2']);
  assert.equal(body.identifiers, false);
  assert.equal(body.identifiersMinutes, 0);
  assert.equal(body.acceptContested, false);
  for (const rev of [0, -1, 1.5, NaN, undefined]) assert.throws(() => g.approveBody(profile({ rev }), draft, tiers), /revision/);
});

test('nothing is allowed by default: summaries only, no identifiers, 8 hours, and a tool outside the tier is never sent', () => {
  const d = g.defaultDraft(tiers, limits, ['s1']);
  assert.equal(d.tier, 'aggregate');
  assert.equal(d.identifiers, false);
  assert.equal(d.idleMinutes, 480);
  assert.equal(d.acceptContested, false);
  const sneaky = { ...d, tools: [...AGG, 'audit_get_requests'] };
  assert.ok(!g.approveBody(profile(), sneaky, tiers).tools.includes('audit_get_requests'));
  // identifiers: the duration goes only with the switch
  assert.equal(g.approveBody(profile(), { ...d, identifiers: true, identifiersMinutes: 480 }, tiers).identifiersMinutes, 480);
  assert.equal(g.approveBody(profile(), { ...d, identifiers: false, identifiersMinutes: 480 }, tiers).identifiersMinutes, 0);
});

test('the contested warning needs its own tick, and the tick counts only for a contested host', () => {
  const d = g.defaultDraft(tiers, limits, ['s1']);
  const contested = profile({ contested: 2 });
  assert.equal(g.draftProblem(contested, d, text), 'ct');
  assert.equal(g.draftProblem(contested, { ...d, acceptContested: true }, text), '');
  assert.equal(g.approveBody(contested, { ...d, acceptContested: true }, tiers).acceptContested, true);
  assert.equal(g.approveBody(profile(), { ...d, acceptContested: true }, tiers).acceptContested, false, 'a tick left over does not travel for a clean host');
});

test('Approve is not ready for a host that never connected, with no tools or no sources, or that is no longer waiting', () => {
  const d = g.defaultDraft(tiers, limits, ['s1']);
  assert.equal(g.draftProblem(profile({ enrolled: false }), d, text), 'nc');
  assert.equal(g.draftProblem(profile(), { ...d, tools: [] }, text), 'nt');
  assert.equal(g.draftProblem(profile(), { ...d, sources: [] }, text), 'ns');
  assert.equal(g.draftProblem(profile({ state: 'active' }), d, text), 'nw');
  assert.equal(g.draftProblem(profile(), d, text), '');
});

test('changing the tier keeps the picks the new tier has, and a rise adds the new tools switched on', () => {
  let d = g.defaultDraft(tiers, limits, ['s1']);
  d = { ...d, tools: ['audit_get_guide', 'audit_compare'] };
  const up = g.withTier(d, tiers, 'evidence');
  assert.equal(up.tier, 'evidence');
  assert.ok(up.tools.includes('audit_get_guide') && up.tools.includes('audit_compare'));
  assert.ok(!up.tools.includes('audit_get_breakdown'), 'a tool the person switched off stays off');
  assert.ok(['audit_get_requests', 'audit_get_slowest', 'audit_get_activity'].every((t) => up.tools.includes(t)));
  const down = g.withTier(up, tiers, 'aggregate');
  assert.deepEqual(down.tools, ['audit_get_guide', 'audit_compare']);
  assert.deepEqual(g.toggled(['a', 'b'], 'a', false), ['b']);
  assert.deepEqual(g.toggled(['a'], 'a', true), ['a']);
});

test('state chips say the state in words, not only in colour', () => {
  assert.equal(g.chipOf(profile(), false).label, 'Waiting for you');
  assert.equal(g.chipOf(profile({ enrolled: false }), false).label, 'Not connected');
  assert.equal(g.chipOf(profile({ state: 'active', claimed: true }), false).label, 'Active');
  assert.equal(g.chipOf(profile({ state: 'active', claimed: true }), true).label, 'Stopped');
  assert.equal(g.chipOf(profile({ state: 'active', claimed: true, expired: true }), false).label, 'Expired');
  assert.match(g.chipOf(profile({ state: 'active', claimed: false }), false).label, /not connected yet/);
  assert.equal(g.chipOf(profile({ state: 'revoked' }), false).label, 'Revoked');
  assert.equal(g.chipOf(profile({ state: 'denied' }), false).label, 'Denied');
});

test('times and scope read in plain words', () => {
  const now = '2026-11-24T12:00:00Z';
  assert.equal(g.ago(now, '2026-11-24T11:59:40Z'), 'just now');
  assert.equal(g.ago(now, '2026-11-24T11:55:00Z'), '5 minutes ago');
  assert.equal(g.ago(now, '2026-11-24T11:00:00Z'), '1 hour ago');
  assert.equal(g.ago(now, '2026-11-20T12:00:00Z'), '4 days ago');
  assert.equal(g.ago(now, ''), '');
  assert.equal(g.inFuture(now, '2026-11-24T12:05:00Z'), 'in 5 minutes');
  assert.equal(g.inFuture(now, '2026-11-24T20:00:00Z'), 'in 8 hours');
  assert.equal(g.inFuture(now, '2026-11-24T11:00:00Z'), 'now');
  assert.equal(g.span(60), '1 hour');
  assert.equal(g.span(10080), '7 days');
  assert.equal(g.scopeLine(profile({ tier: 'aggregate', tools: AGG, sources: ['a'] })), 'Summaries only, 7 tools, 1 source, no user ids or addresses');
  assert.match(g.scopeLine(profile({ tier: 'evidence', tools: EVI, sources: ['a', 'b'], identifiers: true })), /Summaries and request rows, 10 tools, 2 sources, user ids and addresses on/);
  assert.deepEqual(g.sourceNames(['a', 'x'], new Map([['a', 'West']])), ['West', 'x']);
});

test('every tool a tier can hold has a plain label', () => {
  for (const t of EVI) assert.ok(TOOL_LABELS[t], t);
  assert.equal(toolLabel('audit_new_tool'), 'audit_new_tool');
});

test('the words pass the copy checks', () => {
  const findings = lintCopy(mcpLintEntries(), []);
  assert.deepEqual(findings.filter((f) => f.rule !== 'undefined-term').map((f) => f.id + ': ' + f.message), []);
  assert.match(MCP_TEXT.hostedWarning, /cannot take results back/);
  assert.match(MCP_TEXT.stopButton, /Stop all AI access/);
});

/* ------------------------------------------------------------------ the calls */

const TOKEN = 'mcp-screen-token';
const realFetch = globalThis.fetch;
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });
const store = (token) => ({ getItem: () => token, setItem() {}, removeItem() {} });
function fake(status, body) {
  const calls = [];
  globalThis.sessionStorage = store(TOKEN);
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), method: init.method, headers: init.headers, body: init.body, credentials: init.credentials });
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}

test('the list is a GET with the session token and nothing else', async () => {
  const calls = fake(200, { mode: 'on', stopped: false, profiles: [] });
  const list = await api.listHosts();
  assert.equal(list.mode, 'on');
  assert.equal(calls[0].url, '/api/mcp/profiles');
  assert.equal(calls[0].method, 'GET');
  assert.equal(calls[0].headers['X-Audit-Token'], TOKEN);
  assert.equal(calls[0].credentials, 'same-origin');
  assert.equal(calls[0].body, undefined);
});

test('approve posts the card revision to that host and no other', async () => {
  const calls = fake(200, { ok: true, profile: { id: 'claude', state: 'active' } });
  const body = g.approveBody(profile({ rev: 4 }), g.defaultDraft(tiers, limits, ['s1']), tiers);
  const got = await api.mcpApi.approve('claude', body);
  assert.equal(got.state, 'active');
  assert.equal(calls[0].url, '/api/mcp/profiles/claude/approve');
  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].headers['Content-Type'], 'application/json');
  assert.equal(JSON.parse(calls[0].body).expectRev, 4);
  assert.equal(JSON.parse(calls[0].body).code, false, 'the default approval sends code mode off');
});

test('code mode is its own permission: off by default, sent exactly as ticked, read strictly from the server', async () => {
  const d = g.defaultDraft(tiers, limits, ['s1']);
  assert.equal(d.code, false);
  assert.equal(g.approveBody(profile(), d, tiers).code, false);
  assert.equal(g.approveBody(profile(), { ...d, code: true }, tiers).code, true);
  assert.equal(g.approveBody(profile(), { ...d, code: undefined }, tiers).code, false, 'a missing tick is off, never on');
  assert.equal(g.approveBody(profile(), { ...d, code: 'yes' }, tiers).code, false, 'only a real true turns it on');
  // ticking it changes nothing else: not the tier, the tools, or the identifiers choice
  const on = g.approveBody(profile(), { ...d, code: true }, tiers);
  const off = g.approveBody(profile(), d, tiers);
  assert.deepEqual({ ...on, code: false }, off);
  // the tier change and the identifiers tick keep the code tick as it was
  assert.equal(g.withTier({ ...d, code: true }, tiers, 'evidence').code, true);
  assert.equal(g.approveBody(profile(), { ...d, identifiers: true, code: false }, tiers).code, false);
  // the server's answer
  assert.equal(g.parseProfile({ id: 'a', state: 'active', code: true }).code, true);
  assert.equal(g.parseProfile({ id: 'a', state: 'active' }).code, false, 'an answer without the field reads as off');
  assert.equal(g.parseProfile({ id: 'a', state: 'active', code: 'true' }).code, false);
  // and the call really posts it
  const calls = fake(200, { ok: true, profile: { id: 'claude', state: 'active', code: true } });
  const got = await api.mcpApi.approve('claude', g.approveBody(profile({ rev: 4 }), { ...d, code: true }, tiers));
  assert.equal(got.code, true);
  assert.equal(JSON.parse(calls[0].body).code, true);
});

test('code mode has its own checkbox on the card, outside the identifiers choice, with a plain sentence, and a line on an approved row', () => {
  const card = read('src/components/McpApprovalCard.svelte');
  assert.match(card, /<input type="checkbox" data-testid="mcp-code" checked=\{draft\.code\}/);
  assert.match(card, /\.\.\.draft, code: e\.currentTarget\.checked/);
  const fieldsets = card.match(/<fieldset[\s\S]*?<\/fieldset>/g) || [];
  const mine = fieldsets.filter((f) => f.includes('data-testid="mcp-code"'));
  assert.equal(mine.length, 1);
  assert.ok(!mine[0].includes('draft.identifiers'), 'the code tick is not inside the identifiers fieldset');
  const ids = fieldsets.find((f) => f.includes('draft.identifiers'));
  assert.ok(ids && !ids.includes('mcp-code') && !ids.includes('draft.code'));
  assert.equal(MCP_TEXT.codeLabel, 'Allow code mode');
  assert.ok(MCP_TEXT.codeNote.split(/[.!?]\s/).length <= 3 && /off/i.test(MCP_TEXT.codeNote), 'a short plain note that says it is off by default');
  const panel = read('src/components/McpAccessPanel.svelte');
  assert.match(panel, /\{#if p\.code\}<p class="scope" data-testid="mcp-row-code">/);
});

test('every other action sends the revision it showed, and a host name that is not a plain id never reaches a URL', async () => {
  const calls = fake(200, { ok: true });
  await api.mcpApi.revoke('claude', 5);
  await api.mcpApi.deny('claude', 5);
  await api.mcpApi.reissue('claude', 5);
  await api.mcpApi.renew('claude', 5);
  await api.mcpApi.remove('claude', 5);
  assert.deepEqual(calls.map((c) => c.url), ['revoke', 'deny', 'reissue', 'renew', 'remove'].map((a) => '/api/mcp/profiles/claude/' + a));
  for (const c of calls) assert.equal(JSON.parse(c.body).expectRev, 5);
  const before = calls.length;
  for (const bad of ['../stop', 'a/b', 'A', '', 'x'.repeat(33), 'a b', 'a?b']) {
    await assert.rejects(() => api.mcpApi.revoke(bad, 1), (e) => e.code === 'bad_id');
  }
  assert.equal(calls.length, before, 'a bad id makes no request');
});

test('stop and resume are one POST each; a stop that could not be saved says so', async () => {
  let calls = fake(200, { ok: true, stopped: true, saved: true });
  assert.deepEqual(await api.mcpApi.stop(), { stopped: true, saved: true });
  assert.equal(calls[0].url, '/api/mcp/stop');
  assert.equal(calls[0].method, 'POST');
  fake(200, { ok: true, stopped: true, saved: false });
  assert.deepEqual(await api.mcpApi.stop(), { stopped: true, saved: false });
  calls = fake(200, { ok: true, stopped: false });
  assert.deepEqual(await api.mcpApi.resume(), { stopped: false });
  assert.equal(calls[0].url, '/api/mcp/resume');
});

test('a refusal keeps the server code and its fixed text; a stale card is told apart', async () => {
  fake(409, { error: 'This host changed since you opened it. Reload the list and look again.', code: 'stale' });
  await assert.rejects(() => api.mcpApi.revoke('claude', 1), (e) => e.code === 'stale' && e.status === 409 && /changed since/.test(e.message));
  fake(500, null);
  await assert.rejects(() => api.mcpApi.deny('claude', 1), (e) => e.status === 500 && /refused \(500\)/.test(e.message));
});

test('no session token means no request', async () => {
  let called = 0;
  globalThis.sessionStorage = store('');
  globalThis.fetch = async () => { called++; return new Response('{}'); };
  await assert.rejects(() => api.listHosts(), (e) => e.code === 'no_session');
  await assert.rejects(() => api.mcpApi.stop(), (e) => e.code === 'no_session');
  assert.equal(called, 0);
});

/* ------------------------------------------------------------------ the source of the screen */

const SCREEN = ['src/components/McpAccessPanel.svelte', 'src/components/McpApprovalCard.svelte'];

test('host text is shown as text: no {@html}, no innerHTML, no inline style, no inline handler, no script', () => {
  for (const file of [...SCREEN, 'src/lib/mcpGrants.ts', 'src/lib/mcpGrantsApi.ts', 'src/lib/copy/mcpGrantsCopy.ts']) {
    const src = read(file);
    assert.ok(!/\{@html/.test(src), file + ' has {@html}');
    assert.ok(!/innerHTML|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function|javascript:/.test(src), file + ' builds markup or code');
  }
  for (const file of SCREEN) {
    const src = read(file);
    const markup = src.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<style[\s\S]*?<\/style>/g, '');
    assert.ok(!/\sstyle\s*=/.test(markup), file + ' has an inline style attribute');
    assert.ok(!/\sstyle:/.test(markup), file + ' sets a style from script');
    assert.ok(!/\son[a-z]+\s*=\s*"/.test(markup), file + ' has a quoted inline handler');
  }
});

test('the host fields are printed by name from the data and each one sits in the "reported by the program" block', () => {
  const card = read('src/components/McpApprovalCard.svelte');
  for (const field of ['profile.hostName', 'profile.hostVersion', 'profile.exe']) assert.ok(card.includes('{' + field), field);
  assert.match(card, /<section class="who" aria-label=\{T\.reportedLabel\}>/);
  assert.match(card, /data-testid="mcp-host-exe"/);
});

test('Approve is always built through approveBody (the revision), and the panel never posts a body of its own', () => {
  const panel = read('src/components/McpAccessPanel.svelte');
  assert.match(panel, /mcpApi\.approve\(p\.id, approveBody\(p, /);
  assert.ok(!/fetch\(/.test(panel), 'the panel makes no request of its own');
  assert.match(panel, /visiblePoll\(/, 'the list is read again only while the page is visible');
  assert.ok(!/setInterval\(/.test(panel));
  for (const action of ['deny', 'revoke', 'renew', 'reissue', 'remove']) assert.match(panel, new RegExp('mcpApi\\.' + action + '\\(p\\.id, p\\.rev\\)'));
});

test('the global stop is one obvious button at the top, and Resume replaces it while stopped', () => {
  const panel = read('src/components/McpAccessPanel.svelte');
  assert.equal((panel.match(/data-testid="mcp-stop"/g) || []).length, 1);
  assert.match(panel, /\{#if stopped\}[\s\S]*data-testid="mcp-resume"[\s\S]*\{:else\}[\s\S]*data-testid="mcp-stop"/);
  assert.match(panel, /\.stopbar \{ position: sticky; top: 0;/);
  assert.match(panel, /class="primary-button danger-fill" data-testid="mcp-stop"/);
});

test('the header has the button and the panel is a lazy side panel, not part of a case', () => {
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /openSidePanel\('mcp'\)/);
  assert.match(header, /data-testid="open-mcp"/);
  const app = read('src/App.svelte');
  assert.match(app, /lazyComponent\(\(\) => import\('\.\/components\/McpAccessPanel\.svelte'\)\)/);
  assert.match(app, /panels\.sidePanel === 'mcp'/);
});
