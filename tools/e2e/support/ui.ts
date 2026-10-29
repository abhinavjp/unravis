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
  await expect(page.getByRole('button', { name: /^All cases/ })).toBeVisible();
}

// ---- Guided analysis helpers (shared by guide-*.spec.ts) ----

/** The slow window and the normal window before it, on the demo day (data exists 08:30 to 09:00 UTC). */
export const INC: [string, string] = ['2026-10-20T08:45', '2026-10-20T09:00'];
export const REF: [string, string] = ['2026-10-20T08:30', '2026-10-20T08:45'];

export async function openGuided(page: Page, caseTitle = 'Checkout slowdown, 17 October'): Promise<void> {
  await goHome(page);
  await openCase(page, caseTitle);
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
  await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
}

export async function setWindows(page: Page, inc: [string, string] = INC, ref: [string, string] = REF): Promise<void> {
  const incident = page.getByRole('group', { name: 'Incident window' });
  const reference = page.getByRole('group', { name: 'Reference window (normal)' });
  await incident.getByLabel('From').fill(inc[0]);
  await incident.getByLabel('To').fill(inc[1]);
  await reference.getByLabel('From').fill(ref[0]);
  await reference.getByLabel('To').fill(ref[1]);
}

export function stepRail(page: Page) {
  return page.getByRole('navigation', { name: 'Guided analysis steps' });
}

/** Set the windows, press Run the steps and wait until no step is Waiting or Running. */
export async function runSteps(page: Page, inc: [string, string] = INC, ref: [string, string] = REF): Promise<void> {
  await setWindows(page, inc, ref);
  await page.getByRole('button', { name: 'Run the steps' }).click();
  await expect(stepRail(page).getByRole('button', { name: /Waiting|Running/ })).toHaveCount(0);
}

/** Open step n from the rail; the section of that step is returned. */
export async function gotoStep(page: Page, n: number) {
  await stepRail(page).getByRole('button', { name: new RegExp('^' + n + ' ') }).click();
  await expect(page.locator('#step-title-' + n)).toBeFocused();
  return page.locator('section:has(#step-title-' + n + ')');
}
