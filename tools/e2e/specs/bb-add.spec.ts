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

/** The status pop-up of one Blackbox (BL-131): its body is one region named by the Blackbox. */
const statusOf = (page: Page, name: string) => page.locator('dialog.pop[open]').getByRole('region', { name: new RegExp('^' + name + '\\b') });
/** The state sticker, which sits in the pop-up's title. */
const popSticker = (page: Page) => page.locator('dialog.pop[open] .bb-pop-title .bb-sticker');
async function closePop(page: Page) {
  await page.locator('dialog.pop[open]').getByRole('button', { name: 'Close', exact: true }).click();
  await expect(page.locator('dialog.pop[open]')).toHaveCount(0);
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
  await expect(dialog.getByText('did not accept the read key').first()).toBeVisible();
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
  await expect(statusOf(page, 'web-b')).toBeVisible();
  await closePop(page);
  await expect(dialog.getByRole('button', { name: 'Open status: web-b' })).toHaveCount(1);
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
});

test('BB-08: web-e is Unreachable, says why in plain words, offers the steps for the server, and changes nothing', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-e' }).click();
  const region = statusOf(page, 'web-e');
  await expect(popSticker(page)).toHaveText('Unreachable');
  await expect(region.getByText('Nothing answered at http://web-e:7788. The server may be off, or the port is blocked.')).toBeVisible();
  await expect(region.getByText('Unravis only reads here. Nothing was changed on that server.')).toBeVisible();
  await region.getByRole('button', { name: 'Check again' }).click();
  await expect(popSticker(page)).toHaveText('Unreachable');
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

test('BB-09c: Esc with a half-typed key closes the status pop-up, a second Esc the drawer, and the key is gone from the page', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  await dialog.getByRole('button', { name: 'Type a new read key' }).click();
  await dialog.getByLabel('New read key').fill('ubk-test-HALFTYPED-NOT-A-REAL-KEY');
  await page.keyboard.press('Escape');
  await expect(page.locator('dialog.pop[open]')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  expect(await page.content()).not.toContain('HALFTYPED');
  const again = await openDrawer(page);
  await expect(again.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
});

/* The Place pop-up (PR #13, BL-130). Steps: Name, Where, Record, SQL, Redis, Watch, Keep, Folder, Review. */
const pop = (page: Page) => page.locator('dialog.pop[open]');
const stepTitle = (page: Page) => pop(page).locator('h2').first();

/** Opens Place, types the name and goes on to Where. */
async function placeTo(page: Page, name: string, where: 'pack' | 'pc' = 'pack') {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await expect(stepTitle(page)).toHaveText('What do you call this Blackbox?');
  await pop(page).getByLabel('Name', { exact: true }).fill(name);
  await pop(page).getByRole('button', { name: 'Next', exact: true }).click();
  await expect(stepTitle(page)).toHaveText('Where should it run?');
  await page.getByText(where === 'pack' ? 'On another server' : 'On this PC', { exact: true }).click();
  return dialog;
}

/** Presses Next until the step with this title shows. */
async function nextUntil(page: Page, title: string) {
  for (let i = 0; i < 9 && (await stepTitle(page).innerText()) !== title; i++) {
    await pop(page).getByRole('button', { name: 'Next', exact: true }).click();
  }
  await expect(stepTitle(page)).toHaveText(title);
}

test('BB-14: the place pop-up has nine named steps, saves nothing, and Leave adds no row', async ({ page, allow }) => {
  // Review asks the server to check the settings; a demo run off Windows cannot read a D:\ path, so that answer may be 422.
  allow(/\/api\/blackbox\/validate/, 'Review check of a Windows path on a non-Windows demo');
  const posts: string[] = [];
  page.on('request', (r) => {
    if (r.method() !== 'GET' && r.url().includes('/api/blackbox')) posts.push(r.method() + ' ' + new URL(r.url()).pathname);
  });
  const dialog = await placeTo(page, 'bb-test-1');
  const rail = await pop(page).getByRole('button', { name: /, step \d/ }).evaluateAll((els) => els.map((e) => (e.getAttribute('aria-label') ?? '').replace(/, skipped$/, '')));
  expect(rail).toEqual(['Name, step 1', 'Where, step 2', 'Record, step 3', 'SQL, step 4', 'Redis, step 5', 'Watch, step 6', 'Keep, step 7', 'Folder, step 8', 'Review, step 9']);
  await expect(pop(page).getByText('Nothing is saved or installed until the last step')).toBeVisible();
  await nextUntil(page, 'Check it, then finish');
  await expect(pop(page).getByRole('button', { name: 'Build the pack' })).toBeVisible();
  await pop(page).getByRole('button', { name: 'Close set-up' }).click();
  await expect(page.getByRole('alertdialog', { name: 'Leave without placing it?' })).toBeVisible();
  await page.getByRole('button', { name: 'Leave', exact: true }).click();
  await expect(pop(page)).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  await expect(dialog.getByRole('button', { name: 'Open status: bb-test-1' })).toHaveCount(0);
  expect(posts.filter((p) => !/\/blackbox\/validate$/.test(p)), 'only the settings check was sent').toEqual([]);
});

test('BB-14a: step 1 has no Back button, a reload drops the pop-up, and nothing was saved', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await expect(pop(page).getByRole('button', { name: 'Back', exact: true })).toHaveCount(0);
  await pop(page).getByLabel('Name', { exact: true }).fill('bb-reload');
  await page.reload();
  const again = await openDrawer(page);
  await expect(pop(page)).toHaveCount(0);
  await expect(again.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  await expect(again.getByRole('button', { name: 'Open status: bb-reload' })).toHaveCount(0);
});

test('BB-14b: Esc inside the pop-up asks before leaving, and Keep going keeps every answer', async ({ page }) => {
  const dialog = await placeTo(page, 'bb-esc');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('alertdialog', { name: 'Leave without placing it?' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Keep going' })).toBeFocused();
  await page.getByRole('button', { name: 'Keep going' }).click();
  await expect(dialog).toBeVisible();
  await expect(stepTitle(page)).toHaveText('Where should it run?');
  await pop(page).getByRole('button', { name: 'Back', exact: true }).click();
  await expect(pop(page).getByLabel('Name', { exact: true })).toHaveValue('bb-esc');
});

test('BB-14c: the step number is said above the heading', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await expect(pop(page).locator('.shell-head')).toContainText('step 1 of 9');
  await pop(page).getByLabel('Name', { exact: true }).fill('bb-heading');
  await pop(page).getByRole('button', { name: 'Next', exact: true }).click();
  await expect(pop(page).locator('.shell-head')).toContainText('step 2 of 9');
});

test('BB-16: every out-of-range Keep setting is refused in its own words and keeps the pop-up on Keep', async ({ page }) => {
  await placeTo(page, 'bb-test-2');
  const keep = 'How long should it keep recordings?';
  await nextUntil(page, keep);
  await pop(page).getByRole('radio', { name: 'Other', exact: true }).check();
  const next = pop(page).getByRole('button', { name: 'Next', exact: true });
  const cases: { label: string; bad: string[]; message: string; good: string }[] = [
    { label: 'Other', bad: ['0', '366', '-3', '1.5', ''], message: 'Enter a whole number of days from 1 to 365.', good: '7' },
    { label: 'Disk space limit, in GB', bad: ['0', '0.05', '10001', '-1', ''], message: 'Enter a size from 0.1 to 10000 GB.', good: '2' },
    { label: 'Pause recording when free disk space is below, in %', bad: ['0', '51', '2.5', '-4', ''], message: 'Enter a whole number from 1 to 50 percent.', good: '10' }
  ];
  for (const c of cases) {
    const field = pop(page).getByRole('spinbutton', { name: c.label, exact: true });
    for (const bad of c.bad) {
      await field.fill(bad);
      await next.click();
      await expect(pop(page).getByText(c.message).first(), c.label + ' = "' + bad + '"').toBeVisible();
      await expect(stepTitle(page), 'still on Keep').toHaveText(keep);
    }
    await field.fill(c.good);
  }
  // The edges are allowed.
  for (const [label, edge, good] of [['Other', '1', '7'], ['Other', '365', '7'], ['Disk space limit, in GB', '0.1', '2'], ['Disk space limit, in GB', '10000', '2'], ['Pause recording when free disk space is below, in %', '1', '10'], ['Pause recording when free disk space is below, in %', '50', '10']]) {
    await pop(page).getByRole('spinbutton', { name: label, exact: true }).fill(edge);
    await next.click();
    await expect(stepTitle(page), label + ' = ' + edge).toHaveText('Where should recordings be saved?');
    await pop(page).getByRole('button', { name: 'Back', exact: true }).click();
    await expect(stepTitle(page)).toHaveText(keep);
    await pop(page).getByRole('spinbutton', { name: label, exact: true }).fill(good);
  }
});

test('BB-16b: a wrong port, bind address or folder each get their own message and keep the pop-up on Folder', async ({ page }) => {
  await placeTo(page, 'bb-bind');
  const folderStep = 'Where should recordings be saved?';
  await nextUntil(page, folderStep);
  const next = pop(page).getByRole('button', { name: 'Next', exact: true });
  const folder = pop(page).getByLabel('Folder on the other server', { exact: true });
  for (const bad of ['', 'D:\\', 'C:\\Windows\\x', 'relative\\path', 'D:/bb', '\\\\srv\\share\\bb']) {
    await folder.fill(bad);
    await next.click();
    await expect(stepTitle(page), 'folder = "' + bad + '"').toHaveText(folderStep);
    await expect(pop(page).getByRole('alert').first(), 'folder = "' + bad + '"').toBeVisible();
  }
  await folder.fill('D:\\UnravisBlackbox');
  await pop(page).getByText('Advanced · port and who can connect').click();
  const port = pop(page).getByLabel('Status port');
  for (const bad of ['0', '65536', '7788.5', '-80', '']) {
    await port.fill(bad);
    await next.click();
    await expect(stepTitle(page), 'port = "' + bad + '"').toHaveText(folderStep);
    await expect(pop(page).getByText('The status port must be a whole number from 1 to 65535.')).toBeVisible();
  }
  await port.fill('7788');
  const bind = pop(page).getByLabel('Listens on');
  for (const bad of ['', 'bad address!', '999.1.1.1', '*', '0.0.0.0/0']) {
    await bind.fill(bad);
    await next.click();
    await expect(stepTitle(page), 'bind = "' + bad + '"').toHaveText(folderStep);
    await expect(pop(page).getByRole('alert').first(), 'bind = "' + bad + '"').toBeVisible();
  }
});

test('BB-16c: a bind address other than this computer is called out with the read key as the only protection', async ({ page, allow }) => {
  allow(/\/api\/blackbox\/validate/, 'Review check of a Windows path on a non-Windows demo');
  await placeTo(page, 'bb-bind-warn');
  await nextUntil(page, 'Where should recordings be saved?');
  await pop(page).getByText('Advanced · port and who can connect').click();
  await pop(page).getByLabel('Listens on').fill('0.0.0.0');
  await expect(pop(page).getByText('This lets other computers on the network reach the status port. The read key is the only protection.')).toBeVisible();
  await nextUntil(page, 'Check it, then finish');
  await expect(pop(page).getByText('This Blackbox listens on every address, so other computers can reach it. The read key is the only protection.')).toBeVisible();
});

/** Builds the pack on Review and returns the download. Windows only: off Windows the server refuses a D:\ folder. */
async function buildPack(page: Page, name: string) {
  await placeTo(page, name);
  await nextUntil(page, 'Check it, then finish');
  const build = pop(page).getByRole('button', { name: 'Build the pack' });
  await expect(build).toBeEnabled();
  const [download] = await Promise.all([page.waitForEvent('download'), build.click()]);
  return download;
}

test('BB-17: Build the pack gives unravis-blackbox-<name>.zip with the recorder and its settings and no read key', async ({ page }, testInfo) => {
  test.skip(process.platform !== 'win32', 'the pack folder is a D:\\ path, which the server refuses off Windows');
  const download = await buildPack(page, 'bb-test-3');
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
  await expect(pop(page).getByText('Pack built')).toBeVisible();
});

test('BB-17a: the pack README has the exact install command, the screen says to run install as an administrator, and neither adds a flag', async ({ page }, testInfo) => {
  test.skip(process.platform !== 'win32', 'the pack folder is a D:\\ path, which the server refuses off Windows');
  const download = await buildPack(page, 'bb-test-4');
  const zip = testInfo.outputPath('pack4.zip');
  await download.saveAs(zip);
  const out = testInfo.outputPath('pack4');
  mkdirSync(out, { recursive: true });
  execFileSync(TAR, ['-xf', zip, '-C', out], { windowsHide: true });
  const readme = readFileSync(join(out, 'README.txt'), 'utf8');
  await expect(pop(page).getByText('Pack built')).toBeVisible();
  const screen = await pop(page).innerText();
  // The screen keeps to plain words (no command line); the README holds the exact command. Neither adds a flag the other never mentions.
  expect(screen).toContain('Unzip it and run install as an administrator.');
  expect(screen).not.toContain('install --');
  expect(readme).toContain('unravis-blackbox.exe install');
  expect(readme, 'the README does not add a flag the screen never mentions').not.toContain('install --name');
});

test('BB-20: the views of the other servers offer only reading, never a button that changes them', async ({ page }) => {
  const dialog = await openDrawer(page);
  const forbidden = /^(Start|Stop|Apply settings|Snapshot now|Remove|Install.*)$/;
  for (const name of ['web-b', 'web-c', 'web-d', 'web-e', 'web-f']) {
    await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
    const region = statusOf(page, name);
    await expect(region).toBeVisible();
    const buttons = await region.getByRole('button').evaluateAll((els) => els.map((e) => (e.textContent ?? '').trim()));
    for (const b of buttons) expect(b, name + ' offers "' + b + '"').not.toMatch(forbidden);
    await expect(region, name).toContainText('Nothing was changed on that server.');
    await expect(region.locator('details', { hasText: 'Steps for the server' }).getByRole('button')).toHaveCount(0);
    await expect(region.getByText(/delete/i), name + ' has no delete wording').toHaveCount(0);
    await closePop(page);
  }
});
