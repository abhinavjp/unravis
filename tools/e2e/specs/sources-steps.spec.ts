// Step 7, Step 8, Add data, Add another day and Combinations on the case home. The demo keeps its state across the tests of this file:
// the reads come first, then the days are added to the open case, and the closed case is only looked at.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';

const OPEN = 'Checkout slowdown, 17 October';
const NOTHING = 'Report export timeouts, 18 October';
const CLOSED = 'Night batch overrun, 10 October';

async function hub(page: Page, caseTitle: string, tab: string) {
  await goHome(page);
  await openCase(page, caseTitle);
  await page.getByRole('tab', { name: tab, exact: true }).click();
  const panel = page.getByRole('tabpanel');
  await expect(panel).toBeVisible();
  return panel;
}

type Panel = ReturnType<Page['getByRole']>;
const form = (panel: Panel) => panel.getByRole('region', { name: 'Read from a server' });
const WIN = { is: '2026-10-20 08:45', ie: '2026-10-20 09:00', rs: '2026-10-20 08:30', re: '2026-10-20 08:45' };

async function setTimes(f: Panel, t: { is?: string; ie?: string; rs?: string; re?: string }) {
  if (t.is !== undefined) await f.getByLabel('Incident starts (UTC)', { exact: true }).fill(t.is);
  if (t.ie !== undefined) await f.getByLabel('Incident ends (UTC)', { exact: true }).fill(t.ie);
  if (t.rs !== undefined) await f.getByLabel('Reference starts (UTC)', { exact: true }).fill(t.rs);
  if (t.re !== undefined) await f.getByLabel('Reference ends (UTC)', { exact: true }).fill(t.re);
}

// Reads one day on Step 7 or Step 8 with the given database and the demo window, and waits for the notice.
async function readDay(page: Page, panel: Panel, db: string, kind: 'sql' | 'mongo') {
  const f = form(panel);
  if (kind === 'sql') {
    const again = panel.getByRole('button', { name: 'Add a SQL Server' });
    await expect(again.or(f)).toBeVisible();
    if (await again.count()) await again.click();
  }
  await expect(f).toBeVisible();
  await f.getByLabel('Audit database', { exact: true }).fill(db);
  await setTimes(f, WIN);
  await f.getByRole('button', { name: 'Read this day' }).click();
  await expect(panel.locator('[data-notice]')).toHaveText('Read and added to the case.');
}

test('GUIDE-24: Combinations lists the five rows and what this case holds today', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Combinations');
  await expect(panel.getByRole('heading', { name: 'Source combinations' })).toBeVisible();
  const rows = panel.locator('tbody tr');
  await expect(rows).toHaveCount(6);
  await expect(rows.nth(0)).toContainText('Mongo audit logs only');
  await expect(rows.nth(1)).toContainText('+ IIS logs');
  await expect(rows.nth(2)).toContainText('+ SQL Server');
  await expect(rows.nth(3)).toContainText('+ MongoDB server (profiler, clusterMonitor)');
  await expect(rows.nth(4)).toContainText('+ Blackbox (web server), and one on the SQL host');
  await expect(rows.nth(5)).toContainText('This case today');
  await expect(rows.nth(5)).toContainText('No SQL Server or MongoDB server has been read for this case.');
  await expect(panel.getByText('Add the next source that unlocks the most.')).toBeVisible();
  // The table never says a missing source passed.
  await expect(panel.getByText(/\bpassed\b/i)).toHaveCount(0);
});

test('GUIDE-24a: the case that read nothing says so on Step 7, never that a check passed, and its Add another day and Add data buttons send nothing', async ({ page }) => {
  const panel = await hub(page, NOTHING, 'Step 7: SQL Server');
  await expect(panel.getByText('Nothing could be read', { exact: true })).toBeVisible();
  await expect(panel.getByText('The case is not broken. Every source answered, but the window held nothing to read.')).toBeVisible();
  const table = panel.getByRole('table', { name: 'What this case can answer' });
  await expect(table.getByText('Did calls get slower, and when?')).toBeVisible();
  await expect(table.getByText('Cannot test yet: no records in the window')).toBeVisible();
  await expect(table.getByText('Is SQL blocking or busy?')).toBeVisible();
  await expect(table.getByText('Cannot test yet: no incident window to look at')).toBeVisible();
  await expect(panel.getByText('No question was answered. A missing answer is never a pass.')).toBeVisible();
  await expect(panel.getByText(/\bpassed\b|\bPass\b/)).toHaveCount(0);

  // Add another day opens the read form on this same step; no request is made until Read this day is pressed.
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST') posts.push(r.url()); });
  await panel.getByRole('button', { name: 'Add another day' }).click();
  await expect(form(panel)).toBeVisible();
  expect(posts).toEqual([]);
  // Add data leads to the five cards.
  await panel.getByRole('button', { name: 'Add data', exact: true }).click();
  await expect(page.locator('[data-card=sql]')).toBeVisible();
  expect(posts).toEqual([]);
});

test('GUIDE-21: the Add data tab has five cards with their counts and each button leads where it says', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Add data');
  const card = (id: string) => panel.locator('[data-card=' + id + ']');
  await expect(card('sql')).toContainText('1 saved connection ready');
  await expect(card('sql')).toContainText('None yet');
  await expect(card('mongo')).toContainText('1 saved connection ready');
  await expect(card('mongo')).toContainText('None yet');
  await expect(card('blackbox')).toContainText('None yet');
  await expect(card('files')).toBeVisible();
  await expect(card('day')).toContainText('0 of 0 sources can be read by day');
  await expect(card('day').getByRole('button', { name: 'Add another day' })).toBeDisabled();
  await expect(panel.getByText('A step that ran before the new data arrived is marked Out of date, rerun. It does not change by itself.')).toBeVisible();
  await expect(panel.getByText('Reading does not change any server.')).toBeVisible();

  await card('sql').getByRole('button', { name: 'Add SQL source' }).click();
  await expect(page.getByRole('tab', { name: 'Step 7: SQL Server', exact: true })).toHaveAttribute('aria-selected', 'true');
  await page.getByRole('tab', { name: 'Add data', exact: true }).click();
  await card('mongo').getByRole('button', { name: 'Add Mongo source' }).click();
  await expect(page.getByRole('tab', { name: 'Step 8: MongoDB', exact: true })).toHaveAttribute('aria-selected', 'true');
  await page.getByRole('tab', { name: 'Add data', exact: true }).click();
  await card('files').getByRole('button', { name: 'Add files' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(1);
  await expect(page.getByRole('heading', { name: 'Import files' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Add data' })).toBeVisible();
  await card('blackbox').getByRole('button', { name: 'Add a Blackbox' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(1);
  await expect(page.getByRole('dialog').getByText(/recorder/i).first()).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('GUIDE-21a: on a closed case every Add data button is off and the note says why', async ({ page }) => {
  const panel = await hub(page, CLOSED, 'Add data');
  await expect(panel.getByText('This case cannot take new data right now: it is closed, or it changed somewhere else. Reopen or refresh it first. What it already holds can still be read here.')).toBeVisible();
  for (const name of ['Add SQL source', 'Add Mongo source', 'Add a Blackbox', 'Add files', 'Add another day']) {
    await expect(panel.getByRole('button', { name }), name).toBeDisabled();
  }
});

test('GUIDE-23a: with no SQL day in the case, Add another day says to read one first', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Add another day');
  await expect(panel.getByText('The case holds no SQL Server day yet. Read one first, then add another.')).toBeVisible();
  await expect(panel.getByText('Only sources that can be read by day are offered.')).toBeVisible();
  await expect(panel.getByRole('button', { name: 'Read this day' })).toHaveCount(0);
});

test('GUIDE-22: with nothing read, Step 7 says Cannot test yet, offers the read form and shows the grant script that is never run', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Step 7: SQL Server');
  const none = panel.locator('[data-no-source]');
  await expect(none).toContainText('Cannot test yet');
  await expect(none).toContainText('No SQL Server has been read for this case yet. Step 7 says Cannot test yet until one is added.');
  await expect(form(panel)).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'Permissions to give the login' })).toBeVisible();
  await expect(panel.getByText('Unravis never runs this script. It only shows it, so the owner of the server can run it.')).toBeVisible();
  await expect(panel.locator('pre.script')).toContainText('VIEW SERVER PERFORMANCE STATE');
  // Nothing was read just by looking.
  await expect(panel.getByRole('heading', { name: 'Days read' })).toHaveCount(0);
});

test('GUIDE-22a: the read form refuses a missing connection, database or time with its own sentence and sends nothing', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Step 7: SQL Server');
  const f = form(panel);
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST') posts.push(r.url()); });
  const read = f.getByRole('button', { name: 'Read this day' });
  const problem = f.locator('[data-problem]');
  // The one saved connection is already picked, so the database comes first.
  await read.click();
  await expect(problem).toHaveText('Type the audit database first.');
  await f.getByLabel('Saved connection', { exact: true }).selectOption({ label: 'Choose a connection…' });
  await read.click();
  await expect(problem).toHaveText('Choose a saved connection first.');
  await f.getByLabel('Saved connection', { exact: true }).selectOption({ label: 'Demo SQL Server' });
  await f.getByLabel('Audit database', { exact: true }).fill('AuditDb');
  await setTimes(f, { is: '26/10/2026 10:30', ie: '2026-10-26 11:00', rs: '2026-10-26 09:30', re: '2026-10-26 10:30' });
  await read.click();
  const badTime = 'A time could not be read. Write each one as year-month-day, then hour:minute, like 2026-10-26 10:30.';
  await expect(problem).toHaveText(badTime);
  await setTimes(f, { is: '2026-10-26 10:30:15' });
  await read.click();
  await expect(problem).toHaveText(badTime);
  await setTimes(f, { is: '' });
  await read.click();
  await expect(problem).toHaveText('Give the incident a start and an end, with the end after the start.');
  await setTimes(f, { is: '2026-10-26 10:30', rs: '' });
  await read.click();
  await expect(problem).toHaveText('Give the reference a start and an end, with the end after the start.');
  expect(posts).toEqual([]);
});

test('GUIDE-22b: the grant panel fills in the login and database, switches between the 2022 and 2019 scripts and refuses a hostile login name', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Step 7: SQL Server');
  const grant = panel.getByRole('region', { name: 'Permissions to give the login' });
  const script = grant.locator('pre.script');
  await expect(script).toContainText('[login]');
  await grant.getByLabel('Login to give the permissions to', { exact: true }).fill('svc_unravis');
  await grant.getByLabel('Audit database', { exact: true }).fill('AuditDb');
  await expect(script).toContainText('svc_unravis');
  await expect(script).toContainText('USE [AuditDb]');
  for (const word of ['VIEW SERVER PERFORMANCE STATE', 'VIEW ANY ERROR LOG', 'VIEW DATABASE PERFORMANCE STATE']) await expect(script).toContainText(word);
  await expect(grant.locator('[data-version-note]')).toContainText('Do not run the 2019 script here.');
  await grant.getByRole('button', { name: 'SQL Server 2019 or earlier' }).click();
  await expect(grant.getByRole('button', { name: 'SQL Server 2019 or earlier' })).toHaveAttribute('aria-pressed', 'true');
  await expect(grant.locator('[data-version-note]')).toContainText('Do not run the 2022 script here.');
  await expect(script).toContainText('VIEW SERVER STATE');
  await expect(script).toContainText('VIEW DATABASE STATE');
  await expect(script).not.toContainText('VIEW SERVER PERFORMANCE STATE');
  await expect(script).not.toContainText('VIEW DATABASE PERFORMANCE STATE');
  await expect(script).toContainText('svc_unravis');
  // A login that is really a command is not put into the script.
  await grant.getByLabel('Login to give the permissions to', { exact: true }).fill('bad]; DROP LOGIN x;--');
  await expect(script).not.toContainText('DROP LOGIN');
  await expect(script).toContainText('[login]');
});

test('GUIDE-22c: Copy script puts the shown script on the clipboard and says so', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const panel = await hub(page, OPEN, 'Step 7: SQL Server');
  const grant = panel.getByRole('region', { name: 'Permissions to give the login' });
  const script = grant.locator('pre.script');
  await expect(script).toContainText('VIEW SERVER PERFORMANCE STATE');
  await grant.getByRole('button', { name: 'Copy script' }).click();
  await expect(grant.getByText('Copied. Paste it into SSMS.')).toBeVisible();
  const clip = await page.evaluate(() => navigator.clipboard.readText());
  const flat = (t: string) => t.replace(/\s+/g, ' ').trim();
  expect(flat(clip)).toBe(flat(await script.innerText()));
});

test('GUIDE-22d: reading AuditDb adds a Ready day with the read message and lists it under Days read', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Step 7: SQL Server');
  const f = form(panel);
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST') posts.push(r.url()); });
  await f.getByLabel('Audit database', { exact: true }).fill('AuditDb');
  await setTimes(f, WIN);
  await f.getByRole('button', { name: 'Read this day' }).dblclick();
  await expect(panel.locator('[data-notice]')).toHaveText('Read and added to the case.');
  expect(posts.length).toBe(1);
  await expect(panel.getByRole('heading', { name: 'Days read' })).toBeVisible();
  const src = panel.locator('[data-source]');
  await expect(src).toHaveCount(1);
  await expect(src.first()).toContainText('AuditDb');
  await expect(src.first()).toContainText('2026-10-20 08:45 UTC');
  await expect(panel.getByText('Ready', { exact: true }).first()).toBeVisible();
  // The form is gone until Add a SQL Server is pressed again.
  await expect(panel.getByRole('button', { name: 'Add a SQL Server' })).toBeVisible();
  await expect(panel.getByRole('button', { name: 'Read this day' })).toHaveCount(0);
});

test('GUIDE-22e: NoGrantDb reads Partly ready, names the missing permission with a To fix script and answers no question as a pass', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Step 7: SQL Server');
  await readDay(page, panel, 'NoGrantDb', 'sql');
  await expect(panel.getByText('Partly ready', { exact: true }).first()).toBeVisible();
  await expect(panel.getByText('Cannot read this yet: the login is missing VIEW DATABASE PERFORMANCE STATE.').first()).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'To fix' }).first()).toBeVisible();
  await expect(panel.locator('pre.script').first()).toContainText('USE [NoGrantDb]');
  await expect(panel.getByText(/\bpassed\b/i)).toHaveCount(0);
});

test('GUIDE-22f: EmptyDb reads Cannot test yet, shows Nothing could be read and leaves every question unanswered', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Step 7: SQL Server');
  await readDay(page, panel, 'EmptyDb', 'sql');
  const nothing = panel.locator('[data-nothing-read]');
  await expect(nothing.getByRole('heading', { name: 'Nothing could be read' })).toBeVisible();
  await expect(nothing).toContainText('The case is not broken. Every source answered, but the window held nothing to read.');
  await expect(nothing.getByRole('button', { name: 'Add another day' })).toBeVisible();
  await expect(nothing.getByRole('button', { name: 'Add data', exact: true })).toBeVisible();
  await expect(panel.getByText('No question was answered. A missing answer is never a pass.')).toBeVisible();
  // Three databases are now held, newest first.
  await expect(panel.locator('[data-source]')).toHaveCount(3);
});

test('GUIDE-22g: Step 8 reads Mongo AuditDb as Ready, NoGrantDb as Partly ready with a To fix, and ProfilerOffDb with the Switched on guide', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Step 8: MongoDB');
  await expect(panel.locator('[data-no-source]')).toContainText('No MongoDB server has been read for this case yet.');
  await readDay(page, panel, 'AuditDb', 'mongo');
  await expect(panel.getByText('Ready', { exact: true }).first()).toBeVisible();
  await readDay(page, panel, 'NoGrantDb', 'mongo');
  await expect(panel.getByText('Partly ready', { exact: true }).first()).toBeVisible();
  await expect(panel.getByText('Cannot read this yet: the login is missing clusterMonitor on the admin database').first()).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'To fix' }).first()).toBeVisible();
  await readDay(page, panel, 'ProfilerOffDb', 'mongo');
  await expect(panel.getByText('Partly ready', { exact: true }).first()).toBeVisible();
  await expect(panel.getByText('The profiler is off for this database').first()).toBeVisible();
  await expect(panel.getByRole('heading', { name: 'Switched on?' })).toBeVisible();
  for (const name of ['Check that it is on', 'Switch it on (you run it)', 'Switch it off again']) await expect(panel.getByText(name).first()).toBeVisible();
  // A Mongo read is never offered by day.
  await expect(panel.getByRole('button', { name: 'Add another day' })).toHaveCount(0);
});

test('GUIDE-22h: on a closed case Step 7 has no read form and says the case cannot take new data', async ({ page }) => {
  const panel = await hub(page, CLOSED, 'Step 7: SQL Server');
  await expect(panel.locator('[data-read-only]')).toBeVisible();
  await expect(panel.getByRole('button', { name: 'Read this day' })).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Add a SQL Server' })).toHaveCount(0);
});

async function pickAuditDb(panel: Panel) {
  // The tab loads its days first; only then is the source picker (when several sources are held) and the form there.
  await expect(form(panel)).toBeVisible();
  const pick = panel.getByLabel('Source to read again', { exact: true });
  if (await pick.count()) await pick.selectOption({ label: 'Demo SQL Server · AuditDb' });
  await expect(form(panel).getByLabel('Audit database', { exact: true })).toHaveValue('AuditDb');
}

test('GUIDE-23: Add another day lists what the case holds, shows Mongo as Not by day and prefills the next day with the same reference', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Add another day');
  const day = panel.locator('[data-add-day]');
  await expect(day.getByText('What the case already holds')).toBeVisible();
  await expect(day.locator('li[data-source]').filter({ hasText: 'Demo SQL Server · AuditDb' })).toContainText('Already in the case (UTC): 2026-10-20 08:45 to 2026-10-20 09:00');
  const mongo = day.locator('li[data-source]').filter({ hasText: 'MongoDB' });
  await expect(mongo.first()).toContainText('Not by day');
  await expect(mongo.first()).toContainText('The profiler keeps only the latest entries. An older day cannot be read.');
  await pickAuditDb(panel);
  const f = form(panel);
  await expect(f.getByLabel('Audit database', { exact: true })).toHaveAttribute('readonly', '');
  await expect(f.getByLabel('Incident starts (UTC)', { exact: true })).toHaveValue('2026-10-21 08:45');
  await expect(f.getByLabel('Incident ends (UTC)', { exact: true })).toHaveValue('2026-10-21 09:00');
  // The reference does not move: a later day is compared with the same normal period.
  await expect(f.getByLabel('Reference starts (UTC)', { exact: true })).toHaveValue('2026-10-20 08:30');
  await expect(f.getByLabel('Reference ends (UTC)', { exact: true })).toHaveValue('2026-10-20 08:45');
  await expect(day.locator('[data-reference-stays]')).toContainText('The reference window does not move.');
});

test('GUIDE-23b: the prefilled day is added next to the first one, and the same hours again are allowed without a duplicate', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Add another day');
  await pickAuditDb(panel);
  const day = panel.locator('[data-add-day]');
  const f = form(panel);
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST') posts.push(r.url()); });
  await f.getByRole('button', { name: 'Read this day' }).dblclick();
  await expect(day.locator('[data-said]')).toHaveText('Day added to the case.');
  expect(posts.length).toBe(1);
  const held = day.locator('li[data-source]').filter({ hasText: 'Demo SQL Server · AuditDb' });
  await expect(held).toContainText('2026-10-21 08:45 to 2026-10-21 09:00');
  await expect(held).toContainText('2026-10-20 08:45 to 2026-10-20 09:00');
  // The same hours as the first day again: a re-check, allowed, one more request, and the list does not gain a copy.
  await f.getByLabel('Incident starts (UTC)', { exact: true }).fill('2026-10-20 08:45');
  await f.getByLabel('Incident ends (UTC)', { exact: true }).fill('2026-10-20 09:00');
  await f.getByRole('button', { name: 'Read this day' }).click();
  await expect(day.locator('[data-said]')).toHaveText('Day added to the case.');
  expect(posts.length).toBe(2);
  await expect(held.getByText('Already in the case (UTC): 2026-10-20 08:45 to 2026-10-20 09:00')).toHaveCount(1);
});

test('GUIDE-23c: hours that overlap a day without being the same are refused with nothing sent and the earlier success message is gone', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Add another day');
  await pickAuditDb(panel);
  const day = panel.locator('[data-add-day]');
  const f = form(panel);
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST') posts.push(r.url()); });
  // A good read first so a stale success message could show.
  await f.getByLabel('Incident starts (UTC)', { exact: true }).fill('2026-10-23 08:45');
  await f.getByLabel('Incident ends (UTC)', { exact: true }).fill('2026-10-23 09:00');
  await f.getByRole('button', { name: 'Read this day' }).click();
  await expect(day.locator('[data-said]')).toHaveText('Day added to the case.');
  expect(posts.length).toBe(1);
  const overlap = 'You already added a day for this database that overlaps these times. Use the same start and end to check that day again, or pick times that do not overlap. Nothing was read.';
  for (const [s, e] of [['2026-10-21 08:50', '2026-10-21 09:10'], ['2026-10-21 08:50', '2026-10-21 08:55']]) {
    await f.getByLabel('Incident starts (UTC)', { exact: true }).fill(s);
    await f.getByLabel('Incident ends (UTC)', { exact: true }).fill(e);
    await f.getByRole('button', { name: 'Read this day' }).click();
    await expect(f.locator('[data-problem]')).toHaveText(overlap);
    await expect(page.getByText('Day added to the case.')).toHaveCount(0);
  }
  expect(posts.length).toBe(1);
  // Hours that only touch are not an overlap.
  await f.getByLabel('Incident starts (UTC)', { exact: true }).fill('2026-10-21 09:00');
  await f.getByLabel('Incident ends (UTC)', { exact: true }).fill('2026-10-21 09:15');
  await f.getByRole('button', { name: 'Read this day' }).click();
  await expect(day.locator('[data-said]')).toHaveText('Day added to the case.');
  expect(posts.length).toBe(2);
});

test('GUIDE-23d: a reversed range, a zero-length range, seconds and zone letters are refused and nothing is sent', async ({ page }) => {
  const panel = await hub(page, OPEN, 'Add another day');
  await pickAuditDb(panel);
  const f = form(panel);
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST') posts.push(r.url()); });
  const problem = f.locator('[data-problem]');
  const read = f.getByRole('button', { name: 'Read this day' });
  const tryTimes = async (s: string, e: string) => {
    await f.getByLabel('Incident starts (UTC)', { exact: true }).fill(s);
    await f.getByLabel('Incident ends (UTC)', { exact: true }).fill(e);
    await read.click();
  };
  const range = 'Give the incident a start and an end, with the end after the start.';
  const badTime = 'A time could not be read. Write each one as year-month-day, then hour:minute, like 2026-10-26 10:30.';
  await tryTimes('2026-10-25 09:00', '2026-10-25 08:45');
  await expect(problem).toHaveText(range);
  await tryTimes('2026-10-25 09:00', '2026-10-25 09:00');
  await expect(problem).toHaveText(range);
  await tryTimes('2026-10-25 08:45:30', '2026-10-25 09:00');
  await expect(problem).toHaveText(badTime);
  await tryTimes('2026-10-25 08:45Z', '2026-10-25 09:00');
  await expect(problem).toHaveText(badTime);
  await tryTimes('2026-10-25 08:45', '2026-10-25 09:00 UTC');
  await expect(problem).toHaveText(badTime);
  expect(posts).toEqual([]);
});
