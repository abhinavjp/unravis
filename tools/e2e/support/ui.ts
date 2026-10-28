// Small helpers for the page: how to reach the start screen and a case.
import { expect, type Page } from '@playwright/test';

/** Go to the start screen (the explorer opens first; Home is in the header). */
export async function goHome(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await expect(page.locator('#start-title')).toBeVisible();
}

/** Open a case from the start screen by its title. */
export async function openCase(page: Page, title: string): Promise<void> {
  await page.getByRole('button', { name: 'Open case ' + title }).click();
  await expect(page.locator('#case-heading')).toBeVisible();
}
