// The create-a-case wizard shell: name rules, leaving and discarding, the environments field.
// Nothing is saved until the last step, so none of these tests makes a case.
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';

test.beforeEach(async ({ page }) => {
  await goHome(page);
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(page.getByText('Nothing is saved until the last step.')).toBeVisible();
});

const name = (page: Page) => page.getByLabel('Case name');
const next = (page: Page) => page.getByRole('button', { name: 'Next' });
const problem = (page: Page, text: string) => page.getByRole('alert').filter({ hasText: text });

test('CASE-06: an empty name blocks Next and the later steps are not available', async ({ page }) => {
  await expect(next(page)).toBeDisabled();
  await expect(page.getByText('Give the case a name to go on.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Step 2, Sources, not available until the case has a name' })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Step 3, Review, not available until the case has a name' })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Step 1, Name, current step' })).toBeVisible();
});

test('CASE-06a: one letter is too short and 81 letters is too long, and each blocks Next', async ({ page }) => {
  await name(page).fill('A');
  await name(page).blur();
  await expect(problem(page, 'The name needs at least 2 characters.')).toBeVisible();
  await expect(page.getByText('1 of 80')).toBeVisible();
  await expect(next(page)).toBeDisabled();
  await name(page).fill('B'.repeat(81));
  await expect(problem(page, 'The name can have at most 80 characters.')).toBeVisible();
  await expect(page.getByText('81 of 80')).toBeVisible();
  await expect(next(page)).toBeDisabled();
  await name(page).fill('C'.repeat(80));
  await expect(problem(page, 'The name can have at most 80 characters.')).toHaveCount(0);
  await expect(next(page)).toBeEnabled();
});

test('CASE-06b: spaces only count as no name, and a control character is refused', async ({ page }) => {
  await name(page).fill('      ');
  await expect(page.getByText('Give the case a name to go on.')).toBeVisible();
  await expect(page.getByText('0 of 80')).toBeVisible();
  await expect(next(page)).toBeDisabled();
  await name(page).fill('QA bell \u0007 name');
  await expect(problem(page, 'The name has a character that is not allowed. Remove it and try again.')).toBeVisible();
  await expect(next(page)).toBeDisabled();
  await name(page).fill('QA fine name');
  await expect(problem(page, 'The name has a character that is not allowed. Remove it and try again.')).toHaveCount(0);
  await expect(next(page)).toBeEnabled();
});

test('CASE-06c: a name with angle brackets is accepted and shown as plain text', async ({ page }) => {
  await name(page).fill('QA <b>bold</b> test');
  await expect(next(page)).toBeEnabled();
  await next(page).click();
  await next(page).click();
  await expect(page.getByText('QA <b>bold</b> test').first()).toBeVisible();
  await expect(page.locator('main b, .wizard b')).toHaveCount(0);
});

test('CASE-06d: Next then Back keeps the name', async ({ page }) => {
  await name(page).fill('QA Resume');
  await next(page).click();
  await expect(page.getByRole('heading', { name: 'Choose what to look at' })).toBeVisible();
  await page.getByRole('button', { name: 'Back' }).click();
  await expect(name(page)).toHaveValue('QA Resume');
});

test('CASE-06e: Leave the guide asks, Keep working keeps the draft, Leave and discard makes no case', async ({ page, api }) => {
  await name(page).fill('QA Discard Me');
  await page.getByRole('button', { name: 'Leave the guide' }).click();
  await expect(page.getByText('Leave without creating a case?')).toBeVisible();
  await expect(page.getByText('What you typed here will be thrown away. Nothing has been saved yet.')).toBeVisible();
  await page.getByRole('button', { name: 'Keep working' }).click();
  await expect(page.getByText('Leave without creating a case?')).toHaveCount(0);
  await expect(name(page)).toHaveValue('QA Discard Me');

  await page.getByRole('button', { name: 'Leave the guide' }).click();
  await page.getByRole('button', { name: 'Leave and discard' }).click();
  await expect(page.getByRole('button', { name: 'Create a new case' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open case QA Discard Me' })).toHaveCount(0);
  const res = await api('GET', '/api/cases');
  expect(JSON.stringify(res.json)).not.toContain('QA Discard Me');

  await page.getByRole('button', { name: 'Create a new case' }).click();
  await expect(name(page)).toHaveValue('');
  await expect(page.getByText('Your earlier answers are still here.')).toHaveCount(0);
});

test('CASE-06f: Escape on the leave question closes it and keeps the wizard', async ({ page }) => {
  await name(page).fill('QA Escape');
  await page.getByRole('button', { name: 'Leave the guide' }).click();
  await expect(page.getByText('Leave without creating a case?')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByText('Leave without creating a case?')).toHaveCount(0);
  await expect(name(page)).toHaveValue('QA Escape');
});

test('CASE-06g: environments are split on commas, duplicates are left out with a note', async ({ page }) => {
  await name(page).fill('QA Envs');
  await page.getByLabel('Environments (optional)').fill('Prod, Test, prod, Prod, Staging');
  await expect(page.getByText('Left out, as the name is already in the list: prod, Prod.')).toBeVisible();
  await expect(page.locator('.envs-label').filter({ hasText: '3 environments' })).toBeVisible();
  const list = page.getByRole('list', { name: 'Environments in this case' });
  await expect(list.getByRole('listitem')).toHaveText(['Prod', 'Test', 'Staging']);
});

test('CASE-06h: more than 32 environments are cut with a note naming the rest', async ({ page }) => {
  await name(page).fill('QA Many Envs');
  const many = Array.from({ length: 35 }, (_, i) => 'E' + i).join(', ');
  await page.getByLabel('Environments (optional)').fill(many);
  await expect(page.getByText('A case holds at most 32 environments. Left out: E32, E33, E34.')).toBeVisible();
  await expect(page.locator('#c43-envs-list, .envs-label').filter({ hasText: '32 environments' })).toBeVisible();
});

test('CASE-06i: with no environments the list says none are named yet', async ({ page }) => {
  await name(page).fill('QA No Envs');
  await expect(page.getByText('No environments named yet. You can name them later.')).toBeVisible();
});
