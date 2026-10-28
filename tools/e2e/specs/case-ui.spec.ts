// The start screen and case home in the browser: cards, list, open, enter a code, wizard, reopen.
import { test, expect } from '../support/fixtures';
import { demoId } from '../support/api';
import { goHome, openCase } from '../support/ui';

test.beforeEach(async ({ page }) => {
  await goHome(page);
});

test('CASE-01: three cards, and one filled accent button only', async ({ page }) => {
  await expect(page.locator('#start-title')).toHaveText('Start an investigation');
  await expect(page.locator('#card-existing')).toContainText('Use an existing case');
  await expect(page.locator('#card-enter')).toContainText('Enter an existing case');
  await expect(page.locator('#card-create')).toContainText('Create a new one');
  const filled = page.locator('.primary-button:visible');
  await expect(filled).toHaveCount(1);
  await expect(filled).toHaveText('Create a new case');
  await expect(page.getByRole('button', { name: 'Go to the analysis' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Protect your cases with a master key' })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  const boxes = await Promise.all(['#card-existing', '#card-enter', '#card-create'].map((s) => page.locator(s).boundingBox()));
  const [a, b, c] = boxes;
  expect(a && b && c).toBeTruthy();
  expect(Math.abs(a!.x - b!.x)).toBeLessThan(2);
  expect(b!.y).toBeGreaterThan(a!.y);
  expect(c!.y).toBeGreaterThan(b!.y);
});

test('CASE-02: rows, stickers and the archived and erased lists', async ({ page, api }) => {
  const list = await api('GET', '/api/cases');
  expect(list.json.cases).toHaveLength(7);
  expect(list.json.archivedTotal).toBe(2);
  const card = page.getByRole('region', { name: 'Use an existing case' });
  await expect(card.getByRole('button', { name: 'Open case Checkout slowdown, 17 October' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Open case Night batch overrun, 10 October' })).toBeVisible();
  await expect(card.getByRole('button', { name: /^Open case Search index rebuild/ })).toHaveCount(0);
  await expect(card).toContainText('Working copy missing');
  await expect(card).toContainText('Legacy case from an older version');
  await expect(card).toContainText('Key ending');
  await expect(card).not.toContainText('Payroll run discrepancy');
  await expect(card.getByRole('button', { name: 'Archived cases (2)' })).toBeVisible();
  await expect(card).toContainText('Erased case k7m2qx4a');
  await expect(card).toContainText('Erased case q2w7exak');
  await expect(card.getByRole('button', { name: 'Show more cases' })).toHaveCount(0);
});

test('CASE-03: open a case and read its story, then come back with All cases', async ({ page }) => {
  await openCase(page, 'Checkout slowdown, 17 October');
  await expect(page.getByRole('heading', { level: 2, name: 'Checkout slowdown, 17 October' })).toBeVisible();
  await expect(page.getByText('Case started').first()).toBeVisible();
  await page.getByRole('button', { name: 'All cases' }).click();
  await expect(page.locator('#start-title')).toBeVisible();
  await expect(page.getByRole('region', { name: 'Use an existing case' })).toContainText('Checkout slowdown, 17 October');
});

test('CASE-04: a case code is accepted in any layout and refused with one sentence', async ({ page, allow }) => {
  // The page asks the server about an erased code and the server says 404; the browser logs it. That is the test.
  allow(/Failed to load resource.*404.*api.cases/, 'provoked on purpose');
  const field = page.getByLabel('Case code');
  const open = page.getByRole('button', { name: 'Open case', exact: true });
  const refused = page.getByText('That code does not match a case on this computer.');
  await field.fill(demoId('demoopen'));
  await open.click();
  await expect(page.getByRole('button', { name: /^All cases/ })).toBeVisible();
  await page.getByRole('button', { name: 'All cases' }).click();
  await expect(page.locator('#start-title')).toBeVisible();
  await page.getByLabel('Case code').fill('DEMO-OPEN aaaa aaaa aaaa aaaa aa');
  await open.click();
  await expect(page.getByRole('button', { name: /^All cases/ })).toBeVisible();
  await page.getByRole('button', { name: 'All cases' }).click();
  for (const bad of ['demoopen', demoId('demoopen') + 'a', 'demoopen1aaaaaaaaaaaaaaaaaa', demoId('k7m2qx4a')]) {
    await page.getByLabel('Case code').fill(bad);
    await open.click();
    await expect(refused, bad).toBeVisible();
    expect(page.url(), 'the typed code is not put in the address').not.toContain(bad);
  }
  // An empty field is not asked about (D-A-04): the button is off.
  await page.getByLabel('Case code').fill('');
  await expect(open).toBeDisabled();
  await expect(page.locator('#start-title')).toBeVisible();
});

test('CASE-05: the wizard makes an empty case that appears in the list', async ({ page }) => {
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByText('Nothing is saved until the last step.')).toBeVisible();
  await page.getByLabel('Case name').fill('QA Wizard Happy');
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText('No sources chosen. The case will be made empty')).toBeVisible();
  await expect(page.getByText('None. This case has no key of its own.')).toBeVisible();
  await page.getByRole('button', { name: 'Create empty case' }).click();
  // A created case opens on Case home (D-A-05).
  await expect(page.getByRole('heading', { level: 2, name: 'QA Wizard Happy' })).toBeVisible();
  await expect(page.getByText('Case started').first()).toBeVisible();
  await page.getByRole('button', { name: 'All cases' }).click();
  await expect(page.getByRole('button', { name: 'Open case QA Wizard Happy' })).toBeVisible();
});

test('CASE-08: a closed case is read-only and reopens once, only from closed', async ({ page, api }) => {
  const id = demoId('democlos');
  await openCase(page, 'Night batch overrun, 10 October');
  await expect(page.getByText('This case is closed. You can read it, but nothing can be added.')).toBeVisible();
  const reopen = page.getByRole('button', { name: 'Reopen this case' });
  await reopen.click();
  await expect(page.getByText('Case reopened').first()).toBeVisible();
  await expect.poll(async () => (await api('GET', `/api/cases/${id}`)).json.state).toBe('open');
  await expect(reopen).toHaveCount(0);
});

test('CASE-09: a second tab that is out of date is told the case changed', async ({ page, api, context, allow }) => {
  allow(/Failed to load resource.*409.*reopen/, 'provoked on purpose: the stale belief is the test');
  const id = demoId('demostal');
  await openCase(page, 'Ledger posting lag, 12 October');
  const reopenA = page.getByRole('button', { name: 'Reopen this case' });
  await expect(reopenA).toBeVisible();
  // The other tab reopens the case.
  const done = await api('POST', `/api/cases/${id}/reopen`, undefined, 'closed');
  expect(done.status).toBe(200);
  await reopenA.click();
  await expect(page.getByText('This case changed in another tab.')).toBeVisible();
  await page.getByRole('button', { name: 'Reload' }).click();
  await expect(page.getByRole('button', { name: 'Reopen this case' })).toHaveCount(0);
  expect((await api('GET', `/api/cases/${id}`)).json.state).toBe('open');
  void context;
});
