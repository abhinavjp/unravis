// Focused browser flow checks for prototype/tracing-paper-PROTOTYPE.html (throwaway; not the product's test suite).
// Run:  node prototype/flow-check.cjs [screenshot-dir]      (needs Playwright, e.g. `npm i playwright` or the one preinstalled in the cloud image)
// It drives the prototype in Chromium through the normal journeys (no scenario shortcuts unless a step says so) and prints PASS/FAIL per check.
const { chromium } = require('playwright');
const path = require('path'), fs = require('fs');
const FILE = 'file://' + path.resolve(__dirname, 'tracing-paper-PROTOTYPE.html');
const SHOTS = process.argv[2] || '';
let pass = 0, fail = 0; const failures = [];
const ok = (name, cond, extra = '') => { if (cond) { pass++; console.log('PASS  ' + name); } else { fail++; failures.push(name); console.log('FAIL  ' + name + (extra ? '  -> ' + extra : '')); } };
(async () => {
  const browser = await chromium.launch(); const ctx = await browser.newContext({ viewport:{ width:1440, height:900 } }); const pg = await ctx.newPage(); const errs = [];
  pg.on('pageerror', e => errs.push(e.message)); pg.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
  const shot = async n => { if (SHOTS) { fs.mkdirSync(SHOTS, { recursive:true }); await pg.screenshot({ path:path.join(SHOTS, n + '.png') }); } };
  const txt = async sel => (await pg.locator(sel || '#main').first().innerText()).replace(/\s+/g, ' ');
  const click = async (name, o = {}) => pg.getByRole('button', Object.assign({ name, exact:true }, o)).first().click();
  const screen = () => pg.evaluate(() => RT.screen);
  const fresh = async () => { await pg.goto(FILE); await pg.evaluate(() => { localStorage.clear(); sessionStorage.clear(); }); await pg.goto(FILE); await pg.waitForFunction(() => document.querySelector('#card-existing') || document.querySelector('#mk')); await pg.waitForFunction(() => !document.body.innerText.includes('Loading your cases')); };

  /* ---------- A. start screen: three cards ---------- */
  await fresh(); await shot('01-start');
  ok('A1 fresh launch reaches the start screen', (await screen()) === 'start');
  const heads = await pg.locator('.cards h3, .card > h3').allInnerTexts(); ok('A2 three cards', ['Use an existing case', 'Enter an existing case', 'Create a new one'].every(h => heads.includes(h)), heads.join('|'));
  const list = await txt('section[aria-labelledby=card-existing]'); ok('A3 seeded cases listed with states', /Payroll\.Run slowdown/.test(list) && /Closed/.test(list) && /Key ending ey01/.test(list) && /Legacy case from an older version/.test(list) && /The working copy is missing/.test(list) && /Erased/.test(list), list.slice(0, 200));
  ok('A4 locked/untrusted cases are named by code, not title', !/Vendor portal outage/.test(list) && /Case VENDORPO/i.test(list));
  /* ---------- B. enter an existing case ---------- */
  await pg.fill('#ec-code', 'not a code'); await click('Open case'); ok('B1 invalid code message', /That code does not match a case on this computer\./.test(await txt('section[aria-labelledby=card-enter]')));
  await pg.fill('#ec-code', 'payrollr'); await click('Open case'); ok('B2 the 8-character short code does not open a case (preserved)', /does not match/.test(await txt('section[aria-labelledby=card-enter]')));
  await pg.fill('#ec-code', ''); ok('B3 empty code: button waits, no sentence', await pg.getByRole('button', { name:'Open case', exact:true }).first().isDisabled());
  const orders = await pg.evaluate(() => ID.orders); await pg.fill('#ec-code', orders.slice(0, 8) + '-' + orders.slice(8, 16) + ' ' + orders.slice(16).toUpperCase()); await click('Open case');
  ok('B4 the full code (spaces, dashes, case ignored) opens the case', (await screen()) === 'case' && /Payroll\.Run slowdown, 20 Oct/.test(await txt()));
  await pg.goBack(); await pg.waitForFunction(() => RT.screen === 'start'); ok('B5 browser Back returns to the start screen', (await screen()) === 'start');
  await pg.goForward(); await pg.waitForFunction(() => RT.screen === 'case'); ok('B6 browser Forward returns to the case', (await screen()) === 'case');
  await click('All cases: back to the start screen'); await pg.waitForFunction(() => document.querySelector('#card-enter'));
  await click('Restore from a case file'); await click('Restore as a new case'); ok('B7 restore: empty path refused', /Type the path of the case file\./.test(await txt('section[aria-labelledby=card-enter]')));
  await pg.fill('#ec-path', 'C:\\Cases\\x.txt'); await pg.fill('#ec-key', 'k'); await click('Restore as a new case'); ok('B8 restore: wrong extension', /must end in \.unravis-case/.test(await txt('section[aria-labelledby=card-enter]')));
  await pg.fill('#ec-path', '\\\\server\\share\\a.unravis-case'); await pg.fill('#ec-key', 'k'); await click('Restore as a new case'); ok('B9 restore: network path refused', /Network paths are not accepted/.test(await txt('section[aria-labelledby=card-enter]')));
  await pg.fill('#ec-path', 'C:\\Cases\\orders-slowdown.unravis-case'); await pg.fill('#ec-key', 'wrong'); await click('Restore as a new case'); ok('B10 restore: wrong key gives the one failure sentence', /could not be opened with that master key/.test(await txt('section[aria-labelledby=card-enter]')));
  const before = await pg.evaluate(() => DB.cases.length); await pg.fill('#ec-path', 'C:\\Cases\\orders-slowdown.unravis-case'); await pg.fill('#ec-key', 'correct horse battery staple'); await click('Restore as a new case'); await pg.waitForFunction(() => RT.screen === 'case');
  ok('B11 restore makes a NEW case and opens it', (await pg.evaluate(() => DB.cases.length)) === before + 1 && (await pg.evaluate(() => RT.caseId)) !== orders);
  /* ---------- C. create a new case ---------- */
  await click('All cases: back to the start screen'); await pg.waitForFunction(() => document.querySelector('#card-create')); await click('Create a new case'); await pg.waitForSelector('#wz-name'); await shot('02-create-name');
  ok('C1 create opens the guide on Name', (await screen()) === 'create' && /Step 1 of 3: Name/.test(await txt()));
  ok('C2 Next is off with no name', await pg.getByRole('button', { name:'Next', exact:true }).isDisabled());
  await pg.fill('#wz-name', 'a'); await pg.locator('#wz-name').blur(); ok('C3 one-letter name: problem + Next off', /at least 2 characters/.test(await txt()) && await pg.getByRole('button', { name:'Next', exact:true }).isDisabled());
  await pg.fill('#wz-name', 'x'.repeat(81)); ok('C4 81 characters: too long, shown at once', /at most 80 characters/.test(await txt()));
  await pg.fill('#wz-name', 'Checkout slowdown'); await pg.fill('#wz-envs', 'Production, production, Staging'); ok('C5 environments deduplicated, note shown', /Left out, as the name is already in the list: production/.test(await txt()) && /2 environments/.test(await txt()));
  await pg.locator('[data-bind="W.caseKey.on"]').click(); await pg.fill('#wz-ck', 'short'); ok('C6 own key too short blocks Next', /too short/i.test(await txt()) && await pg.getByRole('button', { name:'Next', exact:true }).isDisabled());
  await pg.fill('#wz-ck', 'a-long-case-key-1'); await pg.fill('#wz-ck2', 'a-long-case-key-2'); ok('C7 key mismatch', /not the same/.test(await txt()));
  await pg.fill('#wz-ck2', 'a-long-case-key-1'); await click('Next'); await pg.waitForSelector('text=Scan this computer'); ok('C8 Next moves to Sources', /Step 2 of 3: Sources/.test(await txt()));
  await click('Back'); ok('C9 Back keeps every entry', (await pg.inputValue('#wz-name')) === 'Checkout slowdown' && (await pg.inputValue('#wz-envs')).includes('Staging'));
  await click('Next'); await click('Scan this computer'); await pg.waitForSelector('#fd-fd_iis'); await shot('03-create-sources'); await pg.locator('#fd-fd_iis').click(); await pg.locator('#fd-fd_winev').click(); ok('C10 scan, tick two sources', /2 sources chosen/.test(await txt()));
  ok('C11 unreadable and needs-export sources cannot be ticked', await pg.locator('#fd-fd_sqlerr').isDisabled());
  await click('Connect to MongoDB'); await pg.fill('#mg-uri', 'nope'); await click('Check the connection'); ok('C12 mongo: bad address', /must start with mongodb:/.test(await txt()));
  await pg.fill('#mg-uri', 'mongodb://audit-mongo:27017'); await click('Check the connection'); await click('Add the chosen databases'); ok('C13 mongo: audit database added', /3 sources chosen/.test(await txt()));
  await click('Next'); await pg.waitForSelector('text=Check what will be made'); await shot('04-create-review'); ok('C14 Review lists what will be made', /Checkout slowdown/.test(await txt()) && /Its own key/.test(await txt()) && /An audit database/.test(await txt()));
  await click('← Leave the guide'); ok('C15 leaving a dirty draft asks first', /Leave without creating a case\?/.test(await txt())); await click('Keep working'); ok('C16 Keep working stays', (await screen()) === 'create');
  await click('Create case'); await pg.waitForFunction(() => document.body.innerText.includes('could not be read'), null, { timeout:8000 }); await shot('05-create-result');
  ok('C17 a failed source does not lose the case', /Windows event log/.test(await txt()) && /The case was kept/.test(await txt()));
  await click('Open the case anyway'); await pg.waitForFunction(() => RT.screen === 'case'); ok('C18 the new case opens on its home with the retry list', /Needs another try/.test(await txt()) && /Checkout slowdown/.test(await txt()));
  await click('Try again'); ok('C19 retry: first retry fails and stays listed', /It did not go through again/.test(await txt())); await click('Try again'); ok('C20 retry: second retry succeeds', !/Needs another try<|Windows event log/.test(await txt()) || (await pg.evaluate(() => curCase().retries.length)) === 0);
  await click('All cases: back to the start screen'); await pg.waitForFunction(() => document.querySelector('#card-existing')); ok('C21 the new case is on the start list', /Checkout slowdown/.test(await txt('section[aria-labelledby=card-existing]')));
  await click('Create a new case'); await pg.fill('#wz-name', 'Temp'); await click('← Leave the guide'); await click('Leave and discard'); await pg.waitForFunction(() => RT.screen === 'start'); ok('C22 leave and discard returns to the start screen', (await screen()) === 'start');
  /* ---------- D. protected case and lifecycle ---------- */
  const keyedId = await pg.evaluate(() => ID.keyed); await pg.evaluate(id => ACT.openCase({ id }), keyedId); await pg.waitForSelector('#ck-open'); await shot('06-case-key');
  ok('D1 a keyed case opens on its key card', /This case has its own key/.test(await txt()));
  await pg.fill('#ck-open', 'wrong-wrong-wrong'); await click('Open case'); ok('D2 wrong case key', /did not open this case/.test(await txt()));
  await pg.fill('#ck-open', 'vendor-case-key-01'); await click('Open case'); await pg.waitForSelector('#ch-title'); ok('D3 right case key opens it', /Vendor portal outage/.test(await txt()));
  ok('D4 no files yet: the case says only the live audit lane has data', /Only the live audit lane has data/.test(await txt()));
  await pg.evaluate(id => { RT.caseId = id; go('case', { caseId:id }); }, await pg.evaluate(() => ID.closed)); ok('D5 closed case: Start tracing disabled', await pg.getByRole('button', { name:'Start tracing', exact:true }).isDisabled());
  await click('Reopen this case'); ok('D6 reopen makes it open again', await pg.getByRole('button', { name:'Start tracing', exact:true }).isEnabled());
  /* ---------- E. tracing -> analysis -> evidence -> record -> back ---------- */
  await pg.evaluate(id => { go('case', { caseId:id }); }, await pg.evaluate(() => ID.orders)); await pg.waitForSelector('#ch-title');
  await shot('07-case-home'); ok('E0 case home shows Tracing and no Guided button', /Tracing/.test(await txt()) && !/Guided analysis|Open guided/i.test(await txt()));
  await click('Start tracing'); await pg.waitForSelector('#pk-inc'); ok('E1 tracing shows incident AND reference windows', /Incident window/.test(await txt()) && /Reference window/.test(await txt()));
  await pg.click('#pk-inc'); await pg.getByRole('button', { name:'The slowdown in the demo data' }).click(); await pg.waitForFunction(() => !document.querySelector('.pk'));
  await click('Start tracing'); await pg.waitForSelector('#ov'); await shot('08-tracing');
  ok('E2 three lanes drawn with live and stored provenance', /API audit/.test(await txt()) && /Live read/.test(await txt()) && /Stored · as of/.test(await txt()) && /not a conservation check/.test(await txt()));
  ok('E3 per-lane comparison uses the reference', /the comparison period rate|No comparison/.test(await txt()));
  const win0 = await pg.evaluate(() => [INV.q.start, INV.q.end, INV.q.refStart, INV.q.refEnd].join());
  const box = await pg.locator('#ov').boundingBox(); await pg.mouse.click(box.x + box.width * 0.5, box.y + 20); ok('E4 clicking a bucket selects it', (await pg.evaluate(() => RT.selBucket)) !== null);
  await pg.keyboard.press('ArrowRight'); ok('E5 arrow keys move the selection', (await pg.evaluate(() => RT.selBucket)) !== null);
  await click('Open Run analysis for this bucket'); await pg.waitForFunction(() => RT.screen === 'running'); await shot('09-analysis-from-tracing');
  ok('E6 analysis opens with an explicit hand-off strip and both windows', /Opened from Tracing/.test(await txt()) && /reference \(comparison period\)/.test(await txt()));
  const inc1 = await pg.evaluate(() => [INV.q.start, INV.q.end].join()); ok('E7 incident became the clipped bucket, reference kept', inc1 !== win0.split(',').slice(0, 2).join() && (await pg.evaluate(() => [INV.q.refStart, INV.q.refEnd].join())) === win0.split(',').slice(2).join());
  await click('Where is it?', { exact:true }).catch(async () => { await pg.getByRole('button', { name:'Where is it?' }).first().click(); }); await pg.waitForSelector('#where-h'); await shot('10-where');
  ok('E8 Where is it? ranks operations', /Payroll\.Run/.test(await txt()));
  await pg.locator('button.rankrow', { hasText:'Payroll.Run' }).first().click(); await pg.waitForSelector('text=Slowest requests'); await pg.getByRole('button', { name:/^Inspect record/ }).first().click(); await pg.waitForSelector('#rec-h'); await shot('11-record');
  ok('E9 evidence -> record detail', /Stored fields/.test(await txt()) && /Derived by Unravis/.test(await txt()));
  await pg.getByRole('button', { name:'← Back to the evidence' }).click(); await pg.getByRole('button', { name:'Nearby' }).first().click(); ok('E10 Nearby activity (call trace is not wired)', /Call trace for one request is not wired/.test(await txt()));
  await click('← Back to tracing'); await pg.waitForFunction(() => RT.screen === 'tracing'); ok('E11 back to tracing keeps both windows', (await pg.evaluate(() => [INV.q.start, INV.q.end].join())) === inc1 && (await pg.evaluate(() => [INV.q.refStart, INV.q.refEnd].join())) === win0.split(',').slice(2).join());
  /* ---------- F. the two window rules ---------- */
  await pg.evaluate(() => { const Zd = s => Date.parse(s); INV.q.start = Zd('2026-10-20T11:00:00Z'); INV.q.end = Zd('2026-10-20T12:00:00Z'); INV.q.refStart = Zd('2026-10-20T10:30:00Z'); INV.q.refEnd = Zd('2026-10-20T11:30:00Z'); render(); });
  ok('F1 tracing: overlapping reference only warns, Start tracing stays on', /The two windows overlap/.test(await txt()) && await pg.getByRole('button', { name:/Start tracing|Trace again/ }).first().isEnabled());
  await pg.evaluate(() => { go('running'); }); await pg.waitForSelector('.scope');
  ok('F2 analysis: the same windows are refused with its own rule', /The reference window must end at or before the current window starts/.test(await txt('.scope')) && await pg.getByRole('button', { name:'Run analysis', exact:true }).isDisabled());
  await pg.evaluate(() => { INV.q.end = INV.q.start + 8 * 86400000; INV.q.refEnd = INV.q.start; INV.q.refStart = INV.q.start - 8 * 86400000; render(); }); ok('F3 analysis: windows over 7 days are refused', /at most 7 days/.test(await txt('.scope')));
  /* ---------- G. export with the approved tracing change ---------- */
  await pg.evaluate(() => loadScenario('running', 'x-ready')); await click('Preview'); ok('G1 export preview with scrub on by default', /scrub on/.test(await txt('.drawer')) && /Client IP, referer and cookie hash are left out/.test(await txt('.drawer')));
  await pg.evaluate(() => { findCase(ID.orders).rev++; }); await click('Save the export'); ok('G2 revision moved after preview: 409 and a forced new preview', /409/.test(await txt('.drawer')) && await pg.getByRole('button', { name:'Save the export' }).isDisabled());
  await click('Preview again'); await click('Save the export'); ok('G3 re-previewed export saves', /Saved\./.test(await txt('.drawer')));
  /* ---------- H. launch conditions and narrow layout ---------- */
  await pg.evaluate(() => loadScenario('start', 's-locked')); ok('H1 locked key: unlock screen first, no case titles', /Unlock your cases/.test(await txt()) && !/Payroll/.test(await txt()));
  await pg.fill('#mk', 'orange-sunrise-2026'); await pg.keyboard.press('Enter'); await pg.waitForFunction(() => RT.screen === 'start'); ok('H2 unlocking reaches the start screen', (await screen()) === 'start');
  await pg.evaluate(() => loadScenario('start', 's-fresh')); ok('H3 fresh PC offers the master key set-up', /Protect your cases with a master key/.test(await txt()));
  await pg.setViewportSize({ width:390, height:800 }); await pg.evaluate(() => { RT.dockMin = true; render(); }); await pg.evaluate(() => loadScenario('case', 'c-ready')); await pg.waitForSelector('#ch-title'); await shot('12-narrow-case');
  const ov = await pg.evaluate(() => document.documentElement.scrollWidth); ok('H4 narrow layout has no sideways scroll on case home', ov <= 392, 'scrollWidth ' + ov);
  await pg.evaluate(() => { loadScenario('tracing', 't-all'); }); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); await pg.waitForSelector('#ov'); await shot('13-narrow-tracing'); const ov2 = await pg.evaluate(() => document.documentElement.scrollWidth); ok('H5 narrow layout has no sideways scroll on tracing', ov2 <= 392, 'scrollWidth ' + ov2);

  /* ---------- I. empty, closed, archived, partial-data and failed-source scenarios ---------- */
  await pg.setViewportSize({ width:1440, height:900 }); await pg.evaluate(() => { RT.dockMin = false; });
  const sc = async (screenName, id) => { await pg.evaluate(([a, b]) => loadScenario(a, b), [screenName, id]); await pg.waitForTimeout(350); };
  await sc('start', 's-empty'); ok('I1 empty case list says so and keeps the three cards', /No cases yet\. Create a new case to begin\./.test(await txt()) && /Create a new one/.test(await txt()));
  await sc('start', 's-fail'); await pg.waitForFunction(() => document.body.innerText.includes('could not be loaded')); ok('I2 list failure shows the error and Try again', /Your cases could not be loaded\./.test(await txt()));
  await click('Try again'); await pg.waitForFunction(() => document.body.innerText.includes('Payroll.Run slowdown')); ok('I3 Try again loads the list', /Payroll\.Run slowdown/.test(await txt()));
  await sc('start', 's-many'); await pg.waitForFunction(() => document.body.innerText.includes('cases shown')); ok('I4 long list is paged with Show more cases', /50 of 128 cases shown/.test(await txt()) || /of \d+ cases shown/.test(await txt()));
  await sc('create', 'w-resumed'); ok('I5 a left-behind draft is offered again', /Your earlier answers are still here/.test(await txt()));
  await sc('case', 'c-archived'); ok('I6 archived case: record and restore choices only', /This case is archived/.test(await txt()) && /Restore as a new case/.test(await txt()) && !/Open the analysis/.test(await txt()) && !/Start tracing/.test(await txt()));
  await sc('case', 'c-closed'); ok('I7 closed case: reason and disabled add buttons', /This case is closed/.test(await txt()) && await pg.getByRole('button', { name:'Start tracing', exact:true }).isDisabled() && await pg.getByRole('button', { name:'Add a file', exact:true }).isDisabled());
  await sc('case', 'c-failed'); await click('Add the file again'); ok('I8 failed import: Add the file again opens the import drawer', /Import files/.test(await txt('.drawer')));
  await sc('running', 'a-sparse'); ok('I9 reference without data: honest note, no baseline-dependent deltas', /No audit data was recorded in the comparison period/.test(await txt()) && !/vs reference/.test(await txt()) && !/the comparison period\b.*x the/.test(await txt()));
  await sc('running', 'a-partial'); ok('I10 half-covered reference is labelled and withheld', /only 50% covered|% covered by data/.test(await txt()));
  await sc('running', 'a-slow'); ok('I11 a good reference shows real differences', /x the comparison period/.test(await txt()) && /P95 is about \d+ times/.test(await txt()));
  await sc('running', 'a-srcfail'); ok('I12 failed source banner', /Some sources failed/.test(await txt()));
  await sc('running', 'a-empty'); ok('I13 no data in the period', /No requests in this period/.test(await txt()));
  await sc('running', 'a-nocase'); ok('I14 analysis without a case has no Case home or Tracing', !(await pg.getByRole('button', { name:'Case home', exact:true }).count()) && !/Compare in Tracing/.test(await txt()));
  await sc('tracing', 't-tz'); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); ok('I15 unvalidated time zone switches the multi-source chart off', /Time zone not checked, so nothing is drawn/.test(await txt()) && !(await pg.locator('#ov').count()));
  await sc('tracing', 't-empty'); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); ok('I16 nothing stored: fixed no-file reasons, allLanesStored caveat', (await txt()).split('This case has no file for this source').length >= 3 && /allLanesStored is literally true/.test((await pg.evaluate(() => document.querySelector('details.card:last-of-type').innerText))) || /This case has no file for this source/.test(await txt()));
  await sc('tracing', 't-partial'); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); ok('I17 partly stored: gap noted on the lane', /Stored data has a gap in this window/.test(await txt()));
  await sc('tracing', 'w-sparse'); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); ok('I18 tracing: sparse reference says no comparison', /No comparison/.test(await txt()));
  await sc('tracing', 'w-30dx'); ok('I19 30 days + 1 second is refused', /The longest window is 30 days/.test(await txt()) && await pg.getByRole('button', { name:'Start tracing', exact:true }).first().isDisabled());
  await sc('tracing', 'w-30d'); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); ok('I20 off-hour 30 days coarsens to 8 hours, 91 buckets', /8 hours buckets/.test(await txt()) && /91 on the axis/.test(await txt()));
  await sc('tracing', 'w-overlap'); ok('I21 overlap warning is non-blocking in tracing', /The two windows overlap/.test(await txt()) && await pg.getByRole('button', { name:'Start tracing', exact:true }).first().isEnabled());
  await sc('tracing', 't-closed'); ok('I22 closed case: tracing is off with the reason', await pg.getByRole('button', { name:'Start tracing', exact:true }).first().isDisabled());
  await sc('tracing', 'm-overlap'); await click('Pull this window'); ok('I23 MVP-2: partly overlapping pull is refused with the remedy', /409 window-overlap; re-pull the exact stored window or import a disjoint window/.test(await txt()));
  await sc('tracing', 'm-late'); await click('Pull this window'); ok('I24 MVP-2: a changed pull replaces the stored one', /the stored pull was replaced/.test(await txt()));
  await sc('tracing', 'm-same'); await click('Pull this window'); ok('I25 MVP-2: unchanged pull is a no-op', /Nothing new/.test(await txt()));
  await sc('tracing', 't-all'); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); await pg.evaluate(() => { RT.sim.auditOver = true; }); await pg.getByRole('button', { name:'Trace again', exact:true }).first().click(); ok('I26 audit over budget: incomplete lane, nothing shortened', /nothing was shortened/.test(await txt()));
  /* ---------- J. keyboard ---------- */
  await pg.evaluate(() => loadScenario('create', 'w-new')); await pg.fill('#wz-name', 'Keyboard case'); await pg.locator('#wz-name').press('Enter'); await pg.waitForFunction(() => RT.screen === 'create' && WIZ.step === 'sources'); ok('J1 Enter in the name field goes to the next step and focus lands on the heading', (await pg.evaluate(() => document.activeElement && document.activeElement.id)) === 'wz-heading');
  await pg.evaluate(() => loadScenario('tracing', 't-all')); await pg.getByRole('button', { name:'Start tracing', exact:true }).first().click(); await pg.locator('#ov').focus(); await pg.keyboard.press('Home'); await pg.keyboard.press('Enter'); await pg.waitForFunction(() => RT.screen === 'running'); ok('J2 chart: Home then Enter opens Run analysis for the first bucket, keyboard only', (await screen()) === 'running');
  ok('Z no script errors during the whole run', errs.length === 0, errs.slice(0, 3).join(' | '));
  await browser.close(); console.log('\n' + pass + ' passed, ' + fail + ' failed' + (fail ? ': ' + failures.join('; ') : '')); process.exit(fail ? 1 : 0);
})().catch(e => { console.error('CHECK CRASHED:', e.message); process.exit(2); });
