// The start screen's restore section (demo has no master key) and the start screen's absence in a demo without cases.
import { test, expect } from '../support/fixtures';
import { goHome } from '../support/ui';

test.describe('with demo cases', () => {
  test.beforeEach(async ({ page }) => {
    await goHome(page);
  });

  test('CASE-24: the restore toggle opens the intro, and the demo says a master key is needed first', async ({ page }) => {
    const toggle = page.getByRole('button', { name: 'Restore from a case file' });
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(page.getByText('A case file is one encrypted file with a copy of a case. Restoring it always makes a new case. It never changes a case you already have.')).toBeVisible();
    await expect(page.getByText('Restoring needs a master key on this PC too. Set one up first.')).toBeVisible();
    await expect(page.locator('#start-title').locator('xpath=ancestor::main').getByRole('button', { name: 'Protect your cases with a master key' }).first()).toBeVisible();
    // With no master key there is no path field and no restore button to press.
    await expect(page.getByLabel('Path of the case file')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Restore as a new case' })).toHaveCount(0);
  });

  test('CASE-24a: the toggle closes and opens again, and nothing about it reaches the address bar', async ({ page }) => {
    const toggle = page.getByRole('button', { name: 'Restore from a case file' });
    const intro = page.getByText('Restoring it always makes a new case.');
    await toggle.click();
    await expect(intro).toBeVisible();
    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await expect(intro).toHaveCount(0);
    await toggle.click();
    await expect(intro).toBeVisible();
    expect(page.url()).not.toMatch(/restore|unravis-case|passphrase/i);
  });

  test('CASE-24b: the restore API answers no-master-key for an empty, a bad and a network path alike, and keeps no case', async ({ api }) => {
    const before = await api('GET', '/api/cases');
    const count = (before.json.cases as unknown[]).length;
    for (const path of ['', 'C:\\Cases\\notacase.txt', '\\\\server\\share\\a.unravis-case']) {
      const r = await api('POST', '/api/cases/restore', { path, key: 'qa passphrase one' });
      expect(r.status).toBe(409);
      expect(JSON.stringify(r.json)).toContain('no-master-key');
      expect(JSON.stringify(r.json)).not.toContain('qa passphrase one');
    }
    const after = await api('GET', '/api/cases');
    expect((after.json.cases as unknown[]).length).toBe(count);
  });
});

test.describe('a demo without cases', () => {
  test.use({ demoOptions: { cases: false } });

  test('CASE-23: there is no start screen and the case routes answer 503 cases-unavailable', async ({ page, api }) => {
    await expect(page.getByRole('button', { name: 'Create a new case' })).toHaveCount(0);
    await expect(page.getByText('Your cases could not be loaded.')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Home' })).toHaveCount(0);
    // The explorer opens instead.
    await expect(page.getByRole('button', { name: 'Run analysis' })).toBeVisible();
    const list = await api('GET', '/api/cases');
    expect(list.status).toBe(503);
    expect(JSON.stringify(list.json)).toContain('cases-unavailable');
    const create = await api('POST', '/api/cases', { title: 'QA No Store', requestId: 'qa-nostore-1' });
    expect(create.status).toBe(503);
  });
});
