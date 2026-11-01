// Re-runnable screenshot capture for the Unravis prototype flows guide (throwaway; not the product's test suite).
// Run:  NODE_PATH=/opt/node22/lib/node_modules node prototype/video/capture-flows.cjs [only-section-prefix]
// Writes prototype/video/images/flows/NNN-short-name.png (three digits, so more than 99 shots still sort) and MANIFEST.json (file, what it shows, how to reach it).
// A failed capture is logged and skipped; the run goes on. The prototype dock, its drawer and the scenario banner are hidden.
const { chromium } = require('playwright');
const path = require('path'), fs = require('fs');
const FILE = 'file://' + path.resolve(__dirname, '..', 'tracing-paper-PROTOTYPE.html');
const OUT = process.env.OUTDIR || path.resolve(__dirname, 'images', 'flows');
const ONLY = process.argv[2] || '';
fs.mkdirSync(OUT, { recursive: true });
if (!ONLY) for (const f of fs.readdirSync(OUT)) if (/^\d+-.*\.png$/.test(f)) fs.unlinkSync(path.join(OUT, f));

let pg, n = 0; const manifest = [], failures = [];
const HIDE = '.dock,.pdrawer,.scen{display:none !important} .topbar{position:static !important}';
const wait = ms => pg.waitForTimeout(ms);
const num = k => String(k).padStart(3, '0');

// shot: run `act` (state changes), then capture. target = undefined (whole page), a CSS selector (crop) or 'view' (visible window only).
async function S(slug, what, how, act, target) {
  n++; const file = num(n) + '-' + slug + '.png';
  try {
    if (act) await act();
    await wait(250);
    const p = path.join(OUT, file);
    if (target === 'view') await pg.screenshot({ path: p });
    else if (Array.isArray(target)) { const l = pg.locator(target[0]).first(); await l.scrollIntoViewIfNeeded().catch(() => {}); const b = await l.boundingBox(); const h = Math.min(b.height, 1100); await pg.screenshot({ path: p, fullPage: true, clip: { x: b.x, y: b.y + b.height - h, width: b.width, height: h } }); }
    else if (target) { const l = pg.locator(target).first(); await l.scrollIntoViewIfNeeded().catch(() => {}); await l.screenshot({ path: p }); }
    else await pg.screenshot({ path: p, fullPage: true });
    manifest.push({ file, what, how });
    if (process.env.DUMP && new RegExp(process.env.DUMP).test(slug)) { const t = await pg.evaluate(sel => (sel ? (document.querySelector(sel) || document.body) : document.getElementById('main') || document.body).innerText.replace(/\s+/g, ' '), typeof target === 'string' && target !== 'view' ? target : ''); console.log('DUMP ' + file + ': ' + t.slice(0, +(process.env.DUMPLEN || 1500))); }
    if (process.env.DEBUG) { const t = await pg.evaluate(() => (document.getElementById('main') || document.body).innerText.replace(/\s+/g, ' ').slice(0, 160)); console.log('ok  ' + file + '  | ' + t); }
  } catch (e) { failures.push(file); console.log('FAIL ' + file + ' -> ' + e.message.split('\n')[0]); }
}
// act only (no capture), failures logged
async function A(label, act) { try { await act(); await wait(200); } catch (e) { console.log('STEP FAILED ' + label + ' -> ' + e.message.split('\n')[0]); } }

const fresh = async (vw = 1440, vh = 900) => {
  await pg.setViewportSize({ width: vw, height: vh });
  await pg.goto(FILE); await pg.evaluate(() => { localStorage.clear(); sessionStorage.clear(); }); await pg.goto(FILE);
  await pg.waitForFunction(() => document.querySelector('#card-existing') || document.querySelector('#mk'));
  await pg.waitForFunction(() => !document.body.innerText.includes('Loading your cases'));
  await pg.addStyleTag({ content: HIDE }).then(h => h.evaluate(e => e.setAttribute('data-hide', '1'))); await wait(150);
};
const sc = async (screen, id) => { await pg.evaluate(([a, b]) => { loadScenario(a, b); }, [screen, id]); await wait(450); };
const btn = (name, o = {}) => pg.getByRole('button', Object.assign({ name, exact: true }, o)).first();
const click = async (name, o = {}) => { await btn(name, o).click(); await wait(300); };
const clickRe = async (re, o = {}) => { await pg.getByRole('button', Object.assign({ name: re }, o)).first().click(); await wait(300); };
const tab = async re => { await pg.getByRole('tab', { name: re }).click(); await wait(250); };
const fill = async (sel, v) => { await pg.fill(sel, v); await wait(120); };
const bind = k => 'input[data-bind="' + k + '"]';
const blk = id => 'section[aria-labelledby=' + id + ']';
const openCaseFromList = async title => { await pg.getByRole('button', { name: 'Open case ' + title }).click(); await pg.waitForSelector('#ch-title'); await wait(300); };
const toCaseHome = async () => { await sc('case', 'c-ready'); };

(async () => {
  const browser = await chromium.launch();
  pg = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  pg.on('pageerror', e => console.log('PAGE ERROR ' + e.message));
  const want = p => !ONLY || p.startsWith(ONLY);

  /* ================= 1. launch and master key ================= */
  if (want('1')) {
    await fresh();
    await S('launch-start-screen', 'The start screen on a normal launch: three cards.', 'Open the file with the key unlocked (prototype panel > Launch the app as > Normal).');
    await sc('start', 's-locked');
    await S('key-locked-unlock-screen', 'Master key locked: the unlock screen comes first, no case titles or counts.', 'Scenario s-locked (Launch conditions: Master key locked at launch).');
    await S('key-wrong-first-try', 'A wrong master key: one sentence, nothing revealed.', 'Unlock screen: type any wrong text, press Unlock.', async () => { await fill('#mk', 'not-the-key-1'); await click('Unlock'); });
    await S('key-wrong-throttled', 'After 3 wrong tries the screen asks to wait a few seconds.', 'Press Unlock with a wrong key 3 times in a row.', async () => { await fill('#mk', 'not-the-key-2'); await click('Unlock'); await fill('#mk', 'not-the-key-3'); await click('Unlock'); });
    await S('key-right-after-wait', 'Waited, typed the right key: back on the start screen.', 'Wait about 6 seconds, type orange-sunrise-2026 (test aid), press Unlock.', async () => { await wait(5600); await fill('#mk', 'orange-sunrise-2026'); await click('Unlock'); await pg.waitForFunction(() => RT.screen === 'start', null, { timeout: 4000 }); });
    await sc('start', 's-damaged');
    await S('key-file-damaged', 'The master key file is damaged: the only way forward is to start over.', 'Scenario s-damaged (Launch conditions: Master key file damaged).');
    await S('key-file-damaged-ticked', 'The start-over choice, with the acknowledgement box ticked.', 'Tick the box on the damaged-key screen.', async () => { await pg.locator('#main input[type=checkbox]').check(); });
    await S('key-file-set-aside', 'After Start over: the old file is set aside (kept, not deleted) and the screen offers Set a new master key.', 'Press Start over with a new master key.', async () => { await click('Start over with a new master key'); await wait(700); });
    await S('key-set-new-key-button-does-nothing', 'After pressing Set a new master key the prototype stays on this screen: its own move rules refuse the step from the unlock screen to the set-up form (a prototype gap; the fresh-PC route, Launch the app as Fresh PC, reaches the form).', 'Press Set a new master key (nothing visible happens).', async () => { await click('Set a new master key'); await wait(500); });
    await sc('start', 's-fresh');
    await S('fresh-pc-start-screen', 'First launch on a new PC: the start screen offers to protect cases with a master key.', 'Scenario s-fresh (Launch conditions: Fresh PC).');
    await S('master-key-setup-form', 'The set-up form for a master key.', 'Press Protect your cases with a master key.', async () => { await click('Protect your cases with a master key'); });
    await S('master-key-too-short', 'A master key that is too short is explained at once.', 'Type 3 characters in the first field.', async () => { await fill('#mk1', 'abc'); });
    await S('master-key-mismatch', 'The two entries differ: the form says so.', 'Type a long key in the first field and a different one in the second.', async () => { await fill('#mk1', 'a-long-master-key-1'); await fill('#mk2', 'a-long-master-key-2'); });
    await S('master-key-set-done', 'Key set: back on the start screen (the header now shows Lock cases).', 'Make both fields equal, tick the confirmation box if shown, press Set master key.', async () => { await fill('#mk2', 'a-long-master-key-1'); await pg.locator('#main input[type=checkbox]').check().catch(() => {}); await click('Set master key'); await wait(500); });
  }

  /* ================= 2. start screen ================= */
  if (want('2')) {
    await fresh();
    await S('start-three-cards', 'The start screen: Use an existing case, Enter an existing case, Create a new one.', 'Launch normally.');
    await S('start-card-existing-list', 'The case list with the different row states: open, closed, own key, needs another try, working copy missing, legacy, erased.', 'Start screen, left card.', null, blk('card-existing'));
    await S('start-card-enter', 'The Enter an existing case card: code field, Open case, Restore from a case file.', 'Start screen, middle card.', null, blk('card-enter'));
    await S('start-card-create', 'The Create a new one card.', 'Start screen, right card.', null, blk('card-create'));
    await S('start-erase-missing-copy', 'Erasing a case whose working copy is missing asks for its code.', 'Press Erase: Case whose files were moved.', async () => { await clickRe(/^Erase: Case whose/); }, blk('card-existing'));
    await S('start-erase-typed-code', 'The erase prompt with the code typed: Type movedcop to confirm.', 'Type movedcop in the box.', async () => { await pg.locator('section[aria-labelledby=card-existing] input').first().fill('movedcop'); await wait(200); }, blk('card-existing'));
    await S('start-erased-line-after-erase', 'After Erase this case: only one line is left, saying the case was erased, with Clear.', 'Press Erase this case.', async () => { await pg.locator('section[aria-labelledby=card-existing]').getByRole('button', { name: 'Erase this case', exact: true }).click(); await wait(500); }, blk('card-existing'));
    await S('start-erased-lines-cleared', 'Clear removes the erased lines from the list.', 'Press Clear.', async () => { await pg.locator('section[aria-labelledby=card-existing]').getByRole('button', { name: /^Clear/ }).first().click(); await wait(400); }, blk('card-existing'));
    await S('start-archived-list', 'The Archived cases toggle opened.', 'Press Archived cases (2).', async () => { await fresh(); await clickRe(/^Archived cases/); }, blk('card-existing'));
    await sc('start', 's-empty');
    await S('start-no-cases-yet', 'A new user: the list says there are no cases yet; the three cards are still there.', 'Scenario s-empty (No cases yet).');
    await sc('start', 's-fail');
    await S('start-list-fails', 'The case list could not be loaded, with Try again.', 'Scenario s-fail (Case list fails to load, then works).');
    await S('start-list-after-retry', 'Try again works the second time.', 'Press Try again.', async () => { await click('Try again'); });
    await sc('start', 's-many');
    await S('start-list-paging', 'Long list: 50 shown, with Show more cases.', 'Scenario s-many (120 cases). Only the bottom of the long list is shown.', null, [blk('card-existing')]);
    await S('start-list-show-more', 'After Show more cases.', 'Press Show more cases. Only the bottom of the long list is shown.', async () => { await clickRe(/^Show more cases/); }, [blk('card-existing')]);
    await sc('start', 's-old');
    await S('start-older-cases-note', 'The note about older cases left in the user folder.', 'Scenario s-old (Older cases left in the user folder).', null, blk('card-existing'));
    await sc('start', 's-archiving');
    await S('start-case-archiving', 'A case being archived shows Archiving 45% and has no Open button.', 'Scenario s-archiving.', null, blk('card-existing'));
  }

  /* ================= 3. enter by code, restore from a case file ================= */
  if (want('3')) {
    await fresh();
    await S('enter-code-invalid', 'A code that is not a code: the one not-found sentence.', 'Type "not a code" in the code field, press Open case.', async () => { await fill('#ec-code', 'not a code'); await click('Open case'); }, blk('card-enter'));
    await S('enter-code-short-refused', 'The 8-character code shown in lists is refused (the card asks for the full code).', 'Type payrollr, press Open case.', async () => { await fill('#ec-code', 'payrollr'); await click('Open case'); }, blk('card-enter'));
    await S('enter-code-full-opens-case', 'The full code (spaces, dashes and case ignored) opens the case home.', 'Prototype panel > Show the full case code, type it, press Open case.', async () => { const id = await pg.evaluate(() => ID.orders); await fill('#ec-code', id.slice(0, 8) + '-' + id.slice(8, 16) + ' ' + id.slice(16).toUpperCase()); await click('Open case'); await pg.waitForSelector('#ch-title'); });
    await A('back', async () => { await click('All cases: back to the start screen'); await pg.waitForSelector('#card-enter'); });
    await A('keyed code', async () => { const id = await pg.evaluate(() => ID.keyed); await fill('#ec-code', id); await click('Open case'); await pg.waitForSelector('#ck-open'); });
    await S('enter-code-keyed-case-key-card', 'A code for a case with its own key lands on the case key card.', 'Type the full code of the Vendor portal case, press Open case.');
    await A('back', async () => { await click('Cancel'); await wait(300); if (await pg.evaluate(() => RT.screen) !== 'start') await pg.evaluate(() => go('start')); await pg.waitForSelector('#card-enter'); });
    await S('restore-card-open', 'Restore from a case file: path and master key fields.', 'Press Restore from a case file.', async () => { await click('Restore from a case file'); }, blk('card-enter'));
    await S('restore-empty-path', 'Empty path refused.', 'Press Restore as a new case with nothing typed.', async () => { await click('Restore as a new case'); }, blk('card-enter'));
    await S('restore-wrong-extension', 'The file name must end in .unravis-case.', 'Path C:\\Cases\\x.txt, any key, Restore as a new case.', async () => { await fill('#ec-path', 'C:\\Cases\\x.txt'); await fill('#ec-key', 'k'); await click('Restore as a new case'); }, blk('card-enter'));
    await S('restore-network-path', 'Network paths are not accepted.', 'Path \\\\server\\share\\a.unravis-case, Restore as a new case.', async () => { await fill('#ec-path', '\\\\server\\share\\a.unravis-case'); await click('Restore as a new case'); }, blk('card-enter'));
    await S('restore-wrong-key', 'Wrong master key: the single failure sentence.', 'Valid path C:\\Cases\\orders-slowdown.unravis-case with the key "wrong".', async () => { await fill('#ec-path', 'C:\\Cases\\orders-slowdown.unravis-case'); await fill('#ec-key', 'wrong'); await click('Restore as a new case'); }, blk('card-enter'));
    await S('restore-success-new-case', 'Restore always makes a NEW case and opens it.', 'Same path, key "correct horse battery staple", Restore as a new case.', async () => { await fill('#ec-key', 'correct horse battery staple'); await click('Restore as a new case'); await pg.waitForFunction(() => RT.screen === 'case', null, { timeout: 5000 }); await wait(400); });
    await sc('start', 's-restore');
    await S('restore-scenario-card', 'Scenario with the restore card already open and a hint for the test values.', 'Scenario s-restore.', null, blk('card-enter'));
    await sc('start', 's-fresh');
    await S('restore-needs-master-key', 'On a fresh PC, restoring needs a master key first (set-up link).', 'Scenario s-fresh, press Restore from a case file.', async () => { await click('Restore from a case file'); }, blk('card-enter'));
  }

  /* ================= 4. create a new case ================= */
  if (want('4')) {
    await fresh();
    await S('create-name-step-empty', 'Step 1 of 3: Name. Next stays off until the name is valid.', 'Start screen > Create a new case.', async () => { await click('Create a new case'); await pg.waitForSelector('#wz-name'); });
    await S('create-name-too-short', 'A one-letter name: at least 2 characters.', 'Type "a" in the name field.', async () => { await fill('#wz-name', 'a'); await pg.locator('#wz-name').blur(); });
    await S('create-name-too-long', 'A name over 80 characters is refused at once.', 'Type 81 letters.', async () => { await fill('#wz-name', 'x'.repeat(81)); });
    await S('create-environments-deduplicated', 'Environments typed with a duplicate: the duplicate is left out and the note says so.', 'Name "Checkout slowdown", environments "Production, production, Staging".', async () => { await fill('#wz-name', 'Checkout slowdown'); await fill('#wz-envs', 'Production, production, Staging'); });
    await S('create-own-case-key-too-short', 'Own case key switched on, too short.', 'Tick the own-key box, type "short".', async () => { await pg.locator('[data-bind="W.caseKey.on"]').click(); await fill('#wz-ck', 'short'); });
    await S('create-own-case-key-mismatch', 'The two key entries differ.', 'Type a long key, then a different repeat.', async () => { await fill('#wz-ck', 'a-long-case-key-1'); await fill('#wz-ck2', 'a-long-case-key-2'); });
    await S('create-name-step-valid', 'Name step filled in correctly: Next is on.', 'Make the repeat equal to the key.', async () => { await fill('#wz-ck2', 'a-long-case-key-1'); });
    await S('create-sources-this-computer-start', 'Step 2 of 3: Sources. Four methods across the top; This computer is first.', 'Press Next.', async () => { await click('Next'); await pg.waitForSelector('text=Scan this computer'); });
    await S('create-sources-scan-results', 'Scan results: ready sources, needs-an-export and unreadable places.', 'Press Scan this computer.', async () => { await click('Scan this computer'); await pg.waitForSelector('text=IIS web log · Default Web Site (W3SVC1)'); });
    await S('create-sources-where-is-it', 'The Where is it? helper next to a place that could not be used (look in a folder yourself).', 'Press Where is it? beside the place that could not be used.', async () => { await clickRe(/^Where is it\?/); });
    await S('create-sources-one-found-added', 'One found source added with its Add button; it appears in Sources in this case on the right.', 'Press Add on IIS web log · Default Web Site (W3SVC1).', async () => { await pg.getByRole('button', { name: 'Add IIS web log · Default Web Site (W3SVC1)' }).click(); });
    await S('create-sources-add-all-ready', 'Add all ready sources adds every source that is ready in one go.', 'Press Add all N ready sources.', async () => { await clickRe(/^Add all \d+ ready sources/); });
    await S('create-sources-mongo-empty', 'MongoDB method: saved connection list, address, user, TLS, password, Test and list databases, and the roles script.', 'Tab MongoDB.', async () => { await tab(/MongoDB/); });
    await S('create-mongo-saved-connection', 'Choosing a saved connection fills in the address.', 'Pick Payroll audit Mongo (password saved) in the saved-connection list.', async () => { await pg.locator('select[data-bind="W.mongo.saved"]').selectOption({ label: 'Payroll audit Mongo (password saved)' }); });
    await S('create-mongo-address-no-port', 'An address with no scheme and no port: the default port 27017 is used and shown.', 'Type audit-mongo in the address field.', async () => { await pg.locator('select[data-bind="W.mongo.saved"]').selectOption(''); await fill(bind('W.mongo.addr'), 'audit-mongo'); });
    await S('create-mongo-bad-port', 'A bad port is named in words.', 'Type host:99999.', async () => { await fill(bind('W.mongo.addr'), 'host:99999'); });
    await S('create-mongo-databases-listed', 'After Test and list databases: a database checklist with a profiler chip each; system databases left out.', 'Address audit-mongo, press Test and list databases.', async () => { await fill(bind('W.mongo.addr'), 'audit-mongo'); await click('Test and list databases'); await pg.waitForSelector('input[data-act="dbPick"][data-k="mongo"]'); });
    await S('create-mongo-databases-ticked', 'Two databases ticked; an audit-named one defaults to audit data, the other to application data; each can be both.', 'Tick PayrollAudit and PayrollApp, then also tick application data on PayrollAudit.', async () => { await pg.locator('input[data-act="dbPick"][data-k="mongo"][data-n="PayrollAudit"]').click(); await pg.locator('input[data-act="dbPick"][data-k="mongo"][data-n="PayrollApp"]').click(); await pg.locator(bind('W.mongo.picks.0.app')).click(); });
    await S('create-mongo-added', 'The two databases added as sources from one connection.', 'Press Add 2 databases.', async () => { await click('Add 2 databases'); }, '.srcside');
    await S('create-mongo-permission-script', 'The MongoDB permission script (roles) that Unravis shows but never runs.', 'MongoDB tab, scroll to the roles script.', async () => { await tab(/MongoDB/); await pg.locator('text=clusterMonitor').first().scrollIntoViewIfNeeded(); }, '.stage-inner');
    await S('create-sql-form-empty', 'SQL Server method: where it runs, authentication, encrypt, trust certificate, remember password.', 'Tab SQL Server.', async () => { await tab(/SQL Server/); });
    await S('create-sql-no-port', 'Server name with no port: the usual port 1433 is used.', 'Type db1 in the server field.', async () => { await fill(bind('W.sql.server'), 'db1'); });
    await S('create-sql-azure-flavor', 'Where it runs: Azure SQL Database (one connection per database).', 'Choose Azure SQL Database in the where-it-runs list.', async () => { await pg.locator('select[data-bind="W.sql.flavor"]').selectOption('azure'); });
    await S('create-sql-windows-auth', 'Windows Authentication hides the user and password fields.', 'Choose Windows Authentication.', async () => { await pg.locator('select[data-bind="W.sql.flavor"]').selectOption('server'); await pg.locator('select[data-bind="W.sql.auth"]').selectOption('windows'); });
    await S('create-sql-encrypt-trust-remember', 'Encrypt options, Trust server certificate and Remember password.', 'SQL authentication, Encrypt Strict, tick Trust server certificate and Remember password.', async () => { await pg.locator('select[data-bind="W.sql.auth"]').selectOption('sql'); await pg.locator('select[data-bind="W.sql.encrypt"]').selectOption('strict'); await pg.locator(bind('W.sql.trust')).check(); await pg.locator(bind('W.sql.remember')).check(); });
    await S('create-sql-saved-connection', 'Choosing a saved SQL connection fills the form.', 'Pick PROD-SQL-01 (orders) (password saved) in the saved list.', async () => { await pg.locator('select[data-bind="W.sql.saved"]').selectOption({ label: 'PROD-SQL-01 (orders) (password saved)' }); });
    await S('create-sql-login-failed', 'A wrong login is refused in words.', 'New connection, server db1, user unravis_reader, password wrong, Test and list databases.', async () => { await pg.locator('select[data-bind="W.sql.saved"]').selectOption(''); await pg.locator('select[data-bind="W.sql.encrypt"]').selectOption('mandatory'); await fill(bind('W.sql.server'), 'db1'); await fill(bind('W.sql.user'), 'unravis_reader'); await fill(bind('W.sql.pass'), 'wrong'); await click('Test and list databases'); await pg.waitForFunction(() => /Login failed/.test(document.body.innerText), null, { timeout: 5000 }); });
    await S('create-sql-databases-listed', 'After a good Test: database checklist with Query Store chips; system databases hidden.', 'Correct password "secret", Test and list databases.', async () => { await fill(bind('W.sql.pass'), 'secret'); await click('Test and list databases'); await pg.waitForSelector('input[data-act="dbPick"][data-k="sql"]'); });
    await S('create-sql-databases-ticked', 'Two databases ticked; Add button counts them.', 'Tick orders and PayrollApp.', async () => { await pg.locator('input[data-act="dbPick"][data-k="sql"][data-n="orders"]').click(); await pg.locator('input[data-act="dbPick"][data-k="sql"][data-n="PayrollApp"]').click(); });
    await S('create-sql-added-grouped', 'One connection, two databases, added together and grouped in the sources panel.', 'Press Add 2 databases and keep details.', async () => { await click('Add 2 databases and keep details'); }, '.srcside');
    await S('create-sql-permission-script', 'The SQL permission script (per server and per database), with a version switch and Copy.', 'SQL Server tab, scroll to the permission script.', async () => { await tab(/SQL Server/); await pg.locator('text=GRANT VIEW SERVER').first().scrollIntoViewIfNeeded(); }, '.stage-inner');
    await S('create-files-method-empty', 'Import files method: folder or file location, Look here, and a drop zone.', 'Tab Import files.', async () => { await tab(/Import files/); });
    await S('create-files-drag-drop-file', 'A dropped file is recognised: suggested type and, where needed, a time-zone choice.', 'Drop (or choose in the drop zone) a file named HTTPERR1.log.', async () => { await pg.locator('#dz-file').setInputFiles({ name: 'HTTPERR1.log', mimeType: 'text/plain', buffer: Buffer.from('x') }); await wait(400); });
    await S('create-files-timezone-chosen', 'HTTPERR writes server local time, so the time zone must be chosen; Add 1 file is then available.', 'Choose a time zone (+04:00) for the dropped file.', async () => { await pg.locator('select[data-bind="W.file.items.0.tz"]').selectOption('+04:00'); });
    await S('create-files-added', 'The file lands in the sources panel.', 'Press Add 1 file.', async () => { await click('Add 1 file'); }, '.srcside');
    await S('create-files-by-folder', 'A folder location lists its files, each with a suggested type; unrecognised ones are left out; compressed ones are refused.', 'Tab Import files, type C:\\inetpub\\logs\\LogFiles\\W3SVC1, press Look here.', async () => { await tab(/Import files/); await fill(bind('W.file.path'), 'C:\\inetpub\\logs\\LogFiles\\W3SVC1'); await click('Look here'); });
    await S('create-files-folder-adjusted', 'The user switches one suggestion off and gives an unrecognised file a type and a time zone.', 'Change the type and time-zone selects in the list.', async () => { await pg.locator('select[data-bind="W.file.items.1.choice"]').selectOption(''); await pg.locator('select[data-bind="W.file.items.4.choice"]').selectOption('win-events'); await pg.locator('select[data-bind="W.file.items.2.tz"]').selectOption('+04:00'); await pg.locator('select[data-bind="W.file.items.4.tz"]').selectOption('UTC'); });
    await S('create-files-folder-added', 'The chosen files added in one go; the rest are reported as left out.', 'Press Add N files.', async () => { await clickRe(/^Add \d+ files?$/); });
    await S('create-files-by-one-file', 'A single file location selects just that file.', 'Type C:\\inetpub\\logs\\LogFiles\\W3SVC1\\u_ex261020.log, Look here.', async () => { await tab(/Import files/); await fill(bind('W.file.path'), 'C:\\inetpub\\logs\\LogFiles\\W3SVC1\\u_ex261020.log'); await click('Look here'); });
    await S('create-files-relative-path-refused', 'A relative path is refused with an example.', 'Type logs\\x, Look here.', async () => { await fill(bind('W.file.path'), 'logs\\x'); await click('Look here'); });
    await S('create-chosen-sources-summary', 'The chosen-sources panel as a short collapsed summary.', 'After adding sources, look at the right-hand panel.', null, '.srcside');
    await S('create-chosen-sources-edit-all', 'Edit all opens every source with its environment pills, portal and API choices and Remove.', 'Press Edit all.', async () => { await click('Edit all'); }, '.srcside');
    await S('create-chosen-sources-env-pills', 'Environment is free text: several pills per source, with suggestions from earlier names.', 'In the first source, type "Client A Staging", Enter, "Client B Live", Enter.', async () => { const i = pg.locator('input[data-pill="W.sources.0.envs"]').first(); await i.fill('Client A Staging'); await i.press('Enter'); await i.fill('Client B Live'); await i.press('Enter'); await i.fill('Cl'); }, '.srcside');
    await S('create-chosen-sources-collapsed-again', 'Collapse all returns to the summary.', 'Press Collapse all.', async () => { await click('Collapse all'); }, '.srcside');
    await S('create-review-step', 'Step 3 of 3: Review lists what will be made, with the admin notice and how far back.', 'Press Next.', async () => { await click('Next'); await pg.waitForSelector('text=Check what will be made'); });
    await S('create-review-admin-notice-ticked', 'Review: some sources need administrator rights; the notice box must be ticked before Create case is on (until then the page says Tick the box above to confirm the notice).', 'Tick the administrator notice box.', async () => { await pg.locator('#main input[type=checkbox]').first().check(); await wait(200); });
    await S('create-leave-guide-asks', 'Leaving a draft with entries asks first: Keep working or Leave and discard.', 'Press Leave the guide.', async () => { await click('← Leave the guide'); });
    await S('create-creating-progress', 'Creation progress: the case, environments and each source read in turn.', 'Press Keep working, then Create case (the screen is brief; captured as soon as it appears).', async () => { await click('Keep working'); await pg.locator('#main input[type=checkbox]').first().check(); await btn('Create case').click(); await wait(120); });
    await S('create-failed-source-open-anyway', 'One source could not be read; the case was kept; Open the case anyway.', 'Wait for the progress to end.', async () => { await pg.waitForFunction(() => document.body.innerText.includes('could not be read'), null, { timeout: 9000 }); });
    await S('create-opened-with-retry-list', 'The new case home with Needs another try (the failed source).', 'Press Open the case anyway.', async () => { await click('Open the case anyway'); await pg.waitForFunction(() => RT.screen === 'case'); });
    await S('create-retry-fails-once', 'Try again fails the first time and stays listed (simulated).', 'Press Try again.', async () => { await click('Try again'); }, blk('h-retries'));
    await S('create-retry-succeeds', 'Second Try again succeeds; the list is gone.', 'Press Try again again.', async () => { await click('Try again').catch(() => {}); });
    await sc('create', 'w-resumed');
    await S('create-resume-draft-banner', 'A left-behind draft is offered again, with Start over.', 'Scenario w-resumed (Resume an earlier draft).');
    await sc('create', 'w-empty');
    await S('create-review-empty-case', 'Review with a name and no sources: the button says Create empty case.', 'Scenario w-empty.');
    await sc('create', 'w-review');
    await S('create-review-one-source-will-fail', 'Review where one source will fail to read.', 'Scenario w-review.');
  }

  /* ================= 5. open an existing case + case home ================= */
  if (want('5')) {
    await fresh();
    await S('open-case-from-list', 'The list row to press: Open case.', 'Start screen, left card.', null, blk('card-existing'));
    await S('case-home-full', 'Case home, whole page: header, Analysis, Investigation Timeline, Databases, Story so far, Theories, Health checks, Saved connections, Files, Case key, Erase.', 'Press Open case on Payroll.Run slowdown, 20 Oct.', async () => { await openCaseFromList('Payroll.Run slowdown, 20 Oct'); });
    await S('case-home-header', 'The case header: title, counts, All cases and Archive this case.', 'Case home, top.', null, '#ch-title');
    await S('case-block-analysis', 'Analysis block: Open the analysis.', 'Case home.', null, blk('h-analysis'));
    await S('case-block-timeline', 'Investigation Timeline block: Open the Investigation Timeline.', 'Case home.', null, blk('h-trace'));
    await S('case-block-databases', 'Databases block: grouped by connection, with environment and history chip.', 'Case home.', null, blk('h-dbs'));
    await S('case-block-story', 'Story so far.', 'Case home.', null, blk('h-story'));
    await S('case-block-theories', 'Theories block.', 'Case home.', null, blk('h-theories'));
    await S('case-block-health', 'Health checks block.', 'Case home.', null, blk('h-health'));
    await S('case-health-checks-run', 'Health checks after Run checks, with history.', 'Press Run checks.', async () => { await click('Run checks'); await wait(600); }, blk('h-health'));
    await S('case-block-saved-connections', 'Saved connections: SQL Server and MongoDB, with Test, Edit, Forget, + SQL Server, + MongoDB, Refresh.', 'Case home.', null, blk('h-hub'));
    await S('case-saved-connection-test', 'Testing a saved connection.', 'Press Test on the first connection.', async () => { await click('Test'); await wait(800); }, blk('h-hub'));
    await S('case-saved-connection-forget-asks', 'Forgetting a saved connection asks first.', 'Press Forget.', async () => { await click('Forget'); }, blk('h-hub'));
    await A('cancel forget', async () => { const c = pg.getByRole('button', { name: /^(Cancel|Keep)/ }).first(); if (await c.count()) await c.click(); });
    await S('case-block-files', 'Files block: what is in the case and Add a file.', 'Case home.', null, blk('h-files'));
    await S('case-block-case-key', 'Case key block: Add a case key.', 'Case home.', null, blk('h-key'));
    await S('case-block-erase', 'Erase this case block.', 'Case home.', null, blk('h-erase'));
    // theories and connections drawers
    await S('theories-ledger-drawer', 'The theories (ledger) drawer.', 'Press Open the theories.', async () => { await click('Open the theories'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await A('theory add', async () => { const t = pg.locator('.drawer textarea, .drawer input[type=text]').first(); await t.fill('Blocking after the 09:14 deploy'); await pg.getByRole('button', { name: /^Add( a)? theory|^Add$/ }).first().click(); });
    await S('theories-after-add', 'A theory added, with its state.', 'Type a theory, press Add.', null, '.drawer');
    await A('close', async () => { await pg.getByRole('button', { name: /^Close/ }).first().click(); });
    await S('connections-drawer', 'Adding a saved connection from the case home (+ SQL Server).', 'Press + SQL Server.', async () => { await click('+ SQL Server'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await A('close', async () => { await pg.getByRole('button', { name: /^Close/ }).first().click(); });
    // case key page and erase
    await S('case-key-add-page', 'Add a case key: the form.', 'Press Add a case key.', async () => { await click('Add a case key'); });
    await A('back', async () => { await pg.evaluate(() => ACT.chBackHome && ACT.chBackHome()); });
    await S('erase-case-page', 'Erase this case: you must type the case code.', 'Press Erase this case.', async () => { await click('Erase this case'); });
    // states
    await sc('case', 'c-failed');
    await S('case-needs-another-try', 'A failed import and an unsaved source name on the Needs another try list.', 'Scenario c-failed.', null, blk('h-retries'));
    await S('case-retry-add-file-again', 'Add the file again opens the import drawer.', 'Press Add the file again.', async () => { await click('Add the file again'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await sc('case', 'c-closed');
    await S('case-closed', 'A closed case: read-only, the Timeline and add buttons are off with the reason; Reopen this case.', 'Scenario c-closed.');
    await S('case-reopened', 'Reopen this case: everything is on again.', 'Press Reopen this case.', async () => { await click('Reopen this case'); });
    await sc('case', 'c-keyed');
    await S('case-key-card', 'A case with its own key: the key card replaces the page.', 'Scenario c-keyed.');
    await S('case-key-wrong', 'A wrong case key.', 'Type a wrong key, press Open case.', async () => { await fill('#ck-open', 'wrong-wrong-wrong'); await click('Open case'); });
    await S('case-key-throttled', 'Three wrong tries: wait.', 'Repeat twice more.', async () => { await fill('#ck-open', 'wrong-2'); await click('Open case'); await fill('#ck-open', 'wrong-3'); await click('Open case'); });
    await S('case-key-right', 'Right key (vendor-case-key-01, test aid): the case opens.', 'Wait, type the key, press Open case.', async () => { await wait(5600); await fill('#ck-open', 'vendor-case-key-01'); await click('Open case'); await pg.waitForSelector('#ch-title'); });
    await sc('case', 'c-archived');
    await S('case-archived', 'Archived case: only the record, the case file and the restore choices.', 'Scenario c-archived.');
    await sc('case', 'c-untrusted');
    await S('case-legacy', 'Legacy case that cannot be opened: Restore from a case file or Erase.', 'Scenario c-untrusted.');
    await S('case-legacy-restore-intent', 'Restore from a case file takes you to the start screen with the restore card open.', 'Press Restore from a case file.', async () => { await click('Restore from a case file'); await wait(400); });
    await sc('case', 'c-loading');
    await S('case-loading', 'Opening the case...', 'Scenario c-loading.');
    await sc('case', 'c-loadfail');
    await S('case-load-failed', 'The case could not be opened, with Try again.', 'Scenario c-loadfail.');
    await sc('case', 'c-gone');
    await S('case-gone', 'The case could not be found; it may have been erased.', 'Scenario c-gone.');
    await sc('case', 'c-changed');
    await S('case-changed-in-another-tab', 'Page is out of date; changes are off until Reload.', 'Scenario c-changed.');
    // missing working copy and erased line
    await fresh();
    await S('start-missing-working-copy-row', 'A case whose working copy is missing, with Erase.', 'Start screen list (row: Case whose files were moved).', null, blk('card-existing'));
    // archive / restore drawers
    await sc('case', 'c-ready');
    await S('archive-drawer', 'Archive this case: folder, sealed-with choice, passphrase, Archive the case.', 'Press Archive this case.', async () => { await click('Archive this case'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await S('archive-progress', 'Archiving: folder and passphrase filled in, Archive the case pressed; the screen shows progress.', 'Fill the folder and passphrase, press Archive the case.', async () => { const ins = pg.locator('.drawer input'); const k = await ins.count(); for (let i = 0; i < k; i++) { const t = await ins.nth(i).getAttribute('type'); if (t === 'password') await ins.nth(i).fill('archive passphrase 1'); else if (!t || t === 'text') await ins.nth(i).fill('D:\\Archives'); } await click('Archive the case'); await wait(300); });
    await S('archive-result-case-home', 'The case after archiving: read-only, with Restore, Restore as a new case, Free the working copy.', 'After the progress ends.', async () => { await wait(2500); });
    await sc('case', 'c-archived');
    await S('archive-restore-drawer', 'Restore: the case goes back to open exactly as archived.', 'Press Restore on an archived case.', async () => { await click('Restore'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await S('archive-restored-case-home', 'After Restore this case: the case is open again.', 'Press Restore this case.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Restore this case', exact: true }).click(); await wait(1500); });
    await sc('case', 'c-archived');
    await S('archive-restore-as-new-drawer', 'Restore as a new case: a second case is made from the case file; this one stays as it is.', 'Press Restore as a new case.', async () => { await click('Restore as a new case'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await S('archive-restore-as-new-done', 'A line says Restored as a new case with a new code; this archived case is left as it was.', 'Press Restore as a new case in the drawer.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Restore as a new case', exact: true }).click(); await wait(1200); });
    await sc('case', 'c-archived');
    await S('archive-free-working-copy-drawer', 'Free the working copy: gives back disk space; the case file stays the only copy.', 'Press Free the working copy.', async () => { await click('Free the working copy'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await S('archive-free-working-copy-done', 'After freeing: the case is archived with no working copy.', 'Press Free the working copy in the drawer.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Free the working copy', exact: true }).click(); await wait(1200); });
    // add a file to an existing case
    await sc('case', 'c-ready');
    await S('import-drawer-open', 'Import files drawer on an existing case: Choose a file, host list, Preview, and the history of imports.', 'Press Add a file in the Files block.', async () => { await click('Add a file'); await pg.waitForSelector('.drawer'); }, '.drawer');
    const dsel = () => pg.locator('.drawer select').first();
    await S('import-file-chosen', 'A file chosen: Unravis says what it looks like and asks which host it came from.', 'Choose u_ex261020_siteB.log in Choose a file.', async () => { await dsel().selectOption({ label: 'u_ex261020_siteB.log' }); }, '.drawer');
    await S('import-preview-no-timezone-rule', 'Preview: rows read, kept and rejected, time covered. This file has no time-zone rule, so the timeline cannot line it up.', 'Press Preview.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Preview', exact: true }).click(); await wait(700); }, '.drawer');
    await S('import-file-imported', 'After Import file: the file is added to the history table.', 'Press Import file.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Import file', exact: true }).click(); await wait(1000); }, '.drawer');
    await S('import-sql-error-log-preview', 'A SQL Server error log previewed with a validated UTC time-zone rule.', 'Choose sql_errorlog_2026-10-20.txt, press Preview.', async () => { await dsel().selectOption({ label: 'sql_errorlog_2026-10-20.txt' }); await pg.locator('.drawer').getByRole('button', { name: 'Preview', exact: true }).click(); await wait(700); }, '.drawer');
    await S('import-duplicate-file', 'A copy of a file already imported is refused as a duplicate.', 'Choose u_ex261020_copy.log, Preview, Import file.', async () => { await dsel().selectOption({ label: 'u_ex261020_copy.log' }); await pg.locator('.drawer').getByRole('button', { name: 'Preview', exact: true }).click(); await wait(500); await pg.locator('.drawer').getByRole('button', { name: 'Import file', exact: true }).click(); await wait(900); }, '.drawer');
    await S('import-empty-file', 'An empty file is refused.', 'Choose empty.log.', async () => { await dsel().selectOption({ label: 'empty.log' }); await wait(300); }, '.drawer');
    await S('import-too-big-file', 'A file over the size limit is refused with its size and the limit.', 'Choose huge_export_2026.log.', async () => { await dsel().selectOption({ label: 'huge_export_2026.log' }); await wait(300); }, '.drawer');
    await S('import-compressed-file', 'A compressed file is refused: extract it first.', 'Choose logs-october.zip.', async () => { await dsel().selectOption({ label: 'logs-october.zip' }); await wait(300); }, '.drawer');
    await S('import-close-case-button', 'Close case lives in this drawer: a closed case is erased 30 days after it was closed.', 'Press Close case in the Import drawer.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Close case', exact: true }).click(); await wait(800); });
    await sc('case', 'c-ready');
    await S('header-import-and-ledger', 'The case header buttons: Home, Ledger, Import.', 'Case home, top bar.', null, '.topbar');
  }
  /* ================= 6. analysis and export ================= */
  if (want('6')) {
    await fresh();
    await A('open case', async () => { await openCaseFromList('Payroll.Run slowdown, 20 Oct'); });
    await S('analysis-open-from-case-home', 'The analysis as it opens from the case home: last hour against the equal period right before it.', 'Case home > Open the analysis.', async () => { await click('Open the analysis'); await pg.waitForSelector('.scope'); await wait(500); });
    await S('analysis-scope-bar', 'The scope bar: quick ranges, Type, More filters and Run analysis.', 'Analysis, top.', null, '.scope');
    await S('analysis-more-filters-when', 'More filters opened: Investigate From/To and Reference From/To with the time zone and UTC echo.', 'Press More filters.', async () => { await clickRe(/^More filters/); await wait(300); }, '.scope');
    await S('analysis-quick-range-4-hours', 'A quick range chip re-runs the analysis at once.', 'Press Last 4 hours.', async () => { await click('Last 4 hours'); await wait(500); });
    await sc('running', 'a-slow');
    await S('analysis-slowdown-pulse', 'Pulse for the slowdown against a good earlier hour: five figures with change against the reference, headline, chart.', 'Scenario a-slow.');
    await S('analysis-kind-filter-async', 'Type filter: only one kind of request.', 'Press Async in the scope bar.', async () => { await click('Async'); await wait(500); }, '.scope');
    await sc('running', 'a-overlap');
    await S('analysis-reference-overlap-blocked', 'The analysis refuses a comparison period that overlaps the investigated period; Run analysis is off and the hint says why.', 'Scenario a-overlap.', null, '.scope');
    await sc('running', 'a-long');
    await S('analysis-window-over-7-days', 'Each window can be at most 7 days here (the timeline allows 30).', 'Scenario a-long.', null, '.scope');
    await sc('running', 'a-sparse');
    await S('analysis-reference-no-data', 'The comparison period has no data: no deltas, ratios or headline.', 'Scenario a-sparse.');
    await sc('running', 'a-partial');
    await S('analysis-reference-half-covered', 'The comparison period is only partly covered: deltas stay off, the note says why.', 'Scenario a-partial.');
    await sc('running', 'a-empty');
    await S('analysis-no-data-in-period', 'No requests in the investigated period.', 'Scenario a-empty.');
    await sc('running', 'a-srcfail');
    await S('analysis-source-failed-banner', 'A banner names the failing source; the figures use the others.', 'Scenario a-srcfail.');
    await S('analysis-sources-popover', 'The sources button opens the list of sources used.', 'Press the sources button (the dot and the number of sources) in the sub-bar.', async () => { await clickRe(/^● \d+ sources?/); await wait(400); });
    await sc('running', 'a-nocase');
    await S('analysis-without-a-case', 'Analysis of the demo audit source with no case behind it: no Case home button and no timeline.', 'Start screen > Go to the analysis (scenario a-nocase).');
    await sc('running', 'a-slow');
    await S('where-is-it-view', 'Where is it?: operations ranked by lens, with comparison to the earlier period.', 'Analysis > Where is it?', async () => { await clickRe(/^Where is it\?/); await pg.waitForSelector('#where-h'); });
    await S('where-is-it-biggest-change', 'Ranking by Biggest change.', 'Press Biggest change.', async () => { await click('Biggest change'); await wait(400); });
    await S('evidence-rows-for-operation', 'Evidence: slowest requests for one operation, with Export CSV, Inspect and Nearby.', 'Press the Payroll.Run row.', async () => { await pg.locator('button.rankrow', { hasText: 'Payroll.Run' }).first().click(); await pg.waitForSelector('text=Slowest requests'); });
    await S('record-detail', 'Record detail: stored fields and fields derived by Unravis.', 'Press Inspect record on a row.', async () => { await pg.getByRole('button', { name: /^Inspect record/ }).first().click(); await pg.waitForSelector('#rec-h'); });
    await S('nearby-activity', 'Nearby activity: one call beside what SQL, Mongo and IIS did. Call trace is not wired (the screen says so).', 'Back to the evidence, press Nearby.', async () => { await pg.getByRole('button', { name: '← Back to the evidence' }).click(); await pg.getByRole('button', { name: 'Nearby' }).first().click(); await wait(400); });
    await S('analysis-breadcrumb-back', 'The breadcrumb and Back return up one level.', 'Press Pulse in the breadcrumb.', async () => { await click('Pulse'); await wait(400); });
    // drawers from the top bar
    await S('drawer-patterns', 'Patterns drawer (three canned entries in the prototype).', 'Press Patterns in the top bar.', async () => { await click('Patterns'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await A('close', async () => { await pg.getByRole('button', { name: /^Close/ }).first().click(); });
    await S('drawer-ledger', 'Ledger drawer from the top bar (theories).', 'Press Ledger in the top bar.', async () => { await clickRe(/^Ledger/); await pg.waitForSelector('.drawer'); }, '.drawer');
    await A('close', async () => { await pg.getByRole('button', { name: /^Close/ }).first().click(); });
    await S('drawer-glossary', 'Glossary from the header.', 'Press Glossary.', async () => { await click('Glossary'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await A('close', async () => { await pg.getByRole('button', { name: /^Close/ }).first().click(); });
    await S('drawer-blackbox', 'Blackbox from the header (stub in the prototype).', 'Press Blackbox.', async () => { await click('Blackbox'); await pg.waitForSelector('.drawer'); }, '.drawer');
    await A('close', async () => { await pg.getByRole('button', { name: /^Close/ }).first().click(); });
    // export
    await sc('running', 'x-ready');
    await S('export-drawer-ready', 'Export drawer: window, options, scrub on by default, Preview.', 'Analysis > Export.', null, '.drawer');
    await S('export-preview-scrub-on', 'After Preview: what will be left out (client IP, referer, cookie hash) and the Save the export button.', 'Press Preview.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Preview', exact: true }).click(); await wait(500); }, '.drawer');
    await S('export-saved', 'After Save the export: Saved.', 'Press Save the export.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Save the export', exact: true }).click(); await wait(600); }, '.drawer');
    await sc('running', 'x-changed');
    await S('export-changed-409', 'The case changed after the preview: Save the export gives 409 and a new preview is needed.', 'Scenario x-changed, press Save the export.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Save the export', exact: true }).click(); await wait(500); }, '.drawer');
    await S('export-preview-again', 'Preview again after a 409.', 'Press Preview again.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Preview again' }).click(); await wait(500); }, '.drawer');
    await sc('running', 'x-scrub');
    await S('export-scrub-switched-409', 'Scrub switched after the preview also gives 409.', 'Scenario x-scrub, press Save the export.', async () => { await pg.locator('.drawer').getByRole('button', { name: 'Save the export', exact: true }).click(); await wait(500); }, '.drawer');
  }
  /* ================= 7. Investigation Timeline (1920 x 1080) ================= */
  if (want('7')) {
    const trWait = async () => { await pg.waitForSelector('#ov', { timeout: 9000 }); await pg.waitForFunction(() => { const r = document.getElementById('sus-rail'); return !r || r.dataset.state === 'done'; }, null, { timeout: 9000 }).catch(() => {}); await wait(300); };
    const showTl = async () => { if (await pg.evaluate(() => !U.tr.result || U.tr.key !== trKey(curCase()))) await pg.getByRole('button', { name: /^(Show|Refresh) timeline$/ }).first().click(); await trWait(); };
    const tsc = async id => { await sc('tracing', id); await trWait().catch(() => {}); };
    const setStyle = async on => pg.evaluate(o => { let st = document.querySelector('style[data-hide]'); if (o && !st) { st = document.createElement('style'); st.setAttribute('data-hide', '1'); st.textContent = '.dock,.pdrawer,.scen{display:none !important} .topbar{position:static !important}'; document.head.appendChild(st); } if (!o && st) st.remove(); }, on);
    const cards = { ctl: 'section.trctl', figures: '.figures.auto', board: '.chartcard', rail: 'aside.trrail', bucket: '.trbot > .card', attr: '#attr-d', det: '.detrow', see: 'section.card:has(.seegrid)' };
    const openAttr = () => pg.evaluate(() => { RT.attrOpen = true; const d = document.getElementById('attr-d'); if (d) d.open = true; });
    const refresh = async () => { await pg.getByRole('button', { name: 'Refresh timeline', exact: true }).first().click(); await trWait(); };
    const whatif = k => pg.evaluate(k2 => { const b = document.querySelector('[data-act=whatif][data-k="' + k2 + '"]'); if (b) b.click(); }, k);
    const seeAll = async () => { await pg.getByRole('button', { name: /^See all \(/ }).click().catch(() => {}); await wait(300); };
    await fresh(1920, 1080);
    await A('open case', async () => { await openCaseFromList('Payroll.Run slowdown, 20 Oct'); });
    await S('timeline-opens-by-itself-loading', 'The timeline opens by itself: the chart is drawn and the Suspects rail starts checking one suspect at a time.', 'Case home > Open the Investigation Timeline (captured a moment after the click).', async () => { await click('Open the Investigation Timeline'); await pg.waitForSelector('#ov', { timeout: 9000 }); await wait(500); }, 'view');
    await S('timeline-default-last-hour-full', 'The whole screen with the default window: last hour against the hour before, all three lanes, the rail finished.', 'Wait a few seconds after opening; nothing else pressed.', async () => { await trWait(); });
    await S('timeline-controls-card', 'The controls card: quick ranges, Investigation window, Compare it with, Sources chips, Add evidence, Refresh timeline.', 'Timeline, top.', null, cards.ctl);
    await S('timeline-quick-range-4-hours', 'A quick range chip sets the window to that period ending now and the comparison period to the equal period right before.', 'Press Last 4 hours.', async () => { await click('Last 4 hours'); await wait(600); await showTl(); }, cards.ctl);
    await S('timeline-picker-investigation-window', 'The Investigation window picker: quick ranges, calendar, start and end times in the chosen zone, Apply and Cancel.', 'Press the Investigation window button.', async () => { await pg.click('#pk-inc'); await pg.waitForSelector('.pk'); }, '.pk');
    await S('timeline-picker-applied', 'After choosing "The slowdown in the demo data": the window reads 09:00 to 12:00 UTC.', 'In the picker press The slowdown in the demo data.', async () => { await pg.getByRole('button', { name: 'The slowdown in the demo data' }).click(); await pg.waitForFunction(() => !document.querySelector('.pk')); await wait(300); }, cards.ctl);
    await S('timeline-picker-comparison-period', 'The Compare it with picker: The equal period right before the incident, Same time yesterday.', 'Press the comparison period button.', async () => { await pg.click('#pk-ref'); await pg.waitForSelector('.pk'); }, '.pk');
    await A('close picker', async () => { await pg.mouse.click(5, 300); await wait(200); });
    await S('timeline-picker-closed-by-outside-click', 'A click outside the picker closes it without changing anything.', 'Click anywhere outside the picker.', null, cards.ctl);
    await S('timeline-sources-chip-off', 'Sources chips: switching IIS off leaves two lanes to draw.', 'Press the IIS chip, then Refresh timeline.', async () => { await pg.getByRole('button', { name: /^IIS ·/ }).click(); await showTl(); }, cards.ctl);
    await A('chip on', async () => { await pg.getByRole('button', { name: /^IIS ·/ }).click(); await showTl(); });
    await S('timeline-refresh-timeline', 'Refresh timeline re-reads and redraws after the windows or sources changed.', 'Press Refresh timeline.', async () => { await refresh(); });
    await tsc('t-slow');
    await S('timeline-slowdown-full', 'The slowdown with a real comparison (09:00 to 11:30 against the 2.5 hours before): the whole screen.', 'Scenario t-slow (The slowdown, with a real comparison).');
    await S('timeline-summary-line-and-mixed-note', 'The summary line (bar size, bars on the axis, window) and the mixed-time note that cannot be dismissed.', 'Timeline, under the controls.', null, '.trtop > .dim');
    await S('timeline-figure-cards', 'The seven overall figure cards: requests, P95, 5xx, IIS 5xx, IIS P95, slow SQL calls, errors and blocking.', 'Timeline, above the charts.', null, cards.figures);
    await S('timeline-what-unravis-sees', 'The reading aid: Application, Web server, Database tiles in plain words.', 'Timeline, under the chart.', null, cards.see);
    await S('timeline-board-three-lanes', 'The trace board: three lanes on one shared time axis with titled panels and legends.', 'Timeline, the big chart card.', null, cards.board);
    await S('timeline-hover-bucket', 'Hovering a bar shows one line per lane for that moment.', 'Move the pointer over the chart.', async () => { await pg.evaluate(() => document.getElementById('ov').scrollIntoView({ block: 'center' })); const b = await pg.locator('#ov').boundingBox(); await pg.mouse.move(b.x + b.width * 0.62, b.y + 80); await wait(300); }, 'view');
    await S('timeline-select-bucket', 'Clicking a bar selects that bucket across all lanes and fills the bucket panel.', 'Click a bar.', async () => { const b = await pg.locator('#ov').boundingBox(); await pg.mouse.click(b.x + b.width * 0.62, b.y + 80); await wait(300); }, 'view');
    await S('timeline-bucket-panel', 'The bucket panel: the chosen time, what each lane saw, and Open Run analysis for this bucket.', 'After selecting a bar.', null, cards.bucket);
    await S('timeline-keyboard-arrow', 'Left and Right arrow keys move the selection; Home and End jump to the ends.', 'Press the Right arrow key.', async () => { await pg.keyboard.press('ArrowRight'); await wait(200); }, cards.bucket);
    await S('timeline-handoff-to-analysis', 'Open Run analysis for this bucket: the analysis opens with a strip saying it came from the Investigation Timeline and shows both windows.', 'Press Open Run analysis for this bucket.', async () => { await click('Open Run analysis for this bucket'); await pg.waitForFunction(() => RT.screen === 'running'); await wait(500); });
    await S('timeline-handoff-back', 'Back to the timeline keeps both windows and the selection.', 'Press Back to the timeline.', async () => { await click('← Back to the timeline'); await pg.waitForFunction(() => RT.screen === 'tracing'); await trWait(); });
    await S('timeline-details-sections', 'Three folded sections: What each lane shows, How this axis was worked out, Timeline details; plus Words used on this screen.', 'Scroll to the bottom of the timeline.', null, cards.det);
    await S('timeline-details-axis-open', 'How this axis was worked out, opened: start and end you chose, axis start and end, bucket size, raw scans.', 'Open How this axis was worked out.', async () => { await pg.locator('details.card', { hasText: 'How this axis was worked out' }).first().evaluate(d => { d.open = true; }); }, cards.det);
    await S('timeline-layout-a-report', 'Layout A (Report): one column, top to bottom.', 'Prototype controls (bottom bar): layout pill, choose A.', async () => { await pg.evaluate(() => { RT.variant = 'A'; render(); }); await trWait(); });
    await S('timeline-layout-b-workbench', 'Layout B (Workbench): windows and sources in a side panel, figures and a taller chart on the right.', 'Prototype controls: layout pill, choose B.', async () => { await pg.evaluate(() => { RT.variant = 'B'; render(); }); await wait(500); });
    await S('timeline-layout-c-lane-ledger', 'Layout C (Lane ledger): one row per lane with trust stickers, event count and a sparkline; open a lane for its panels.', 'Prototype controls: layout pill, choose C.', async () => { await pg.evaluate(() => { RT.variant = 'C'; render(); }); await wait(500); });
    await A('layout A', async () => { await pg.evaluate(() => { RT.variant = 'A'; render(); }); await trWait(); });
    await S('suspects-rail-top-five', 'The Suspects rail: legend, summary line, and the top five suspects (red first, then orange, then grey).', 'Timeline, right side.', null, cards.rail);
    await S('suspects-rail-see-all', 'See all expands in place: every suspect, red then orange then grey, green ones folded as Ruled out by the data.', 'Press See all.', async () => { await seeAll(); }, cards.rail);
    await S('suspects-show-on-chart', 'Show on chart selects the bucket where that suspect first moved.', 'Press Show on chart on the red suspect.', async () => { await pg.evaluate(() => document.querySelector('#sus-rail [data-act=susShow]').click()); await wait(400); }, 'view');
    await S('suspects-ruled-out-folded', 'Green suspects are folded away under Ruled out by the data.', 'Open the Ruled out by the data fold at the bottom of the list.', async () => { await pg.evaluate(() => { const d = [...document.querySelectorAll('#sus-rail details')].pop(); if (d) { d.open = true; d.scrollIntoView({ block: 'center' }); } }); await wait(300); }, cards.rail);
    await S('suspects-show-fewer', 'Show fewer returns to the top five.', 'Press Show fewer.', async () => { await pg.getByRole('button', { name: 'Show fewer' }).click(); await wait(300); }, cards.rail);
    await S('suspects-card-open-evidence', 'One suspect opened: evidence rows (signal, Must or Expect, yes, no or unknown, why), the cheapest check and what would overturn it.', 'Open the evidence on a suspect card.', async () => { await pg.evaluate(() => { const a = document.querySelector('#sus-rail article'); const d = a && a.querySelector('details'); if (d) d.open = true; }); await wait(200); }, '#sus-rail article');
    await S('evidence-card-folded', 'The Where does the evidence point card, folded: title and strength chip only.', 'Scroll below the chart.', async () => { await pg.evaluate(() => { RT.attrOpen = false; const d = document.getElementById('attr-d'); if (d) d.open = false; }); }, cards.attr);
    await S('evidence-card-open-one-environment', 'Opened: The problem appears isolated to Production, with the server block and the by-database table.', 'Open the folded card.', async () => { await openAttr(); await wait(300); }, cards.attr);
    await S('evidence-scope-all-databases', 'Scope switch: All databases on this server, with an unnamed Other databases row.', 'Press All databases on this server.', async () => { await click('All databases on this server'); }, cards.attr);
    await S('evidence-scope-show-names', 'Show their names reveals the unpicked databases, with a warning to hide them before sharing an export.', 'Press Show their names.', async () => { await click('Show their names'); }, cards.attr);
    await S('evidence-scope-server-only', 'Scope switch: Server only.', 'Press Server only.', async () => { await click('Hide their names').catch(() => {}); await click('Server only'); }, cards.attr);
    for (const [id, slug, what] of [['a-server', 'whole-server', 'The whole database server appears affected (every picked database slowed, disk saturated).'], ['a-noisy', 'noisy-neighbour', 'Another database may be affecting yours (unpicked databases take most of the disk).'], ['a-blind', 'not-enough-evidence', 'There is not enough evidence to narrow it down (Query Store off, no samples).'], ['a-quiet', 'quiet-window', 'No clear performance problem in this period.']]) {
      await S('evidence-verdict-' + slug, what, 'Scenario ' + id + ' (Investigation Timeline: where does the evidence point).', async () => { await tsc(id); await openAttr(); await wait(300); }, cards.attr);
    }
    await S('timeline-no-history-full', 'No history (nothing kept): most suspects are grey with a Needs line, nothing is red.', 'Scenario a-blind: the whole screen.', async () => { await tsc('a-blind'); });
    await S('suspects-rail-no-history', 'Suspects rail in the no-history situation.', 'Scenario a-blind.', null, cards.rail);
    await S('timeline-one-environment-full', 'One environment: D1 blocking chain is the only red suspect.', 'Scenario a-env.', async () => { await tsc('a-env'); });
    await tsc('t-all');
    await S('timeline-seven-days-gaps', 'Last 7 days: 2-hour bars, SQL and IIS say there are no logs for most of the window (hatched, with exact range), the audit outage is a thin gap.', 'Press Last 7 days.', async () => { await click('Last 7 days'); await wait(500); await showTl(); });
    await S('timeline-seven-days-board', 'Close-up of the seven-day board with the gap labels.', 'Same screen, chart card.', null, cards.board);
    await S('timeline-seven-days-show-all-panels', 'A lane with logs for under 15% of the window folds to one short panel; Show all panels expands it.', 'Press Show all panels on a folded lane.', async () => { await pg.locator('#tc button[data-act=expandLane]').first().click(); await wait(400); }, cards.board);
    await S('timeline-seven-days-evidence-card', 'Evidence card in the seven-day situation.', 'Same screen, open the evidence card.', async () => { await openAttr(); await wait(300); }, cards.attr);
    await tsc('w-overlap');
    await S('timeline-overlap-notice', 'The two windows overlap: a non-blocking notice. The timeline still runs.', 'Scenario w-overlap.', null, cards.ctl);
    await S('timeline-overlap-fix', 'Move it to just before the investigation window fixes the overlap in one press.', 'Press Move it to just before the investigation window.', async () => { await click('Move it to just before the investigation window'); await wait(500); }, cards.ctl);
    await sc('tracing', 'w-30dx');
    await S('timeline-30-day-limit-refused', 'A window of 30 days and 1 second is refused; Show timeline is off.', 'Scenario w-30dx.', null, cards.ctl);
    await tsc('w-30d');
    await S('timeline-30-days-8-hour-bars', '30 days starting at 00:01: bars coarsen to 8 hours, 91 on the axis, two boundary raw scans.', 'Scenario w-30d.');
    await tsc('w-sparse');
    await S('timeline-no-comparison-sparse', 'The comparison period has no data: cards say Cannot compare with the earlier period.', 'Scenario w-sparse.', null, cards.figures);
    await S('timeline-sparse-lanes', 'Each lane says No comparison with the reason.', 'Same screen, chart card.', null, cards.board);
    await sc('tracing', 't-closed');
    await S('timeline-closed-case', 'A closed case: Show timeline is off with the reason; nothing is drawn.', 'Scenario t-closed.');
    await sc('tracing', 't-tz');
    await S('timeline-timezone-gate', 'A file with no checked time zone switches the multi-source chart off as a whole.', 'Scenario t-tz, Show timeline.', async () => { await showTl().catch(() => {}); await wait(300); });
    await sc('tracing', 't-empty');
    await S('timeline-nothing-stored', 'A case with nothing stored: SQL and IIS say this case has no file for this source.', 'Scenario t-empty, Show timeline.', async () => { await showTl().catch(() => {}); await wait(300); });
    await sc('tracing', 't-partial');
    await S('timeline-half-stored', 'Only half the window is stored for SQL: a gap is labelled on that lane.', 'Scenario t-partial, Show timeline.', async () => { await showTl().catch(() => {}); await wait(300); });
    await sc('tracing', 't-saved');
    await S('timeline-saved-lanes-only', 'Saved lanes only (audit removed): nothing is a live read, so no mixed-time note.', 'Scenario t-saved, Show timeline.', async () => { await showTl().catch(() => {}); await wait(300); });
    await tsc('a-env');
    await S('whatif-audit-over-budget', 'What if: audit read exceeds its budget: the lane is flagged incomplete and nothing is shortened.', 'Prototype controls > What if > Audit read exceeds its budget, then Refresh timeline.', async () => { await whatif('auditOver'); await refresh(); }, cards.board);
    await A('off', async () => { await whatif('auditOver'); });
    await S('whatif-audit-unreachable', 'What if: audit source unreachable.', 'Prototype controls > What if > Audit source unreachable, then Refresh timeline.', async () => { await whatif('auditFail'); await refresh(); }, cards.board);
    await A('off', async () => { await whatif('auditFail'); });
    await S('whatif-boundary-scan-over', 'What if: boundary scan exceeds 50,000 rows.', 'Prototype controls > What if > Boundary scan exceeds 50,000 rows, then Refresh timeline.', async () => { await whatif('edgeOver'); await refresh(); }, cards.board);
    await A('off', async () => { await whatif('edgeOver'); await refresh(); });
    await S('whatif-suspect-d16-sql-timeouts', 'What if: SQL commands time out (D16) turns that suspect orange or red.', 'Prototype controls > What if > SQL commands time out (D16).', async () => { await pg.evaluate(() => { RT.susStep = 0; }); await whatif('sev:D16'); await trWait(); await seeAll(); }, cards.rail);
    await A('off', async () => { await whatif('sev:D16'); });
    await S('whatif-suspect-s10-app-pool-offline', 'What if: app pool goes offline (S10).', 'Prototype controls > What if > App pool goes offline (S10).', async () => { await pg.evaluate(() => { RT.susStep = 0; }); await whatif('sev:S10'); await trWait(); await seeAll(); }, cards.rail);
    await A('off', async () => { await whatif('sev:S10'); });
    await S('whatif-suspect-m12-mongo-rejects', 'What if: MongoDB rejects connections (M12).', 'Prototype controls > What if > MongoDB rejects connections (M12).', async () => { await pg.evaluate(() => { RT.susStep = 0; }); await whatif('sev:M12'); await trWait(); await seeAll(); }, cards.rail);
    await A('off', async () => { await whatif('sev:M12'); });
    await tsc('m-first');
    await S('mvp2-coverage-ledger-no-coverage', 'MVP-2 (planned): coverage ledger says no coverage and offers Pull this window.', 'Scenario m-first (prototype panel: MVP-1 + MVP-2).', async () => { await showTl().catch(() => {}); });
    await S('mvp2-first-pull-done', 'MVP-2: a pull saves the incident window.', 'Press Pull this window.', async () => { await click('Pull this window'); await wait(800); });
    await tsc('m-overlap');
    await S('mvp2-overlap-409', 'MVP-2: a window that partly overlaps a stored pull is refused (409) with the remedy.', 'Scenario m-overlap, press Pull this window.', async () => { await showTl().catch(() => {}); await click('Pull this window'); await wait(600); });
    await tsc('t-all');
    await A('stage back', async () => { await pg.evaluate(() => ACT.stageSet({ s: 'mvp1' })); await wait(300); });
    await A('dock on', async () => { await setStyle(false); await pg.evaluate(() => { RT.dockMin = false; render(); }); });
    await S('prototype-dock-bottom-bar-timeline', 'The prototype-only bottom bar on the timeline: layout pill, prepared-situation picker, What if switches, last action, Prototype and State buttons.', 'Always visible at the bottom of the prototype page.', null, 'view');
    await S('prototype-panel-launch-and-stage', 'The Prototype panel: launch the app as Normal, Key locked, Key damaged or Fresh PC; release stage MVP-1 or MVP-2; test aids; assumptions and questions.', 'Press Prototype in the bottom bar.', async () => { await pg.getByRole('button', { name: 'Prototype', exact: true }).click(); await wait(400); }, 'view');
    await S('prototype-state-panel', 'The State panel: full state after every action and the action log.', 'Press State in the bottom bar.', async () => { await pg.getByRole('button', { name: 'State', exact: true }).click(); await wait(400); }, 'view');
    await A('dock off', async () => { await pg.evaluate(() => { RT.protoOpen = false; RT.stateOpen = false; render(); }); await setStyle(true); });
    await A('narrow', async () => { await pg.setViewportSize({ width: 390, height: 800 }); await wait(300); });
    await S('narrow-timeline-phone-width', 'The timeline at phone width: single column, rail below the chart, no sideways scroll.', 'Resize the window to 390 pixels wide.', async () => { await tsc('t-all'); });
    await S('narrow-case-home-phone-width', 'The case home at phone width.', 'Resize the window to 390 pixels wide, open the case.', async () => { await sc('case', 'c-ready'); });
  }
  await browser.close();
  fs.writeFileSync(path.join(OUT, ONLY ? 'MANIFEST-' + ONLY + '.json' : 'MANIFEST.json'), JSON.stringify(manifest, null, 1));
  console.log('\nCaptured ' + manifest.length + ' images; failed ' + failures.length + (failures.length ? ': ' + failures.join(', ') : ''));
})();
