// The guided Checklist tab. Items are stored on the case, so this file has its own demo and the
// order matters: GUIDE-14 first (each test leaves the list empty again), then GUIDE-13.
import { test, expect, type Page } from '../support/fixtures';
import { openGuided, runSteps, gotoStep } from '../support/ui';

const CLOSED_CASE = 'Night batch overrun, 10 October';
const LISTS = ['Need to do', 'Working on', 'What we learned', 'Ruled out'];

async function checklist(page: Page) {
  await page.getByRole('tab', { name: 'Checklist', exact: true }).click();
  const section = page.locator('section.checklist');
  await expect(section.getByRole('heading', { name: 'Checklist' })).toBeVisible();
  return section;
}

async function addItem(page: Page, text: string) {
  const section = page.locator('section.checklist');
  await section.getByRole('button', { name: '+ Add item' }).click();
  await section.getByLabel('What is the item?').fill(text);
  await section.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(section.getByText(text, { exact: true })).toBeVisible();
}

test('GUIDE-14: four empty lists, a counter, then add, move to each list and delete an item', async ({ page }) => {
  await openGuided(page);
  const section = await checklist(page);
  await expect(section.getByText('Keep four lists: Need to do, Working on, What we learned and Ruled out.')).toBeVisible();
  for (const name of LISTS) {
    await expect(section.getByRole('heading', { name: new RegExp('^' + name + ' \\(0\\)$') })).toBeVisible();
  }
  await expect(section.getByText('Nothing here yet.')).toHaveCount(4);
  const counter = section.locator('.count');
  await expect(counter).toHaveText('Done 0 · Open 0');

  await addItem(page, 'Check the app pool');
  await expect(counter).toHaveText('Done 0 · Open 1');
  await expect(section.getByRole('heading', { name: 'Need to do (1)' })).toBeVisible();

  const row = section.getByRole('listitem').filter({ hasText: 'Check the app pool' });
  const move = row.getByLabel('Move to');
  await move.selectOption({ label: 'Working on' });
  await expect(section.getByRole('heading', { name: 'Working on (1)' })).toBeVisible();
  await expect(counter).toHaveText('Done 0 · Open 1');
  await move.selectOption({ label: 'What we learned' });
  await expect(section.getByRole('heading', { name: 'What we learned (1)' })).toBeVisible();
  await expect(counter).toHaveText('Done 1 · Open 0');
  await move.selectOption({ label: 'Ruled out' });
  await expect(section.getByRole('heading', { name: 'Ruled out (1)' })).toBeVisible();
  await expect(counter).toHaveText('Done 1 · Open 0');
  await move.selectOption({ label: 'Need to do' });
  await expect(section.getByRole('heading', { name: 'Need to do (1)' })).toBeVisible();
  await expect(counter).toHaveText('Done 0 · Open 1');

  await row.getByRole('button', { name: 'Delete' }).click();
  await expect(section.getByText('Check the app pool')).toHaveCount(0);
  await expect(counter).toHaveText('Done 0 · Open 0');
  await expect(section.getByText('Nothing here yet.')).toHaveCount(4);
});

test('GUIDE-14a: the Add button stays off for an empty item and Cancel closes the form', async ({ page }) => {
  await openGuided(page);
  const section = await checklist(page);
  const toggle = section.getByRole('button', { name: '+ Add item' });
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  const add = section.getByRole('button', { name: 'Add', exact: true });
  await expect(add).toBeDisabled();
  await section.getByLabel('What is the item?').fill('   ');
  await expect(add).toBeDisabled();
  await section.getByLabel('What is the item?').fill('x');
  await expect(add).toBeEnabled();
  await section.getByRole('button', { name: 'Cancel' }).click();
  await expect(section.getByLabel('What is the item?')).toHaveCount(0);
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 0');
});

test('GUIDE-14b: an item of 301 characters is refused with a note and 300 is fine', async ({ page }) => {
  await openGuided(page);
  const section = await checklist(page);
  await section.getByRole('button', { name: '+ Add item' }).click();
  const field = section.getByLabel('What is the item?');
  const add = section.getByRole('button', { name: 'Add', exact: true });
  await field.fill('a'.repeat(301));
  await expect(section.getByText('An item holds up to 300 characters.')).toBeVisible();
  await expect(field).toHaveAttribute('aria-invalid', 'true');
  await expect(add).toBeDisabled();
  await field.fill('a'.repeat(300));
  await expect(section.getByText('An item holds up to 300 characters.')).toHaveCount(0);
  await expect(field).toHaveAttribute('aria-invalid', 'false');
  await expect(add).toBeEnabled();
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 0');
});

test('GUIDE-14c: two items with the same text are both kept, and items survive a reload', async ({ page }) => {
  await openGuided(page);
  const section = await checklist(page);
  await addItem(page, 'Look at the pool');
  await section.getByRole('button', { name: '+ Add item' }).click();
  await section.getByLabel('What is the item?').fill('Look at the pool');
  await section.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(section.getByText('Look at the pool', { exact: true })).toHaveCount(2);
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 2');

  await page.reload();
  await openGuided(page);
  const again = await checklist(page);
  await expect(again.getByText('Look at the pool', { exact: true })).toHaveCount(2);
  await expect(again.locator('.count')).toHaveText('Done 0 · Open 2');
  for (const n of [1, 0]) {
    await again.getByRole('button', { name: 'Delete' }).first().click();
    await expect(again.getByText('Look at the pool', { exact: true })).toHaveCount(n);
  }
  await expect(again.locator('.count')).toHaveText('Done 0 · Open 0');
});

test('GUIDE-14d: the checklist is the same when the case is opened again', async ({ page }) => {
  await openGuided(page);
  const section = await checklist(page);
  await addItem(page, 'Ask the DBA');
  await section.getByRole('listitem').filter({ hasText: 'Ask the DBA' }).getByLabel('Move to').selectOption({ label: 'Ruled out' });
  await expect(section.getByRole('heading', { name: 'Ruled out (1)' })).toBeVisible();
  await page.reload();
  await openGuided(page);
  const again = await checklist(page);
  await expect(again.getByRole('heading', { name: 'Ruled out (1)' })).toBeVisible();
  await expect(again.locator('.count')).toHaveText('Done 1 · Open 0');
  await again.getByRole('button', { name: 'Delete' }).click();
  await expect(again.locator('.count')).toHaveText('Done 0 · Open 0');
});

test('GUIDE-13: a suspect check goes on the checklist once and the suggested checks then have nothing left to add', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const step10 = await gotoStep(page, 10);
  const addButtons = step10.getByRole('button', { name: /Add its check to the checklist/ });
  await expect(addButtons).toHaveCount(39);
  await addButtons.first().click();
  await expect(step10.getByText('On the checklist')).toHaveCount(1);
  await expect(addButtons).toHaveCount(38);

  const section = await checklist(page);
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 1');
  await expect(section.getByText(/^Suspect /)).toHaveCount(1);

  // The only red or orange suspect is already there, so the suggestion has nothing left to add.
  const suggest = section.getByRole('button', { name: 'Add suggested checks from Step 10' });
  await expect(suggest).toBeEnabled();
  await suggest.click();
  await expect(section.getByText('Step 10 has no check that is not on the list already.')).toBeVisible();
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 1');
  await expect(section.getByRole('listitem')).toHaveCount(1);
});

test('GUIDE-13b: pressing the check button of a grey suspect adds one more row and keeps the rest', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const step10 = await gotoStep(page, 10);
  // Left from GUIDE-13: one check on the list, so 38 cards still offer the button.
  await expect(step10.getByText('On the checklist')).toHaveCount(1);
  const addButtons = step10.getByRole('button', { name: /Add its check to the checklist/ });
  await expect(addButtons).toHaveCount(38);
  await addButtons.first().click();
  await expect(step10.getByText('On the checklist')).toHaveCount(2);
  await expect(addButtons).toHaveCount(37);
  const section = await checklist(page);
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 2');
  const rows = section.getByRole('listitem');
  await expect(rows).toHaveCount(2);
  const texts = await rows.locator('.text').allInnerTexts();
  expect(new Set(texts).size).toBe(2);
});

test('GUIDE-13c: the suggested checks add the red and orange ones on an empty list and say how many', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const section = await checklist(page);
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 2');
  for (const left of [1, 0]) {
    await section.getByRole('button', { name: 'Delete' }).first().click();
    await expect(section.getByRole('listitem')).toHaveCount(left);
  }
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 0');
  await section.getByRole('button', { name: 'Add suggested checks from Step 10' }).click();
  await expect(section.getByText('Added 1 check.')).toBeVisible();
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 1');
  await expect(section.getByText(/^Suspect /)).toHaveCount(1);
  await section.getByRole('button', { name: 'Add suggested checks from Step 10' }).click();
  await expect(section.getByText('Step 10 has no check that is not on the list already.')).toBeVisible();
  await expect(section.getByRole('listitem')).toHaveCount(1);
  await section.getByRole('button', { name: 'Delete' }).click();
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 0');
});

test('GUIDE-13d: before the steps have run the suggested-checks button is off', async ({ page }) => {
  await openGuided(page);
  const section = await checklist(page);
  await expect(section.getByRole('button', { name: 'Add suggested checks from Step 10' })).toBeDisabled();
});

test('GUIDE-13a: on a closed case the page says so and offers no way to change the checklist', async ({ page }) => {
  await openGuided(page, CLOSED_CASE);
  const notice = 'This case is closed. You can read the checklist but not change it.';
  await expect(page.getByText(notice).first()).toBeVisible();
  await runSteps(page);
  const step10 = await gotoStep(page, 10);
  await expect(step10.getByRole('button', { name: /Add its check to the checklist/ })).toHaveCount(0);
  const section = await checklist(page);
  await expect(section.getByText(notice)).toBeVisible();
  await expect(section.getByRole('button', { name: '+ Add item' })).toHaveCount(0);
  await expect(section.getByRole('button', { name: 'Add suggested checks from Step 10' })).toHaveCount(0);
  await expect(section.getByRole('button', { name: 'Delete' })).toHaveCount(0);
  await expect(section.locator('.count')).toHaveText('Done 0 · Open 0');
});
