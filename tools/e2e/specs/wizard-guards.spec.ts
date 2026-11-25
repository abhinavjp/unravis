// The guards of the one Add sources wizard (BL-148 leftovers), on the real demo app and its real case API:
//   WIZG-01  a card that holds typed input asks before "Back to the list" (keep, or leave and lose it); an empty card leaves without a question
//   WIZG-02  a hub tab switch asks when the open card holds typed input, and keeps an unfinished list (and the keyboard way works)
//   WIZG-03  a run that left failures is kept across a tab switch; "Add more" asks before it clears the failures
//   WIZG-04  "Start over" in the create wizard asks first
//   WIZG-05  nothing resets the draft while a run is writing it; the run is still there when the person comes back
//   WIZG-06  a case closed somewhere else is noticed before the draft question, at Review and at the run; nothing is sent to it
//   WIZG-07  a source the case already holds (added somewhere else) is left out, never added twice
//   WIZG-08  a case erased somewhere else is told apart from a closed one
// Another window is played by the API with the same session token: it is the real route another tab would call.
import { test, expect, type Page } from '../support/fixtures';
import { createCase } from '../support/api';
import { goHome, openCase } from '../support/ui';

const hub = (page: Page) => page.locator('[data-sources-hub]');
const tab = (page: Page, name: string) => hub(page).getByRole('tab', { name, exact: true });
const askLeaveForm = (page: Page) => page.getByRole('dialog', { name: 'Leave this form?' });
const askLeaveTab = (page: Page) => page.getByRole('dialog', { name: 'Leave Add data?' });

async function openAddData(page: Page, title: string) {
  await goHome(page);
  await openCase(page, title);
  await tab(page, 'Add data').click();
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();
}

/**
 * Leaves an unfinished new-case draft behind: a name typed in the create wizard, then the browser's Back to the start screen (the guide stays in memory with
 * the name). It used a reload for this before BL-495h; a reload now brings the guide back (WIZR-01), and "Leave the guide" throws the draft away.
 */
async function leaveDraft(page: Page, name: string) {
  page.on('dialog', (d) => void d.accept());
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.getByLabel('Case name').fill(name);
  await page.goBack();
  await expect(page.locator('#start-title')).toBeVisible();
}

/** Opens the Redis card and types an address into it (not added). */
async function typeRedis(page: Page, address: string) {
  await hub(page).getByRole('button', { name: /^Redis/ }).click();
  const field = hub(page).getByLabel(/Redis address/);
  await field.fill(address);
  return field;
}

/** Adds a Redis address to the tray (the card checks the port and adds it, reachable or not). */
async function addRedis(page: Page, address: string) {
  await typeRedis(page, address);
  await hub(page).getByRole('button', { name: /^Check and add/ }).click();
  await expect(hub(page).getByRole('list').getByText(address, { exact: true }).first()).toBeVisible();
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();
}

const tags = async (api: (m: string, p: string, b?: unknown, s?: string) => Promise<{ json: any }>, id: string) => ((await api('GET', `/api/cases/${id}/sources`)).json.sources as Array<{ kind: string; label: string }>);

test('WIZG-01: a card with typed input asks before "Back to the list"; an empty card does not', async ({ page, api }) => {
  const title = 'QA Guard Card';
  const id = await createCase(api, title);
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST' && r.url().includes('/sources')) posts.push(r.url()); });
  await openAddData(page, title);

  // An untouched card goes back at once.
  await hub(page).getByRole('button', { name: /^Redis/ }).click();
  await hub(page).getByRole('button', { name: 'Back to the list' }).click();
  await expect(askLeaveForm(page)).toHaveCount(0);
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();

  // Typed input: the question appears, keeping is the focused choice, Escape means keep.
  const field = await typeRedis(page, 'cache-9:6379');
  const back = hub(page).getByRole('button', { name: 'Back to the list' });
  await back.click();
  const ask = askLeaveForm(page);
  await expect(ask).toBeVisible();
  await expect(ask.getByRole('button', { name: 'Keep editing' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(ask).toHaveCount(0);
  await expect(field).toHaveValue('cache-9:6379');
  await expect(back).toBeFocused();

  // Enter on the focused "Keep editing" keeps too; the backdrop is not a way to lose it.
  await back.click();
  await expect(ask.getByRole('button', { name: 'Keep editing' })).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(ask).toHaveCount(0);
  await expect(field).toHaveValue('cache-9:6379');

  // Leave and lose it: back on the list, nothing added, nothing sent, and the card is empty when opened again.
  await back.click();
  await ask.getByRole('button', { name: 'Leave and lose it' }).click();
  await expect(ask).toHaveCount(0);
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();
  await expect(hub(page).getByText('1 source ready to add')).toHaveCount(0);
  await hub(page).getByRole('button', { name: /^Redis/ }).click();
  await expect(hub(page).getByLabel(/Redis address/)).toHaveValue('');
  expect(posts).toEqual([]);
  expect(await tags(api, id)).toEqual([]);
});

test('WIZG-02: a hub tab switch asks when the card holds typed input, and keeps an unfinished list otherwise', async ({ page, api }) => {
  const title = 'QA Guard Tabs';
  const id = await createCase(api, title);
  await openAddData(page, title);

  // Typed input in an open card: the tab click asks.
  const field = await typeRedis(page, 'cache-8:6379');
  await tab(page, 'Sources').click();
  const ask = askLeaveTab(page);
  await expect(ask).toBeVisible();
  await expect(tab(page, 'Add data')).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Escape');
  await expect(ask).toHaveCount(0);
  await expect(field).toHaveValue('cache-8:6379');

  // The keyboard way asks too: focus the tab row, arrow to the next tab.
  await tab(page, 'Add data').focus();
  await page.keyboard.press('ArrowRight');
  await expect(ask).toBeVisible();
  await ask.getByRole('button', { name: 'Keep editing' }).click();
  await expect(tab(page, 'Add data')).toHaveAttribute('aria-selected', 'true');
  await expect(field).toHaveValue('cache-8:6379');

  // Leave and lose it: the typed address is gone, the tab changed.
  await tab(page, 'Sources').click();
  await ask.getByRole('button', { name: 'Leave and lose it' }).click();
  await expect(tab(page, 'Sources')).toHaveAttribute('aria-selected', 'true');
  await tab(page, 'Add data').click();
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();
  await expect(hub(page).getByText('1 source ready to add')).toHaveCount(0);

  // The keyboard way out: arrow to the next tab, choose "Leave and lose it", and focus lands on the tab that opened (not the old one).
  await typeRedis(page, 'cache-7b:6379');
  await tab(page, 'Add data').focus();
  await page.keyboard.press('ArrowRight');
  await expect(ask).toBeVisible();
  await ask.getByRole('button', { name: 'Leave and lose it' }).click();
  await expect(tab(page, 'Add another day')).toHaveAttribute('aria-selected', 'true');
  await expect(tab(page, 'Add another day')).toBeFocused();
  await tab(page, 'Add data').click();

  // An unfinished list (one source on the tray, no card open) is not asked about and is still there when the person comes back.
  await addRedis(page, 'cache-7:6379');
  await tab(page, 'Sources').click();
  await expect(askLeaveTab(page)).toHaveCount(0);
  await expect(tab(page, 'Sources')).toHaveAttribute('aria-selected', 'true');
  await tab(page, 'Add data').click();
  await expect(hub(page).getByText('1 source ready to add')).toBeVisible();
  await expect(hub(page).getByRole('list').getByText('cache-7:6379', { exact: true }).first()).toBeVisible();

  // Nothing was sent by all that switching, and the list is still sent once when the person finishes.
  expect(await tags(api, id)).toEqual([]);
  await hub(page).getByRole('button', { name: 'Review' }).click();
  await hub(page).getByRole('button', { name: /^Add 1 source/ }).click();
  await expect(tab(page, 'Sources')).toHaveAttribute('aria-selected', 'true');
  await expect.poll(async () => (await tags(api, id)).map((t) => t.label)).toEqual(['cache-7:6379']);

  // A list that ended cleanly is not kept: the tab opens empty.
  await tab(page, 'Add data').click();
  await expect(hub(page).getByText('1 source ready to add')).toHaveCount(0);
});

// WIZG-02b and WIZG-05 are regression checks: they also pass on develop (nothing was kept there, and a run was not reset by these clicks), so they do not
// prove a guard. The reset blocker itself is proven by the unit test in web/tests/wizardGuards.test.cjs.
test('WIZG-02b: leaving the case drops the kept list, so the next case never starts with it', async ({ page, api }) => {
  const title = 'QA Guard Leave A';
  const other = 'QA Guard Leave B';
  await createCase(api, title);
  await createCase(api, other);
  await openAddData(page, title);
  await addRedis(page, 'cache-6:6379');
  await tab(page, 'Sources').click();
  await goHome(page);
  await openCase(page, other);
  await tab(page, 'Add data').click();
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();
  await expect(hub(page).getByText('1 source ready to add')).toHaveCount(0);
  await expect(hub(page).getByText('cache-6:6379')).toHaveCount(0);
  // And the create wizard does not find it as an unfinished new case either.
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByText('Your earlier answers are still here.')).toHaveCount(0);
});

test('WIZG-03: failures of a run stay on screen across a tab switch, and "Add more" asks before clearing them', async ({ page, api, allow }) => {
  allow(/500/, 'the source names are refused on purpose, to leave a failure on screen');
  const title = 'QA Guard Failures';
  const id = await createCase(api, title);
  await page.route('**/api/cases/*/sources', async (route) => {
    if (route.request().method() === 'POST') await route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'the server could not finish', reason: 'failed' }) });
    else await route.continue();
  });
  await openAddData(page, title);
  await addRedis(page, 'cache-5:6379');
  await hub(page).getByRole('button', { name: 'Review' }).click();
  await hub(page).getByRole('button', { name: /^Add 1 source/ }).click();
  const failure = hub(page).getByText(/cache-5:6379: its names were not saved/);
  await expect(failure).toBeVisible();

  // Switch away and back: the failure is still shown, not wiped.
  await tab(page, 'Sources').click();
  await tab(page, 'Add data').click();
  await expect(failure).toBeVisible();
  await expect(hub(page).getByRole('button', { name: 'Open the case anyway' })).toBeVisible();

  // "Add more" would clear it, so it asks. Keeping leaves the failure there.
  await hub(page).getByRole('button', { name: 'Add more files' }).click();
  const ask = page.getByRole('dialog', { name: 'Add more and drop these?' });
  await expect(ask).toBeVisible();
  await expect(ask.getByRole('button', { name: 'Keep this list' })).toBeFocused();
  await ask.getByRole('button', { name: 'Keep this list' }).click();
  await expect(ask).toHaveCount(0);
  await expect(failure).toBeVisible();
  await hub(page).getByRole('button', { name: 'Add more files' }).click();
  await ask.getByRole('button', { name: 'Add more anyway' }).click();
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();
  await expect(failure).toHaveCount(0);
  expect(await tags(api, id)).toEqual([]);
});

test('WIZG-04: "Start over" in the create wizard asks before it clears the name and the sources', async ({ page }) => {
  await leaveDraft(page, 'QA Guard Draft');
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByText('Your earlier answers are still here.')).toBeVisible();
  await page.getByRole('button', { name: 'Start over', exact: true }).click();
  const ask = page.getByRole('dialog', { name: 'Start over?' });
  await expect(ask).toBeVisible();
  await expect(ask.getByRole('button', { name: 'Keep working' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(ask).toHaveCount(0);
  await expect(page.getByText('Your earlier answers are still here.')).toBeVisible();
  await expect(page.getByLabel('Case name')).toHaveValue('QA Guard Draft');
  await page.getByRole('button', { name: 'Start over', exact: true }).click();
  await ask.getByRole('button', { name: 'Start over' }).click();
  await expect(ask).toHaveCount(0);
  await expect(page.getByLabel('Case name')).toHaveValue('');
  await expect(page.getByText('Your earlier answers are still here.')).toHaveCount(0);
});

test('WIZG-05: nothing resets the list while the run writes it, and the run is still there when the person comes back', async ({ page, api }) => {
  const title = 'QA Guard Run';
  const id = await createCase(api, title);
  // The real route, held for a moment so the run is visibly in flight.
  await page.route('**/api/cases/*/sources', async (route) => {
    if (route.request().method() === 'POST') await new Promise((r) => setTimeout(r, 2500));
    await route.continue();
  });
  await openAddData(page, title);
  await addRedis(page, 'cache-4:6379');
  await hub(page).getByRole('button', { name: 'Review' }).click();
  const sent = page.waitForRequest((r) => r.method() === 'POST' && r.url().includes(`/api/cases/${id}/sources`));
  await hub(page).getByRole('button', { name: /^Add 1 source/ }).click();
  await sent;

  // While it runs the tab buttons are off, so no tab switch can drop the list.
  await expect(tab(page, 'Sources')).toBeDisabled();
  await expect(tab(page, 'Add data')).toBeDisabled();

  // The person leaves the case page and opens the create wizard: its Start over is off, the run is not touched.
  await page.getByRole('button', { name: /^All cases/ }).first().click();
  await page.getByRole('button', { name: 'Create a new case' }).click();
  const startOver = page.getByRole('button', { name: 'Start over', exact: true });
  if (await startOver.count()) await expect(startOver).toBeDisabled();

  // The run finishes on its own: the source is in the case exactly once.
  await expect.poll(async () => (await tags(api, id)).map((t) => t.label), { timeout: 15_000 }).toEqual(['cache-4:6379']);
  await page.waitForTimeout(500);
  expect((await tags(api, id)).length).toBe(1);
});

test('WIZG-09: the hub tabs are off while the case is read again after Add is pressed, so the send cannot outlive its tab', async ({ page, api }) => {
  const title = 'QA Guard Hold';
  const id = await createCase(api, title);
  await openAddData(page, title);
  await addRedis(page, 'cache-hold:6379');
  await hub(page).getByRole('button', { name: 'Review' }).click();
  await expect(hub(page).getByRole('heading', { name: 'Review & add' })).toBeVisible();
  // Hold the fresh read of the case that the press makes first.
  let release: () => void = () => undefined;
  const held = new Promise<void>((r) => { release = r; });
  let reads = 0;
  await page.route(new RegExp('/api/cases/' + id + '$'), async (route) => {
    if (route.request().method() === 'GET') { reads++; await held; }
    await route.continue();
  });
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST' && r.url().includes(`/api/cases/${id}/sources`)) posts.push(r.url()); });
  await hub(page).getByRole('button', { name: /^Add 1 source/ }).click();
  await expect.poll(() => reads).toBeGreaterThan(0);
  // While the read is held: every tab is off and the button cannot be pressed again.
  await expect(tab(page, 'Sources')).toBeDisabled();
  await expect(tab(page, 'Add data')).toBeDisabled();
  await expect(hub(page).getByRole('button', { name: /^Add 1 source/ })).toBeDisabled();
  expect(posts).toEqual([]);
  release();
  // The send happens once, on this tab, and the person ends on the list as usual (no bounce on a later visit).
  await expect(tab(page, 'Sources')).toHaveAttribute('aria-selected', 'true');
  await expect.poll(async () => (await tags(api, id)).map((t) => t.label)).toEqual(['cache-hold:6379']);
  expect(posts.length).toBe(1);
  await tab(page, 'Add data').click();
  await expect(hub(page).getByRole('heading', { name: 'What do you want to add to this case?' })).toBeVisible();
  await page.waitForTimeout(500);
  await expect(tab(page, 'Add data')).toHaveAttribute('aria-selected', 'true');
});

test('WIZG-10: the list is held still while the case is read after Add: Back is off, a status says why, and exactly what Review showed is sent', async ({ page, api }) => {
  const title = 'QA Guard Frozen';
  const id = await createCase(api, title);
  await openAddData(page, title);
  await addRedis(page, 'cache-first:6379');
  await hub(page).getByRole('button', { name: 'Review' }).click();
  await expect(hub(page).getByRole('heading', { name: 'Review & add' })).toBeVisible();
  let release: () => void = () => undefined;
  const held = new Promise<void>((r) => { release = r; });
  let reads = 0;
  await page.route(new RegExp('/api/cases/' + id + '$'), async (route) => {
    if (route.request().method() === 'GET') { reads++; await held; }
    await route.continue();
  });
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST' && r.url().includes(`/api/cases/${id}/sources`)) posts.push(r.url()); });
  await hub(page).getByRole('button', { name: /^Add 1 source/ }).click();
  await expect.poll(() => reads).toBeGreaterThan(0);
  // The status explains the grey controls and holds focus; Back cannot be pressed, so the list cannot change.
  const status = hub(page).locator('[data-checking-case]');
  await expect(status).toContainText('Checking this case again before it is sent.');
  await expect(status).toBeFocused();
  await expect(hub(page).getByRole('button', { name: '← Back' })).toBeDisabled();
  expect(posts).toEqual([]);
  release();
  await expect(tab(page, 'Sources')).toHaveAttribute('aria-selected', 'true');
  await expect.poll(async () => (await tags(api, id)).map((t) => t.label)).toEqual(['cache-first:6379']);
  expect(posts.length).toBe(1);
});

test('WIZG-06: a case closed somewhere else is noticed before the draft question, at Review and at the run, and nothing is sent', async ({ page, api }) => {
  // Part 1: an unfinished new-case draft is waiting; the case is closed in "another window" before the Add data tab opens.
  const titleA = 'QA Guard Closed A';
  const idA = await createCase(api, titleA);
  const titleB = 'QA Guard Closed B';
  const idB = await createCase(api, titleB);
  await leaveDraft(page, 'QA Guard Unfinished');
  await openCase(page, titleA);
  expect((await api('POST', `/api/cases/${idA}/close`, undefined, 'open')).status).toBe(200);
  await tab(page, 'Add data').click();
  // No "throw it away" question: this case cannot take data, so the draft is left alone.
  await expect(hub(page).getByText('This case cannot take new data right now: it is closed, or it changed somewhere else.').first()).toBeVisible();
  await expect(hub(page).getByRole('button', { name: 'Throw it away and continue' })).toHaveCount(0);
  await expect(hub(page).getByText('The new case you had not finished is still there. Nothing was thrown away.')).toBeVisible();
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByLabel('Case name')).toHaveValue('QA Guard Unfinished');
  await page.getByRole('button', { name: 'Leave the guide' }).click();
  await page.getByRole('button', { name: 'Leave and discard' }).click();

  // Part 2: a list is on the tray; the case is closed elsewhere; Review notices and sends nothing.
  const posts: string[] = [];
  page.on('request', (r) => { if (r.method() === 'POST' && r.url().includes(`/api/cases/${idB}/sources`)) posts.push(r.url()); });
  await openAddData(page, titleB);
  await addRedis(page, 'cache-3:6379');
  expect((await api('POST', `/api/cases/${idB}/close`, undefined, 'open')).status).toBe(200);
  await hub(page).getByRole('button', { name: 'Review' }).click();
  await expect(hub(page).getByText('This case cannot take new data right now: it is closed, or it changed somewhere else.').first()).toBeVisible();
  await expect(hub(page).getByRole('button', { name: 'Review' })).toBeDisabled();
  // The list is kept (the person may reopen the case), and nothing was sent.
  await expect(hub(page).getByText('1 source ready to add')).toBeVisible();
  expect(posts).toEqual([]);
  expect(await tags(api, idB)).toEqual([]);

  // Part 3: reopened elsewhere, the case takes it again; closed again between Review and the press, the run refuses and sends nothing.
  expect((await api('POST', `/api/cases/${idB}/reopen`, undefined, 'closed')).status).toBe(200);
  await tab(page, 'Sources').click();
  await tab(page, 'Add data').click();
  await hub(page).getByRole('button', { name: 'Review' }).click();
  await expect(hub(page).getByRole('heading', { name: 'Review & add' })).toBeVisible();
  expect((await api('POST', `/api/cases/${idB}/close`, undefined, 'open')).status).toBe(200);
  await hub(page).getByRole('button', { name: /^Add 1 source/ }).click();
  await expect(hub(page).getByText('This case cannot take new data right now: it is closed, or it changed somewhere else.').first()).toBeVisible();
  await expect(hub(page).getByRole('button', { name: /^Add 1 source/ })).toBeDisabled();
  expect(posts).toEqual([]);
  expect(await tags(api, idB)).toEqual([]);
});

test('WIZG-06b: the case is read again when the window gets focus back', async ({ page, api }) => {
  const title = 'QA Guard Focus';
  const id = await createCase(api, title);
  await openAddData(page, title);
  await addRedis(page, 'cache-2:6379');
  await page.waitForTimeout(1700);
  expect((await api('POST', `/api/cases/${id}/close`, undefined, 'open')).status).toBe(200);
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(hub(page).getByText('This case cannot take new data right now: it is closed, or it changed somewhere else.').first()).toBeVisible();
  await expect(hub(page).getByRole('button', { name: 'Review' })).toBeDisabled();
  await expect(hub(page).getByRole('button', { name: /^Redis/ })).toBeDisabled();
});

test('WIZG-07: a source the case already holds is left out of the list, never added twice', async ({ page, api }) => {
  const title = 'QA Guard Twice';
  const id = await createCase(api, title);
  await openAddData(page, title);
  await addRedis(page, 'cache-1:6379');
  await addRedis(page, 'cache-0:6379');
  // Another window adds cache-1 to the case while this list is open.
  const other = await api('POST', `/api/cases/${id}/sources`, { importId: 0, kind: 'redis', label: 'cache-1:6379', env: '', portal: '', api: '', classification: '' });
  expect(other.status).toBe(201);

  // Review notices: cache-1 is left out with a line saying so, cache-0 stays.
  await hub(page).getByRole('button', { name: 'Review' }).click();
  const note = hub(page).locator('[data-left-out]');
  await expect(note).toContainText('cache-1:6379 is already in this case, so it was left out of your list.');
  await expect(hub(page).getByRole('heading', { name: 'Review & add' })).toBeVisible();
  await expect(hub(page).getByRole('listitem').filter({ hasText: 'cache-0:6379' })).toHaveCount(1);
  await expect(hub(page).getByRole('listitem').filter({ hasText: 'cache-1:6379' })).toHaveCount(0);

  // A second one slips in between Review and the press: the press leaves it out and sends nothing yet.
  const second = await api('POST', `/api/cases/${id}/sources`, { importId: 0, kind: 'redis', label: 'cache-0:6379', env: '', portal: '', api: '', classification: '' });
  expect(second.status).toBe(201);
  const before = (await tags(api, id)).length;
  await hub(page).getByRole('button', { name: /^Add 1 source/ }).click();
  await expect(note).toContainText('cache-0:6379');
  await expect(hub(page).getByText('1 source ready to add')).toHaveCount(0);
  expect((await tags(api, id)).length).toBe(before);
  await note.getByRole('button', { name: 'Dismiss' }).click();
  await expect(note).toHaveCount(0);
  // The case holds each of them once.
  expect((await tags(api, id)).map((t) => t.label).sort()).toEqual(['cache-0:6379', 'cache-1:6379']);
});

test('WIZG-08: a case erased somewhere else says so, and is not called closed', async ({ page, api, allow }) => {
  allow(/404/, 'the case was erased on purpose, so its reads answer not found');
  const title = 'QA Guard Erased';
  const id = await createCase(api, title);
  await openAddData(page, title);
  await addRedis(page, 'cache-0:6379');
  await page.waitForTimeout(1700);
  expect((await api('POST', `/api/cases/${id}/close`, undefined, 'open')).status).toBe(200);
  expect((await api('DELETE', `/api/cases/${id}`, { confirm: id.slice(0, 8) }, 'closed')).status).toBe(204);
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(hub(page).getByText('This case is not on this computer any more.')).toBeVisible();
  await expect(hub(page).getByRole('button', { name: 'Review' })).toBeDisabled();
});
