// The Export tab of the guided analysis and the Export evidence drawer in the header. Nothing here changes
// demo data (a report only reads), so the tests may run in any order.
import { createHash } from 'node:crypto';
import { test, expect, type Page } from '../support/fixtures';
import { openGuided, runSteps, setWindows } from '../support/ui';

const CLOSED_CASE = 'Night batch overrun, 10 October';
const STALE = 'The windows or lists changed after the last run. The report holds the earlier run, as it is on screen.';

async function exportTab(page: Page) {
  await page.getByRole('tab', { name: 'Export', exact: true }).click();
  const section = page.locator('section.export');
  await expect(section.getByRole('heading', { name: 'Export' })).toBeVisible();
  return section;
}

async function readDownload(download: import('@playwright/test').Download): Promise<Buffer> {
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  return Buffer.concat(chunks);
}

async function shownHash(section: import('@playwright/test').Locator): Promise<string> {
  const line = section.getByText(/^Saved\. SHA-256 of the saved file: [0-9a-f]{64}$/);
  await expect(line).toBeVisible();
  return ((await line.innerText()).match(/[0-9a-f]{64}/) as RegExpMatchArray)[0];
}

test('EXP-01: the Export tab shows the lead, the options, one primary button and the privacy line, and Preview saves nothing', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await exportTab(page);
  await expect(section.getByText('Build a report from what is on screen. The report is made on this computer and held in this window until you save it.')).toBeVisible();
  const include = section.getByRole('group', { name: 'Include' });
  for (const name of ['Figures and charts', 'Suspects with their cheapest checks', 'Checklist', 'Sources and what could not be tested']) {
    await expect(include.getByRole('checkbox', { name })).toBeChecked();
  }
  const nearby = include.getByRole('checkbox', { name: 'Nearby activity for chosen calls' });
  await expect(nearby).toBeDisabled();
  await expect(nearby).not.toBeChecked();
  await expect(section.getByText('Pick calls first: this needs calls chosen in Nearby activity.')).toBeVisible();
  await expect(section.getByText('No request payloads, no user ids, no connection details, no passwords. Dates in UTC.')).toBeVisible();

  await expect(section.getByRole('button', { name: 'Download report (HTML)' })).toHaveClass(/primary-button/);
  await expect(section.getByRole('button', { name: 'Download data (CSV)' })).toHaveClass(/secondary-button/);
  await expect(section.getByRole('button', { name: 'Preview', exact: true })).toHaveClass(/secondary-button/);
  await expect(section.locator('button.primary-button')).toHaveCount(1);

  let downloads = 0;
  page.on('download', () => downloads++);
  await section.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect(section.getByText(/^unravis-report-checkout-slowdown-17-october-2026-10-20\.html is ready, \d+ KB\.$/)).toBeVisible();
  const frame = section.locator('iframe[title="Preview"]');
  await expect(frame).toHaveCount(1);
  await expect(frame).toHaveAttribute('sandbox', '');
  const inner = page.frameLocator('iframe[title="Preview"]');
  await expect(inner.getByRole('heading', { name: 'Checkout slowdown, 17 October', level: 1 })).toBeVisible();
  await expect(section.getByText(/Saved\. SHA-256/)).toHaveCount(0);
  await section.getByRole('button', { name: 'Close preview' }).click();
  await expect(frame).toHaveCount(0);
  expect(downloads).toBe(0);
});

test('EXP-01a: Preview twice gives one frame, Escape from the preview closes it and an empty selection still previews', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await exportTab(page);
  const preview = section.getByRole('button', { name: 'Preview', exact: true });
  await preview.click();
  const frame = section.locator('iframe[title="Preview"]');
  await expect(frame).toHaveCount(1);
  await preview.click();
  await expect(section.getByText('Building the report.')).toHaveCount(0);
  await expect(preview).toBeEnabled();
  await expect(frame).toHaveCount(1);
  await section.getByRole('button', { name: 'Close preview' }).focus();
  await page.keyboard.press('Escape');
  await expect(frame).toHaveCount(0);
  await expect(preview).toBeFocused();

  const include = section.getByRole('group', { name: 'Include' });
  for (const name of ['Figures and charts', 'Suspects with their cheapest checks', 'Checklist', 'Sources and what could not be tested']) {
    await include.getByRole('checkbox', { name }).uncheck();
  }
  await preview.click();
  await expect(section.getByText(/ is ready, \d+ KB\.$/)).toBeVisible();
  await expect(frame).toHaveCount(1);
  await expect(section.getByRole('alert')).toHaveCount(0);
});

test('EXP-01b: opening another tab while the preview is open leaves no stale preview behind', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await exportTab(page);
  await section.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect(section.locator('iframe[title="Preview"]')).toHaveCount(1);
  await page.getByRole('tab', { name: 'Checklist', exact: true }).click();
  const again = await exportTab(page);
  await expect(again.locator('iframe[title="Preview"]')).toHaveCount(0);
});

test('EXP-02: the saved HTML report matches the hash on screen and holds no payload, user id or connection detail', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await exportTab(page);
  const [download] = await Promise.all([page.waitForEvent('download'), section.getByRole('button', { name: 'Download report (HTML)' }).click()]);
  expect(download.suggestedFilename()).toBe('unravis-report-checkout-slowdown-17-october-2026-10-20.html');
  const bytes = await readDownload(download);
  const shown = await shownHash(section);
  expect(createHash('sha256').update(bytes).digest('hex')).toBe(shown);
  await expect(section.getByText(/^unravis-report-.*\.html is ready, \d+ KB\.$/)).toBeVisible();

  const html = bytes.toString('utf8');
  expect(html).toContain('Checkout slowdown, 17 October');
  for (const heading of ['How sure are we', 'Figures', 'Suspects', 'Checklist']) expect(html).toContain(heading);
  for (const banned of ['userId', '1003', 'mongodb://', 'demo-sql.example.invalid', '<script', 'javascript:', 'onclick', '<iframe']) {
    expect(html, banned).not.toContain(banned);
  }
  expect(html).not.toMatch(/https?:\/\//);
  expect(html).not.toContain(STALE);
});

test('EXP-02a: downloading twice gives the same file, and the page stays usable', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await exportTab(page);
  const hashes: string[] = [];
  for (let i = 0; i < 2; i++) {
    const [download] = await Promise.all([page.waitForEvent('download'), section.getByRole('button', { name: 'Download report (HTML)' }).click()]);
    hashes.push(createHash('sha256').update(await readDownload(download)).digest('hex'));
    expect(await shownHash(section)).toBe(hashes[i]);
  }
  expect(hashes[1]).toBe(hashes[0]);
  await expect(section.getByRole('button', { name: 'Download report (HTML)' })).toBeEnabled();
});

test('EXP-02b: unticking an option leaves that part out of the saved report', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await exportTab(page);
  const get = async () => {
    const [download] = await Promise.all([page.waitForEvent('download'), section.getByRole('button', { name: 'Download report (HTML)' }).click()]);
    return (await readDownload(download)).toString('utf8');
  };
  const full = await get();
  await section.getByRole('checkbox', { name: 'Checklist' }).uncheck();
  const noChecklist = await get();
  expect(noChecklist.length).toBeLessThan(full.length);
  expect(noChecklist).not.toContain('Need to do');
  expect(full).toContain('Need to do');
  await section.getByRole('checkbox', { name: 'Checklist' }).check();
});

test('EXP-05: after a window changes the Export tab warns, the button says so, the saved report repeats it and Run again clears it', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await exportTab(page);
  await expect(section.getByRole('alert')).toHaveCount(0);
  await expect(section.getByRole('button', { name: 'Download report (HTML)', exact: true })).toBeVisible();

  await page.getByRole('tab', { name: 'Steps', exact: true }).click();
  await page.getByRole('group', { name: 'Reference window (normal)' }).getByLabel('From').fill('2026-10-20T08:25');
  const warned = await exportTab(page);
  const alert = warned.getByRole('alert');
  await expect(alert.getByText('This report has warnings')).toBeVisible();
  await expect(alert.getByText(STALE)).toBeVisible();
  const warnButton = warned.getByRole('button', { name: 'Download report (HTML) with these warnings' });
  await expect(warnButton).toHaveClass(/primary-button/);

  const [download] = await Promise.all([page.waitForEvent('download'), warnButton.click()]);
  const html = (await readDownload(download)).toString('utf8');
  expect(html).toContain('This report has warnings');
  expect(html).toContain('The windows or lists changed after the last run. This report holds the earlier run, as it was on screen.');
  expect(createHash('sha256').update(Buffer.from(html, 'utf8')).digest('hex')).toBe(await shownHash(warned));

  await page.getByRole('tab', { name: 'Steps', exact: true }).click();
  await page.getByRole('button', { name: 'Run again' }).click();
  await expect(page.getByRole('button', { name: /Out of date|Running|Waiting/ })).toHaveCount(0);
  const clean = await exportTab(page);
  await expect(clean.getByRole('alert')).toHaveCount(0);
  await expect(clean.getByRole('button', { name: 'Download report (HTML)', exact: true })).toBeVisible();
});

test('EXP-05a: the warning goes when the window is put back, without running again', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  await page.getByRole('group', { name: 'Reference window (normal)' }).getByLabel('From').fill('2026-10-20T08:20');
  const warned = await exportTab(page);
  await expect(warned.getByText(STALE)).toBeVisible();
  await page.getByRole('tab', { name: 'Steps', exact: true }).click();
  await setWindows(page);
  const back = await exportTab(page);
  await expect(back.getByText(STALE)).toHaveCount(0);
  await expect(back.getByRole('button', { name: 'Download report (HTML)', exact: true })).toBeVisible();
});

test('EXP-10: Export works on a closed case and gives the same file each time', async ({ page }) => {
  await openGuided(page, CLOSED_CASE);
  const section = await exportTab(page);
  await section.getByRole('button', { name: 'Preview', exact: true }).click();
  const inner = page.frameLocator('iframe[title="Preview"]');
  await expect(inner.getByRole('heading', { name: CLOSED_CASE, level: 1 })).toBeVisible();
  await expect(inner.getByText('This case is closed')).toBeVisible();
  await section.getByRole('button', { name: 'Close preview' }).click();

  const hashes: string[] = [];
  for (const label of ['Download report (HTML)', 'Download report (HTML)', 'Download data (CSV)']) {
    const [download] = await Promise.all([page.waitForEvent('download'), section.getByRole('button', { name: label }).click()]);
    const bytes = await readDownload(download);
    const hash = createHash('sha256').update(bytes).digest('hex');
    expect(await shownHash(section)).toBe(hash);
    hashes.push(hash);
  }
  expect(hashes[1]).toBe(hashes[0]);
  expect(hashes[2]).not.toBe(hashes[0]);
});

test('EXP-10a: switching between the ways of working keeps the report the same', async ({ page }) => {
  await openGuided(page, CLOSED_CASE);
  const section = await exportTab(page);
  const [first] = await Promise.all([page.waitForEvent('download'), section.getByRole('button', { name: 'Download report (HTML)' }).click()]);
  const before = createHash('sha256').update(await readDownload(first)).digest('hex');

  await page.reload();
  await openGuided(page, CLOSED_CASE);
  const again = await exportTab(page);
  const [second] = await Promise.all([page.waitForEvent('download'), again.getByRole('button', { name: 'Download report (HTML)' }).click()]);
  expect(createHash('sha256').update(await readDownload(second)).digest('hex')).toBe(before);
});

// ---- The Export evidence drawer in the header ----

async function openEvidenceDrawer(page: Page) {
  await page.getByRole('button', { name: 'Export', exact: true }).first().click();
  const drawer = page.getByRole('dialog', { name: 'Export evidence' });
  await expect(drawer).toBeVisible();
  return drawer;
}

test('EXP-07: the evidence export previews without saving, then the saved file matches the hash on screen', async ({ page }) => {
  await page.getByRole('button', { name: 'Last hour', exact: true }).click();
  await page.getByRole('button', { name: 'Run analysis' }).click();
  const drawer = await openEvidenceDrawer(page);
  await expect(drawer.getByText('Build a file of the findings in this window. You see everything in it before anything is saved.')).toBeVisible();
  await expect(drawer.getByText('This only builds a preview. Nothing is saved until you press Save file.')).toBeVisible();
  await drawer.getByRole('checkbox', { name: 'Include user pseudonyms' }).check();
  await drawer.getByRole('checkbox', { name: 'Include my ledger' }).check();
  await drawer.getByRole('button', { name: 'Preview export' }).click();

  await expect(drawer.getByRole('region', { name: 'What is hidden' })).toBeVisible();
  await expect(drawer.getByRole('region', { name: 'Findings' })).toBeVisible();
  await expect(drawer.getByRole('region', { name: 'Manifest' })).toBeVisible();
  await expect(drawer.getByText('Manifest and body SHA-256')).toBeVisible();
  await expect(drawer.getByRole('region', { name: 'Ledger entries' }).getByText('No ledger entries in this file.')).toBeVisible();
  await expect(drawer.getByRole('region', { name: 'User pseudonyms' })).toBeVisible();
  await drawer.getByText('Show the file content').click();
  const raw = await drawer.getByLabel('Show the file content').innerText();
  for (const banned of ['userId', '1003', 'mongodb://', 'demo-sql.example.invalid']) expect(raw, banned).not.toContain(banned);

  const [download] = await Promise.all([page.waitForEvent('download'), drawer.getByRole('button', { name: 'Save file' }).click()]);
  expect(download.suggestedFilename()).toMatch(/^unravis-evidence-.*\.json$/);
  const bytes = await readDownload(download);
  const line = drawer.getByText(/^Saved\. SHA-256 of the saved file: [0-9a-f]{64}$/);
  await expect(line).toBeVisible();
  expect(createHash('sha256').update(bytes).digest('hex')).toBe(((await line.innerText()).match(/[0-9a-f]{64}/) as RegExpMatchArray)[0]);
  // After a save there is no second Save file for the same preview, only Preview again.
  await expect(drawer.getByRole('button', { name: 'Save file' })).toHaveCount(0);
  await expect(drawer.getByRole('button', { name: 'Preview again' }).first()).toBeVisible();
});

test('EXP-07a: a window with no findings says so and offers nothing to save', async ({ page }) => {
  const drawer = await openEvidenceDrawer(page);
  await drawer.getByRole('button', { name: 'Preview export' }).click();
  await expect(drawer.getByText('No findings in this window, so there is nothing to export.')).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'Save file' })).toHaveCount(0);
});

test('EXP-07b: Escape closes the evidence drawer', async ({ page }) => {
  const drawer = await openEvidenceDrawer(page);
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
});
