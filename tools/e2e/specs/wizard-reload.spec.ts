// BL-495h (sweep CASES-25): a reload in the middle of the create guide goes back to the guide with the kept draft (name, environments), and
// says so with the "still here" line. Before this the draft came back only when the guide was opened again; the reload landed on the analysis screen.
//   WIZR-01  reload on the guide step with a name typed: the guide is back, the name is in the field, the resumed line shows
//   WIZR-02  Home first, then reload: the start screen (the person was not in the guide); the draft still comes back when the guide is opened again
//   WIZR-03  "Start over" then reload: nothing is kept, no guide
//   WIZR-04  an empty guide has nothing to keep: a reload does not stay in the guide
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';

async function openGuide(page: Page) {
  page.on('dialog', (d) => void d.accept());
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByLabel('Case name')).toBeVisible();
}

test('WIZR-01 a reload in the guide comes back to the guide with the typed name and the resumed line', async ({ page }) => {
  await openGuide(page);
  await page.getByLabel('Case name').fill('Reload Keeps Me');
  await page.reload();
  await expect(page.getByLabel('Case name')).toHaveValue('Reload Keeps Me');
  await expect(page.locator('.wiz-resumed')).toContainText('Your earlier answers are still here');
  await expect(page).toHaveTitle(/New case/);
  // Never a key or a source: nothing but the name, environments and the key switch is kept.
  const kept = await page.evaluate(() => sessionStorage.getItem('unravis.wizardDraft') ?? '');
  expect(Object.keys(JSON.parse(kept)).sort()).toEqual(['envText', 'keyOn', 'title']);
});

test('WIZR-02 Home first, then a reload: the start screen, and the draft is still offered when the guide is opened again', async ({ page }) => {
  await openGuide(page);
  await page.getByLabel('Case name').fill('Left And Reloaded');
  // The browser's Back leaves the guide without throwing the draft away (the header Home does the same on a real install; the demo header has none).
  await page.goBack();
  await expect(page.locator('#start-title')).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Case name')).toHaveCount(0);
  await goHome(page);
  await expect(page.getByRole('button', { name: 'Create a new case' })).toBeVisible();
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByLabel('Case name')).toHaveValue('Left And Reloaded');
});

test('WIZR-03 after "Start over" a reload keeps nothing and does not open the guide', async ({ page }) => {
  await openGuide(page);
  await page.getByLabel('Case name').fill('Thrown Away');
  await page.reload();
  await expect(page.getByLabel('Case name')).toHaveValue('Thrown Away');
  await page.getByRole('button', { name: 'Start over' }).click();
  const ask = page.getByRole('dialog');
  if (await ask.count()) await ask.getByRole('button', { name: /start over/i }).last().click();
  await expect(page.getByLabel('Case name')).toHaveValue('');
  await page.reload();
  await expect(page.getByLabel('Case name')).toHaveCount(0);
});

test('WIZR-04 an empty guide keeps nothing: a reload does not stay in it', async ({ page }) => {
  await openGuide(page);
  await page.reload();
  await expect(page.getByLabel('Case name')).toHaveCount(0);
});
