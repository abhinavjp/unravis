// The Blackbox drawer: list, status views, the place wizard and the read key block (demo data only).
import { test, expect, type Page } from '../support/fixtures';

const NAMES = ['web-a', 'web-b', 'web-old', 'web-c', 'web-d', 'web-e', 'web-f', 'web-g', 'old-box'];
const READINESS = ['Ready', 'Partly ready', 'Cannot test yet'];

async function openDrawer(page: Page) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  return dialog;
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

test('BB-02: all nine rows show a sticker, one of the three readiness words and a next step, and Back returns to the list', async ({ page }) => {
  const dialog = await openDrawer(page);
  const states: string[] = [];
  for (const name of NAMES) {
    await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
    const region = dialog.getByRole('region', { name });
    await expect(region).toBeVisible();
    states.push((await region.locator('.bb-sticker').first().innerText()).trim());
    expect(READINESS, name).toContain((await region.locator('.bb-ready strong').first().innerText()).trim());
    expect((await region.locator('p.bb-note').first().innerText()).trim().length, name + ' has a next step').toBeGreaterThan(10);
    await region.getByRole('button', { name: 'Back to the list' }).click();
    await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  }
  expect(new Set(states).size, 'nine different states: ' + states.join(', ')).toBe(9);
});

test('BB-02a: the demo texts for web-e, web-c and web-d name the cause', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-e' }).click();
  await expect(dialog.getByText('Nothing answered at http://web-e:7788. The server may be off, or the port is blocked.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  await expect(dialog.getByRole('region', { name: 'web-c' })).toContainText('6 %');
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await dialog.getByRole('button', { name: 'Open status: web-d' }).click();
  await expect(dialog.getByRole('region', { name: 'web-d' })).toContainText('3.4');
});

test('BB-03: web-a is Running on this PC with its facts and the this-PC buttons only', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const region = dialog.getByRole('region', { name: 'web-a' });
  await expect(region.getByRole('heading', { level: 3 })).toContainText('web-a');
  await expect(region.locator('.bb-sticker')).toHaveText('Running');
  await expect(region.locator('.bb-chip')).toHaveText('this PC');
  for (const fact of ['Reached by', 'Last sample', 'Samples every', 'Recorded', 'Gaps', 'Disk free', 'Host']) {
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
  const a = dialog.getByRole('region', { name: 'web-a' });
  await expect(a.locator('.bb-ready strong')).toHaveText('Ready');
  await expect(a.locator('dd', { hasText: /gap/ }).first()).toBeVisible();
  await a.getByRole('button', { name: 'Back to the list' }).click();
  await dialog.getByRole('button', { name: 'Open status: web-b' }).click();
  const b = dialog.getByRole('region', { name: 'web-b' });
  await expect(b.locator('.bb-ready strong')).toHaveText('Partly ready');
  await expect(b.locator('dd', { hasText: /gap/ }).first()).toBeVisible();
});

test('BB-15: the place wizard checks the name before it moves on', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await expect(dialog.getByText('Nothing is saved until the last step.')).toBeVisible();
  const name = dialog.getByLabel('Name', { exact: true });
  const next = dialog.getByRole('button', { name: 'Next: Where' });
  const ruleOf = async (value: string) => {
    await name.fill(value);
    await next.click();
    return dialog.locator('.bb-wizard');
  };
  let wiz = await ruleOf('');
  await expect(wiz).toContainText('Type a name.');
  wiz = await ruleOf('a'.repeat(64));
  await expect(wiz).toContainText('Use 63 characters or fewer.');
  for (const bad of ['-bad', 'bad name', 'bad_name', '<script>']) {
    wiz = await ruleOf(bad);
    await expect(wiz, bad).toContainText('Use letters, numbers and dashes only, starting with a letter or number.');
    await expect(dialog.locator('script')).toHaveCount(0);
  }
  await name.fill('good-1');
  await next.click();
  await expect(dialog.getByRole('list', { name: 'Part 2 of 4' })).toBeVisible();
});

test('BB-15a: a name of 63 characters is accepted', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('a'.repeat(63));
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 2 of 4' })).toBeVisible();
});

test('BB-19: the read key is hidden until Show, and Hide puts it away at once', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  const region = dialog.getByRole('region', { name: 'web-a' });
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
  const region = dialog.getByRole('region', { name: 'web-a' });
  await region.getByRole('button', { name: 'Show', exact: true }).click();
  await expect(region).toContainText('ubk-demo-a');
  await region.getByRole('button', { name: 'Back to the list' }).click();
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  await expect(dialog.getByRole('region', { name: 'web-a' })).not.toContainText('ubk-');
});
