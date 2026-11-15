// Small helpers for the page: how to reach the start screen and a case.
import { expect, type Page } from '@playwright/test';

/** Go to the start screen (the explorer opens first; Home is in the header). */
export async function goHome(page: Page): Promise<void> {
  // A fresh window can already be on the start screen, where "All cases" does not exist.
  if (!(await page.locator('#start-title').isVisible())) {
    await page.getByRole('button', { name: /^All cases/ }).first().click();
  }
  await expect(page.locator('#start-title')).toBeVisible();
}

/** Open a case from the start screen by its title. */
export async function openCase(page: Page, title: string): Promise<void> {
  await page.getByRole('button', { name: 'Open case ' + title }).click();
  await expect(page.getByRole('button', { name: /^All cases/ })).toBeVisible();
}
