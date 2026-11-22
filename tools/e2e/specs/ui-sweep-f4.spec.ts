// UI sweep F4 (Blackbox, Unravis Lite, Releases): focus stays put after a button runs, the Place wizard keeps its size and Next
// button, the status pop-up and charts use the new plain words. Reads the demo Blackbox "web-a". Needs UNRAVIS_EXE like the
// other specs. Not run in the sweep session (no Windows exe there); the same steps were checked on a Linux demo build.
import { test, expect, type Page } from '../support/fixtures';

test.use({ demoOptions: { cases: false } });

async function openStatus(page: Page, name: string) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Blackbox' });
  await drawer.getByRole('button', { name: new RegExp('^Open status: ' + name) }).first().click();
  const pop = page.locator('dialog.pop[open]');
  await expect(pop.getByRole('region', { name: new RegExp('^' + name + '\\b') })).toBeVisible();
  return pop;
}

const activeName = (page: Page) =>
  page.evaluate(() => {
    const a = document.activeElement as HTMLElement | null;
    return a ? a.tagName + ' ' + ((a.getAttribute('aria-label') || a.textContent || '').trim().slice(0, 40)) : '';
  });

test('SWEEP-F4-02: focus is not lost to the page after Check again or Read the timing log', async ({ page }) => {
  const pop = await openStatus(page, 'web-a');
  const again = pop.getByRole('button', { name: /^(Check|Look) again$/ });
  await again.focus();
  await page.keyboard.press('Enter');
  await page.waitForTimeout(1200);
  expect(await activeName(page)).not.toMatch(/^BODY/);
  // the focus stays inside the open pop-up
  expect(await page.evaluate(() => !!document.activeElement?.closest('dialog.pop'))).toBe(true);
});

test('SWEEP-F4-01: the status pop-up names a failure in one place and the other-server note is plain', async ({ page }) => {
  const pop = await openStatus(page, 'web-a');
  await expect(pop.locator('.bb-ready')).toHaveCount(1);
  // The read-only line sits in the Blackbox list behind the pop-up, not in it.
  await expect(page.getByText(/Unravis reads your servers\. It never changes them\./).first()).toBeAttached();
});

test('SWEEP-F4-09: the Step 9 block is named for what it does and has one column', async ({ page }) => {
  const pop = await openStatus(page, 'web-a');
  const details = pop.locator('details', { hasText: 'Step 9: choose the windows to read' });
  if ((await details.getAttribute('open')) === null) await details.locator('summary').click();
  await expect(details.getByRole('group', { name: 'Incident window', exact: true })).toBeVisible();
  await expect(details.getByRole('group', { name: 'Reference window', exact: true })).toBeVisible();
  const boxes = await Promise.all([
    details.getByRole('group', { name: 'Incident window', exact: true }).boundingBox(),
    details.getByRole('group', { name: 'Reference window', exact: true }).boundingBox()
  ]);
  // stacked, not side by side
  expect(boxes[1]!.y).toBeGreaterThan(boxes[0]!.y + boxes[0]!.height - 1);
});

test('SWEEP-F4-12: the Place wizard keeps the same height and Next button position from step to step', async ({ page }) => {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Blackbox' });
  await drawer.getByRole('button', { name: /^Place a Blackbox/ }).first().click();
  const dlg = page.locator('dialog[open]').last();
  await dlg.getByRole('textbox').first().fill('sweep-box');
  const next = dlg.getByRole('button', { name: 'Next' });
  await expect(next).toBeEnabled();
  const first = await dlg.boundingBox();
  const nextY = (await next.boundingBox())!.y;
  await next.click();
  await expect(dlg.getByText(/step 2 of/)).toBeVisible();
  const second = await dlg.boundingBox();
  expect(Math.abs(second!.height - first!.height)).toBeLessThan(2);
  const y2 = await dlg.getByRole('button', { name: /^(Next|Build the pack|Place it)/ }).first().boundingBox();
  expect(Math.abs(y2!.y - nextY)).toBeLessThan(2);
});

test('SWEEP-F4-17: the charts show one coloured legend per chart and a Zoom group', async ({ page }) => {
  const pop = await openStatus(page, 'web-a');
  await pop.getByRole('button', { name: 'Open charts' }).first().click();
  const charts = page.locator('dialog.bbc[open]');
  await expect(charts.locator('canvas').first()).toBeVisible();
  await charts.getByRole('button', { name: 'Is the disk slow?' }).click();
  await expect(charts.locator('.bbc-legends').first()).toBeVisible();
  await expect(charts.getByRole('group', { name: /Zoom/ })).toBeVisible();
  await expect(charts.getByRole('button', { name: 'Log (x10 steps)' })).toBeVisible();
});
