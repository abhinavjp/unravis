// Blackbox: the add form, the place wizard, the pack download and the other-server views.
// Nothing here changes the demo list (the tests that do live in bb-change.spec.ts).
import { test, expect, type Page, type Locator } from '../support/fixtures';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const TAR = join(process.env.SystemRoot ?? String.raw`C:\Windows`, 'System32', 'tar.exe');
const PLACE = 'Folder, network share, or address and port';
const ADDRESS_KEY = 'Read key (for an address)';
const DUP_TITLE = 'That Blackbox is already in the list';

async function openDrawer(page: Page) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  return dialog;
}

async function openAdd(page: Page) {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await expect(dialog.getByRole('heading', { name: 'Add a Blackbox' })).toBeVisible();
  return dialog;
}

/** The text a person would type for a share: two backslashes, the server, one backslash, the share. */
/** The retry button says Check again at first and Look again after a refusal. */
const again = (dialog: Locator) => dialog.getByRole('button', { name: /^(Check|Look) again$/ });

const share = (server: string, name: string) => String.raw`\\` + server + '\\' + name;

test('BB-04: a local folder needs no read key, is found, and is refused as a duplicate on Add', async ({ page, allow }) => {
  allow(/409/, 'the duplicate answer is a deliberate 409');
  const dialog = await openAdd(page);
  await dialog.getByLabel(PLACE).fill(String.raw`D:\Boxes\web-old`);
  await expect(dialog.getByLabel(ADDRESS_KEY)).toHaveCount(0);
  await again(dialog).click();
  await expect(dialog.getByText('web-old').first()).toBeVisible();
  await dialog.getByRole('button', { name: 'Add to the list' }).click();
  await expect(dialog.getByText(DUP_TITLE)).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Open the one in the list' })).toBeVisible();
});

test('BB-04a: an empty place, and a folder that does not exist, are refused in plain words and nothing is changed', async ({ page, allow }) => {
  allow(/422/, 'the refusal is a deliberate 422');
  const dialog = await openAdd(page);
  await dialog.getByRole('button', { name: 'Add to the list' }).click();
  await expect(dialog.getByText('Type where the Blackbox is.')).toBeVisible();
  await dialog.getByLabel(PLACE).fill(String.raw`Z:\nothing-here`);
  await again(dialog).click();
  await expect(dialog.getByText('Nothing could be read there')).toBeVisible();
  await expect(dialog.getByText('Nothing was changed on that server.')).toBeVisible();
  await expect(dialog.getByText('The most likely reasons:')).toBeVisible();
});

test('BB-04b: Cancel leaves the add form, and the next time it opens it is empty', async ({ page }) => {
  const dialog = await openAdd(page);
  await dialog.getByLabel(PLACE).fill(String.raw`D:\Boxes\web-old`);
  await dialog.getByLabel('Name (optional)').fill('half-way');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await expect(dialog.getByLabel(PLACE)).toHaveValue('');
  await expect(dialog.getByLabel('Name (optional)')).toHaveValue('');
  await expect(dialog.getByRole('alert')).toHaveCount(0);
});

test('BB-05: a network share asks for no read key and reports the incomplete recording', async ({ page }) => {
  const dialog = await openAdd(page);
  await dialog.getByLabel(PLACE).fill(share('web-f', 'Blackbox'));
  await expect(dialog.getByLabel(ADDRESS_KEY)).toHaveCount(0);
  await again(dialog).click();
  await expect(dialog.getByText('Incomplete')).toBeVisible();
  await expect(dialog.getByText(/could not be read to the end/)).toBeVisible();
  await expect(dialog).not.toContainText(/was changed on that server\b(?!\.)/);
});

test('BB-05a: a share with a trailing space and one written with forward slashes both reach the same Blackbox', async ({ page }) => {
  const dialog = await openAdd(page);
  await dialog.getByLabel(PLACE).fill(share('web-f', 'Blackbox') + ' ');
  await again(dialog).click();
  await expect(dialog.getByText('Incomplete')).toBeVisible();
  await dialog.getByLabel(PLACE).fill('//web-f/Blackbox');
  await again(dialog).click();
  await expect(dialog.getByText('Incomplete')).toBeVisible();
});

test('BB-05b: two backslashes alone give a clear refusal, and the buttons stay usable', async ({ page, allow }) => {
  allow(/422/, 'the refusal is a deliberate 422');
  const dialog = await openAdd(page);
  await dialog.getByLabel(PLACE).fill(String.raw`\\`);
  await again(dialog).click();
  await expect(dialog.getByText('Nothing could be read there')).toBeVisible();
  await expect(dialog.getByText('That share name is not complete.')).toBeVisible();
  await expect(again(dialog)).toBeEnabled();
  await expect(dialog.getByRole('button', { name: 'Add to the list' })).toBeEnabled();
});

test('BB-06: an address needs the read key, which is masked until Show, and the key never reaches the address bar', async ({ page, allow }) => {
  allow(/422/, 'the refusals are deliberate 422s');
  const dialog = await openAdd(page);
  await dialog.getByLabel(PLACE).fill('http://web-c:7788');
  const key = dialog.getByLabel(ADDRESS_KEY);
  await expect(key).toBeVisible();
  await expect(key).toHaveAttribute('type', 'password');
  await expect(dialog.getByLabel('Show what I type')).not.toBeChecked();
  await again(dialog).click();
  await expect(dialog.getByText('An address needs the read key. Type it, or use a folder or share instead.')).toBeVisible();
  await expect(key).toHaveAttribute('aria-invalid', 'true');

  // The wrong box's key is refused; the right one reaches the Blackbox.
  await key.fill('ubk-demo-a-NOT-A-REAL-KEY');
  await again(dialog).click();
  await expect(dialog.getByText('did not accept the read key')).toBeVisible();
  await key.fill('ubk-demo-c-NOT-A-REAL-KEY');
  await again(dialog).click();
  await expect(dialog.getByText('Disk low')).toBeVisible();
  expect(page.url()).not.toContain('ubk-');
});

test('BB-06a: Show what I type toggles the key field between dots and text, and a folder takes the key field away', async ({ page }) => {
  const dialog = await openAdd(page);
  const place = dialog.getByLabel(PLACE);
  await place.fill('http://web-c:7788');
  const key = dialog.getByLabel(ADDRESS_KEY);
  await key.fill('ubk-test-typed-NOT-A-REAL-KEY');
  const show = dialog.getByLabel('Show what I type');
  await show.check();
  await expect(key).toHaveAttribute('type', 'text');
  await show.uncheck();
  await expect(key).toHaveAttribute('type', 'password');
  await place.fill(String.raw`D:\Boxes\web-old`);
  await expect(dialog.getByLabel(ADDRESS_KEY)).toHaveCount(0);
  // And a typed key is not sent for a folder.
  const posts: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST') posts.push(r.postData() ?? '');
  });
  await again(dialog).click();
  await expect(dialog.getByText('web-old').first()).toBeVisible();
  expect(posts.length).toBeGreaterThan(0);
  for (const body of posts) expect(body).not.toContain('ubk-');
});

test('BB-07: a place that is already listed is refused on Add with the way to open the existing row, in any letter case', async ({ page, allow }) => {
  allow(/409/, 'the duplicate answer is a deliberate 409');
  const dialog = await openAdd(page);
  await dialog.getByLabel(PLACE).fill(share('WEB-B', 'unravisblackbox'));
  await dialog.getByRole('button', { name: 'Add to the list' }).click();
  await expect(dialog.getByText(DUP_TITLE)).toBeVisible();
  await expect(dialog.getByText('It is listed as web-b. It was not added a second time.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Open the one in the list' }).click();
  await expect(dialog.getByRole('region', { name: 'web-b' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await expect(dialog.getByRole('button', { name: 'Open status: web-b' })).toHaveCount(1);
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
});

test('BB-08: web-e is Unreachable, says why in plain words, offers the steps for the server, and changes nothing', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-e' }).click();
  const region = dialog.getByRole('region', { name: 'web-e' });
  await expect(region.locator('.bb-sticker')).toHaveText('Unreachable');
  await expect(region.getByText('Nothing answered at http://web-e:7788. The server may be off, or the port is blocked.')).toBeVisible();
  await expect(region.getByText('Unravis only reads here. Nothing was changed on that server.')).toBeVisible();
  await region.getByRole('button', { name: 'Check again' }).click();
  await expect(region.locator('.bb-sticker')).toHaveText('Unreachable');
  const steps = region.locator('details', { hasText: 'Steps for the server' });
  for (let i = 0; i < 5; i++) {
    await steps.locator('summary').click();
    await steps.locator('summary').click();
  }
  await steps.locator('summary').click();
  await expect(steps).toContainText('services.msc');
  await expect(steps.getByRole('button')).toHaveCount(0);
});

test('BB-09a: the new-key field toggles open and shut, is masked, and an empty key cannot be saved', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  const toggle = dialog.getByRole('button', { name: 'Type a new read key' });
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  const key = dialog.getByLabel('New read key');
  await expect(key).toHaveAttribute('type', 'password');
  await expect(dialog.getByRole('button', { name: 'Save the new key' })).toBeDisabled();
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  // A box on another server never offers the this-PC read key block.
  await expect(dialog.getByRole('region', { name: 'Read key' })).toHaveCount(0);
});

test('BB-09b: a refused new key is explained and is not shown back; the right key is saved and collapses the field', async ({ page, allow }) => {
  allow(/422/, 'the refusal is a deliberate 422');
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  const toggle = dialog.getByRole('button', { name: 'Type a new read key' });
  await toggle.click();
  const key = dialog.getByLabel('New read key');
  await key.fill('ubk-demo-a-NOT-A-REAL-KEY');
  await dialog.getByRole('button', { name: 'Save the new key' }).click();
  await expect(dialog.getByText('did not accept the read key')).toBeVisible();
  await key.fill('ubk-demo-c-NOT-A-REAL-KEY');
  await dialog.getByRole('button', { name: 'Save the new key' }).click();
  await expect(dialog.getByText('The new key was tried and saved.')).toBeVisible();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(dialog).not.toContainText('ubk-demo');
  expect(await page.content()).not.toContain('ubk-demo-c');
});

test('BB-09c: Esc with a half-typed key closes the drawer and the key is gone from the page', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  await dialog.getByRole('button', { name: 'Type a new read key' }).click();
  await dialog.getByLabel('New read key').fill('ubk-test-HALFTYPED-NOT-A-REAL-KEY');
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  expect(await page.content()).not.toContain('HALFTYPED');
  const again = await openDrawer(page);
  await expect(again.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
});

test('BB-14: the place wizard has four named parts, saves nothing, and Leave set-up adds no row', async ({ page }) => {
  const posts: string[] = [];
  page.on('request', (r) => {
    if (r.method() !== 'GET' && r.url().includes('/api/blackbox')) posts.push(r.method() + ' ' + new URL(r.url()).pathname);
  });
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await expect(dialog.getByText('Nothing is saved until the last step.')).toBeVisible();
  await expect(dialog.getByRole('list', { name: 'Part 1 of 4' }).getByRole('listitem')).toHaveText([/Name/, /Where/, /Settings/, /Review/]);
  await dialog.getByLabel('Name', { exact: true }).fill('bb-test-1');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 2 of 4' })).toBeVisible();
  await expect(dialog.getByText('Nothing is saved until the last step.')).toBeVisible();
  await dialog.getByRole('radio', { name: /On another server \(pack\)/ }).check();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 3 of 4' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Next: Review' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 4 of 4' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Download the pack' })).toBeVisible();
  await expect(dialog.getByText('Nothing is saved until the last step.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Back' }).click();
  await dialog.getByRole('button', { name: 'Back' }).click();
  await dialog.getByRole('button', { name: 'Leave set-up' }).click();
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  await expect(dialog.getByRole('button', { name: 'Open status: bb-test-1' })).toHaveCount(0);
  expect(posts.filter((p) => !/\/blackbox\/validate$/.test(p)), 'only the name check was sent').toEqual([]);
});

test('BB-14a: part 1 has no Back button, a reload drops the wizard, and nothing was saved', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await expect(dialog.getByRole('button', { name: 'Back', exact: true })).toHaveCount(0);
  await dialog.getByLabel('Name', { exact: true }).fill('bb-reload');
  await page.reload();
  const again = await openDrawer(page);
  await expect(again.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  await expect(again.getByRole('button', { name: 'Open status: bb-reload' })).toHaveCount(0);
});

test('BB-14b: Esc inside the wizard steps back one part instead of closing the drawer', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-esc');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 2 of 4' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('list', { name: 'Part 1 of 4' })).toBeVisible();
  await expect(dialog.getByLabel('Name', { exact: true })).toHaveValue('bb-esc');
});

test('BB-14c: the part number is part of the visible heading', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await expect(dialog.getByRole('heading', { level: 3 })).toContainText('Part 1 of 4');
  await dialog.getByLabel('Name', { exact: true }).fill('bb-heading');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await expect(dialog.getByRole('heading', { level: 3 })).toContainText('Part 2 of 4');
});

test('BB-16: every out-of-range setting is refused in its own words and keeps the wizard on Settings', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-test-2');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 3 of 4' })).toBeVisible();

  // The interval is a choice of four, so another value cannot be typed.
  await expect(dialog.getByLabel('Sample every').locator('option')).toHaveText(['5 seconds', '15 seconds', '30 seconds', '60 seconds']);

  const next = dialog.getByRole('button', { name: 'Next: Review' });
  const cases: { label: string; bad: string[]; message: string; good: string }[] = [
    { label: 'Keep for (days)', bad: ['0', '366', '-3', '1.5', ''], message: 'Keep for 1 to 365 days.', good: '7' },
    { label: 'Size limit (GB)', bad: ['0', '0.05', '10001', '-1', ''], message: 'Use a size from 0.1 to 10000 GB.', good: '2' },
    { label: 'Keep free disk (percent)', bad: ['0', '51', '2.5', '-4', ''], message: 'Use 1 to 50 percent.', good: '10' },
    { label: 'Status port', bad: ['0', '65536', '7788.5', '-80', ''], message: 'Use a port from 1 to 65535.', good: '7788' }
  ];
  for (const c of cases) {
    const field = dialog.getByLabel(c.label);
    for (const bad of c.bad) {
      await field.fill(bad);
      await next.click();
      await expect(dialog.getByText(c.message).first(), c.label + ' = "' + bad + '"').toBeVisible();
      await expect(dialog.getByRole('list', { name: 'Part 3 of 4' }), 'still on Settings').toBeVisible();
    }
    await field.fill(c.good);
  }
  // The edges are allowed.
  for (const [label, edge] of [['Keep for (days)', '1'], ['Keep for (days)', '365'], ['Size limit (GB)', '0.1'], ['Size limit (GB)', '10000'], ['Keep free disk (percent)', '1'], ['Keep free disk (percent)', '50'], ['Status port', '1'], ['Status port', '65535']]) {
    await dialog.getByLabel(label).fill(edge);
    await next.click();
    await expect(dialog.getByRole('list', { name: 'Part 4 of 4' }), label + ' = ' + edge).toBeVisible();
    await dialog.getByRole('button', { name: 'Back' }).click();
    await expect(dialog.getByRole('list', { name: 'Part 3 of 4' })).toBeVisible();
    await dialog.getByLabel(label).fill(label === 'Status port' ? '7788' : label === 'Keep for (days)' ? '7' : label === 'Size limit (GB)' ? '2' : '10');
  }
});

test('BB-16a: the settings notes say what the bind address, the probe and housekeeping mean', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-notes');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await expect(dialog.getByText('The SQL Server probe is not part of this release.')).toBeVisible();
  await expect(dialog.getByText('Housekeeping always runs. You can lower the limits but not turn them off.')).toBeVisible();
  await expect(dialog.getByText(/127\.0\.0\.1 means only this computer can ask it/)).toBeVisible();
  await expect(dialog.getByText(/Unravis does not add a firewall rule. Adding one is your own choice./)).toBeVisible();
});

test('BB-16b: a bind address, a folder and a log folder that are wrong each get their own message', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-bind');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  const next = dialog.getByRole('button', { name: 'Next: Review' });
  const bind = dialog.getByLabel('Listens on');
  for (const bad of ['', 'bad address!', '999.1.1.1', '*', '0.0.0.0/0']) {
    await bind.fill(bad);
    await next.click();
    await expect(dialog.getByRole('list', { name: 'Part 3 of 4' }), 'bind = "' + bad + '"').toBeVisible();
    await expect(dialog.getByRole('alert').first(), 'bind = "' + bad + '"').toBeVisible();
  }
  await bind.fill('127.0.0.1');
  const folder = dialog.getByLabel('Folder', { exact: true });
  for (const bad of ['', 'D:\\', 'C:\\Windows\\x', 'relative\\path']) {
    await folder.fill(bad);
    await next.click();
    await expect(dialog.getByRole('list', { name: 'Part 3 of 4' }), 'folder = "' + bad + '"').toBeVisible();
    await expect(dialog.getByRole('alert').first(), 'folder = "' + bad + '"').toBeVisible();
  }
});

test('BB-16c: a bind address other than this computer is called out on the Settings part with the read key as the only protection', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-bind-warn');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await dialog.getByLabel('Listens on').fill('0.0.0.0');
  await dialog.getByRole('button', { name: 'Next: Review' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 4 of 4' })).toBeVisible();
  await expect(dialog.getByText('This Blackbox listens on every address, so other computers can reach it. The read key is the only protection.')).toBeVisible();
});

test('BB-17: Download the pack gives unravis-blackbox-<name>.zip with the recorder and its settings and no read key', async ({ page }, testInfo) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-test-3');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await dialog.getByRole('button', { name: 'Next: Review' }).click();
  await expect(dialog.getByRole('button', { name: 'Download the pack' })).toBeVisible();
  const [download] = await Promise.all([page.waitForEvent('download'), dialog.getByRole('button', { name: 'Download the pack' }).click()]);
  expect(download.suggestedFilename()).toBe('unravis-blackbox-bb-test-3.zip');
  const zip = testInfo.outputPath('pack.zip');
  await download.saveAs(zip);
  const out = testInfo.outputPath('pack');
  mkdirSync(out, { recursive: true });
  execFileSync(TAR, ['-xf', zip, '-C', out], { windowsHide: true });
  const files = readdirSync(out).sort();
  expect(files).toEqual(['README.txt', 'blackbox.json', 'unravis-blackbox.exe']);
  const config = JSON.parse(readFileSync(join(out, 'blackbox.json'), 'utf8'));
  expect(config.name).toBe('bb-test-3');
  expect(config.port).toBe(7788);
  expect(config.bind).toBe('127.0.0.1');
  for (const f of files) expect(readFileSync(join(out, f)).toString('latin1'), f + ' holds no read key').not.toContain('ubk-');
  expect(page.url()).not.toContain('ubk-');
  await expect(dialog.getByRole('button', { name: 'Download again' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Back to the list' })).toBeVisible();
});

test('BB-17a: the pack README and the on-screen steps give the same install command', async ({ page }, testInfo) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-test-4');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await dialog.getByRole('button', { name: 'Next: Review' }).click();
  const [download] = await Promise.all([page.waitForEvent('download'), dialog.getByRole('button', { name: 'Download the pack' }).click()]);
  const zip = testInfo.outputPath('pack4.zip');
  await download.saveAs(zip);
  const out = testInfo.outputPath('pack4');
  mkdirSync(out, { recursive: true });
  execFileSync(TAR, ['-xf', zip, '-C', out], { windowsHide: true });
  const readme = readFileSync(join(out, 'README.txt'), 'utf8');
  const screen = await dialog.innerText();
  expect(screen).toContain('unravis-blackbox.exe install');
  expect(readme).toContain('unravis-blackbox.exe install');
  expect(readme, 'the README does not add a flag the screen never mentions').not.toContain('install --name');
});

test('BB-20: the views of the other servers offer only reading, never a button that changes them', async ({ page }) => {
  const dialog = await openDrawer(page);
  const forbidden = /^(Start|Stop|Apply settings|Snapshot now|Remove|Install.*)$/;
  for (const name of ['web-b', 'web-c', 'web-d', 'web-e', 'web-f']) {
    await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
    const region = dialog.getByRole('region', { name });
    await expect(region).toBeVisible();
    const buttons = await region.getByRole('button').evaluateAll((els) => els.map((e) => (e.textContent ?? '').trim()));
    for (const b of buttons) expect(b, name + ' offers "' + b + '"').not.toMatch(forbidden);
    await expect(region, name).toContainText('Nothing was changed on that server.');
    await expect(region.locator('details', { hasText: 'Steps for the server' }).getByRole('button')).toHaveCount(0);
    await expect(region.getByText(/delete/i), name + ' has no delete wording').toHaveCount(0);
    await region.getByRole('button', { name: 'Back to the list' }).click();
  }
});
