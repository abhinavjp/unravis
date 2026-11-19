// The Blackbox drawer: list, the status pop-up (BL-131), the place wizard and the read key block (demo data only).
import { test, expect, type Page } from '../support/fixtures';

const NAMES = ['web-a', 'web-b', 'web-old', 'web-c', 'web-d', 'web-e', 'web-f', 'web-g', 'old-box'];
const READINESS = ['Ready', 'Partly ready', 'Cannot test yet'];

async function openDrawer(page: Page) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  return dialog;
}

/** The status pop-up of one Blackbox (BL-131): a dialog named by the Blackbox, its body one region with the same name. */
function statusOf(page: Page, name: string) {
  return page.getByRole('dialog', { name: new RegExp('^' + name + '\\b') }).getByRole('region', { name: new RegExp('^' + name + '\\b') });
}

/** Closes the pop-up and waits for the list behind it. */
async function closePop(page: Page, dialog: ReturnType<Page['getByRole']>) {
  await page.locator('dialog.pop[open]').getByRole('button', { name: 'Close', exact: true }).click();
  await expect(page.locator('dialog.pop[open]')).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
}

test('BB-01: the list opens with the nutshell, three buttons, a read-only note and nine rows', async ({ page }) => {
  const dialog = await openDrawer(page);
  await expect(dialog.locator('#blackbox-title')).toHaveText('Blackbox');
  await expect(dialog.locator('.bb-label')).toHaveText('In a nutshell');
  await expect(dialog.getByRole('button', { name: 'Place a Blackbox' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Scan this PC' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Add one by folder, share or address' })).toBeVisible();
  await expect(dialog.locator('.primary-button')).toHaveCount(1);
  await expect(dialog.getByText('Unravis reads your servers. It never changes them.')).toBeVisible();
  const rows = dialog.getByRole('button', { name: /^Open status: / });
  await expect(rows).toHaveCount(9);
  const labels = await rows.evaluateAll((els) => els.map((e) => e.getAttribute('aria-label')));
  expect(labels.sort()).toEqual(NAMES.map((n) => 'Open status: ' + n).sort());
  await expect(dialog.locator('.bb-sticker')).toHaveCount(9);
  for (const s of await dialog.locator('.bb-sticker').allInnerTexts()) expect(s.trim().length, 'a sticker carries a word').toBeGreaterThan(2);
});

test('BB-01a: Esc closes the drawer and it reopens on the same list', async ({ page }) => {
  const dialog = await openDrawer(page);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  const again = await openDrawer(page);
  await expect(again.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
});

test('BB-02: all nine rows show a sticker, one of the three readiness words and a next step, and Close returns to the list', async ({ page }) => {
  const dialog = await openDrawer(page);
  const states: string[] = [];
  for (const name of NAMES) {
    await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
    const region = statusOf(page, name);
    await expect(region).toBeVisible();
    states.push((await page.locator('dialog.pop[open] .bb-pop-title .bb-sticker').innerText()).trim());
    expect(READINESS, name).toContain((await region.locator('.bb-ready strong').first().innerText()).trim());
    expect((await region.locator('p.bb-note').first().innerText()).trim().length, name + ' has a next step').toBeGreaterThan(10);
    await closePop(page, dialog);
  }
  expect(new Set(states).size, 'nine different states: ' + states.join(', ')).toBe(9);
});

test('BB-02a: the demo texts for web-e, web-c and web-d name the cause', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-e' }).click();
  await expect(statusOf(page, 'web-e').getByText('Nothing answered at http://web-e:7788. The server may be off, or the port is blocked.')).toBeVisible();
  await closePop(page, dialog);
  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  await expect(statusOf(page, 'web-c')).toContainText('6%');
  await closePop(page, dialog);
  await dialog.getByRole('button', { name: 'Open status: web-d' }).click();
  await expect(statusOf(page, 'web-d')).toContainText('3.4');
});

test('BB-03: web-a is Running on this PC with its facts and the this-PC buttons only', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const region = statusOf(page, 'web-a');
  const title = page.getByRole('dialog', { name: /^web-a\b/ }).getByRole('heading', { level: 2 });
  await expect(title).toContainText('web-a');
  await expect(title.locator('.bb-sticker')).toHaveText('Running');
  await expect(title.locator('.bb-chip')).toHaveText('this PC');
  for (const fact of ['Reached by', 'Last sample', 'Samples every', 'Recorded', 'Gaps', 'Recordings drive free', 'Host']) {
    await expect(region.locator('dt', { hasText: new RegExp('^' + fact + '$') })).toBeVisible();
  }
  // The scenario lists Start and Stop together; the app shows only the one that fits the state, so Start is absent while running.
  for (const b of ['Stop', 'Apply settings', 'Snapshot now', 'Remove', 'Check again']) {
    await expect(region.getByRole('button', { name: b, exact: true }), b).toBeVisible();
  }
  await expect(region.getByRole('button', { name: 'Start', exact: true })).toHaveCount(0);
  await expect(region.getByRole('button', { name: /Type a new read key/ })).toHaveCount(0);
  await expect(region.getByRole('button', { name: 'Forget this place' })).toHaveCount(0);
  const check = region.getByRole('button', { name: 'Check again', exact: true });
  for (let i = 0; i < 5; i++) await check.click();
  await expect(region.locator('dt', { hasText: /^Gaps$/ })).toHaveCount(1);
});

test('BB-13: the 90 second gap on web-a warns, and the 3 minute gap on web-b is the more serious one', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const a = statusOf(page, 'web-a');
  await expect(a.locator('.bb-ready strong')).toHaveText('Ready');
  await expect(a.locator('dd', { hasText: /gap/ }).first()).toBeVisible();
  await closePop(page, dialog);
  await dialog.getByRole('button', { name: 'Open status: web-b' }).click();
  const b = statusOf(page, 'web-b');
  await expect(b.locator('.bb-ready strong')).toHaveText('Partly ready');
  await expect(b.locator('dd', { hasText: /gap/ }).first()).toBeVisible();
});

test('BB-15: the place pop-up checks the name before it moves on', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  const pop = page.getByRole('dialog', { name: 'What do you call this Blackbox?' });
  const name = pop.getByLabel('Name', { exact: true });
  await expect(name).toBeFocused();
  const next = pop.getByRole('button', { name: 'Next', exact: true });
  const ruleOf = async (value: string, words: string) => {
    await name.fill(value);
    await next.click();
    await expect(pop, value).toContainText(words);
  };
  await ruleOf('', 'Type a name.');
  // the field stops at 63 characters, so 64 can never be typed
  await name.fill('a'.repeat(64));
  await expect(name).toHaveValue('a'.repeat(63));
  await ruleOf('bad name', 'No spaces. Use a dash instead.');
  for (const bad of ['-bad', 'bad_name', '<script>']) await ruleOf(bad, 'Use letters, numbers and dashes only, starting with a letter or number.');
  await expect(pop.locator('script')).toHaveCount(0);
  await name.fill('good-1');
  await next.click();
  await expect(page.getByRole('dialog', { name: 'Where should it run?' })).toBeVisible();
});

test('BB-15a: a name of 63 characters is accepted', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  const pop = page.getByRole('dialog', { name: 'What do you call this Blackbox?' });
  await pop.getByLabel('Name', { exact: true }).fill('a'.repeat(63));
  await pop.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Where should it run?' })).toBeVisible();
});

test('BB-19: the read key is hidden until Show, and Hide puts it away at once', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const region = statusOf(page, 'web-a');
  await expect(region).not.toContainText('ubk-');
  expect(page.url()).not.toContain('ubk-');
  await region.getByRole('button', { name: 'Show', exact: true }).click();
  await expect(region).toContainText('ubk-demo-a');
  await region.getByRole('button', { name: 'Hide', exact: true }).click();
  await expect(region).not.toContainText('ubk-');
});

test('BB-19a: a shown key is hidden again after moving to another row', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const region = statusOf(page, 'web-a');
  await region.getByRole('button', { name: 'Show', exact: true }).click();
  await expect(region).toContainText('ubk-demo-a');
  await closePop(page, dialog);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  await expect(statusOf(page, 'web-a')).not.toContainText('ubk-');
});

test('BB-31: status opens in a pop-up over the list; Esc closes it and focus goes back to the row (BL-131)', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const pop = page.locator('dialog.pop[open]');
  await expect(pop).toHaveCount(1);
  await expect(page.locator('#bb-pop-title')).toBeFocused();
  // Remove opens inside the same pop-up, and Cancel goes back to the status
  await statusOf(page, 'web-a').getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(pop.getByRole('heading', { name: 'Remove web-a' })).toBeVisible();
  await pop.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(statusOf(page, 'web-a')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(pop).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Open status: web-a' })).toBeFocused();
});

test('BB-31a: leaving Apply settings with a change asks first; Keep editing stays, Leave closes (BL-131)', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const pop = page.locator('dialog.pop[open]');
  await statusOf(page, 'web-a').getByRole('button', { name: 'Apply settings', exact: true }).click();
  // no change yet: Esc leaves at once
  await page.keyboard.press('Escape');
  await expect(pop).toHaveCount(0);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  await statusOf(page, 'web-a').getByRole('button', { name: 'Apply settings', exact: true }).click();
  await pop.getByLabel('Keep for (days)').fill('45');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('alertdialog', { name: 'Leave without applying?' })).toBeVisible();
  await page.getByRole('button', { name: 'Keep editing' }).click();
  await expect(pop.getByLabel('Keep for (days)')).toHaveValue('45');
  await pop.getByRole('button', { name: 'Close this Blackbox' }).click();
  await page.getByRole('button', { name: 'Leave', exact: true }).click();
  await expect(pop).toHaveCount(0);
});

test('BB-32: on this PC the Folder step greys out USB and network drives and refuses them (BL-137)', async ({ page, allow }) => {
  // Review asks the server to check the settings; a demo run off Windows cannot read a D:\ path, so that answer may be 422.
  allow(/\/api\/blackbox\/validate/, 'Review check of a Windows path on a non-Windows demo');
  // Drives as Windows would list them; this PC shown as free to place on (the demo has web-a installed here).
  await page.route('**/api/blackbox/this-pc/drives', (r) => r.fulfill({ json: { drives: [
    { letter: 'C', label: 'Windows', type: 'fixed', system: true, freeBytes: 50e9, totalBytes: 250e9 },
    { letter: 'D', label: 'Data', type: 'fixed', system: false, freeBytes: 300e9, totalBytes: 500e9 },
    { letter: 'E', label: 'Stick', type: 'removable', system: false, freeBytes: 10e9, totalBytes: 32e9 },
    { letter: 'Z', label: 'Share', type: 'network', system: false, freeBytes: null, totalBytes: null }
  ] } }));
  const freePc = async (r: import('@playwright/test').Route) => {
    const res = await r.fetch();
    const body = await res.json();
    Object.assign(body.thisPc ?? body, { installed: false, listed: false, running: false });
    await r.fulfill({ response: res, json: body });
  };
  await page.route(/\/api\/blackbox(\?.*)?$/, freePc);
  await page.route('**/api/blackbox/this-pc', freePc);
  await page.reload();
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await page.getByLabel('Name', { exact: true }).fill('web-z');
  const next = page.locator('dialog.pop[open]').getByRole('button', { name: 'Next', exact: true });
  await next.click();
  await page.getByText('On this PC', { exact: true }).click();
  const pop = page.getByRole('dialog', { name: 'Where should recordings be saved?' });
  for (let i = 0; i < 6 && !(await pop.isVisible()); i++) await next.click();
  await expect(pop).toBeVisible();
  for (const letter of ['E', 'Z']) {
    await expect(pop.getByRole('radio', { name: new RegExp('^' + letter + ': ') })).toBeDisabled();
  }
  await expect(pop.getByText('Cannot be used. The Blackbox only records on a local hard drive.')).toHaveCount(2);
  await expect(pop.getByRole('radio', { name: /^D: / })).toBeChecked();
  const path = pop.getByLabel('Folder', { exact: true });
  const refusal = 'The Blackbox only records on a local hard drive. It cannot use a USB drive or a network folder. Pick a local drive.';
  for (const typed of ['E:\\UnravisBlackbox', '\\\\srv\\share\\bb']) {
    await path.fill(typed);
    await next.click();
    await expect(pop.getByRole('alert'), typed).toHaveText(refusal);
  }
  await path.fill('D:\\UnravisBlackbox');
  await next.click();
  await expect(page.getByRole('dialog', { name: 'Check it, then place it' })).toBeVisible();
});
