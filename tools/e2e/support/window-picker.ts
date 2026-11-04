// How the specs drive the Investigation Timeline's two period pickers (WindowPicker.svelte, popover variant).
// Each picker is a button that opens a dialog with the quick ranges, a calendar and four text boxes (start date, start time,
// end date, end time, all UTC). Apply commits; Cancel, Escape and a click outside discard.
import { expect, type Locator, type Page } from '@playwright/test';

export type WhichWindow = 'incident' | 'comparison';

/** The picker names without the "(UTC)" tag; every spoken name says UTC once, at the end. */
export const WINDOW_NAME: Record<WhichWindow, string> = { incident: 'Incident window', comparison: 'Comparison period' };
const DIALOG_NAME: Record<WhichWindow, string> = { incident: 'Choose the incident window (UTC)', comparison: 'Choose the comparison period (UTC)' };

/** The button that shows the period and opens the picker. */
export const pickerButton = (page: Page, which: WhichWindow): Locator => page.getByRole('button', { name: new RegExp('^' + WINDOW_NAME[which] + ': ') });
export const pickerDialog = (page: Page, which: WhichWindow): Locator => page.getByRole('dialog', { name: DIALOG_NAME[which] });

/** One of the four text boxes inside the open picker. */
export const pickerBox = (page: Page, which: WhichWindow, part: 'start date' | 'start time' | 'end date' | 'end time'): Locator =>
  pickerDialog(page, which).getByRole('textbox', { name: `${WINDOW_NAME[which]} ${part}`, exact: true });

/** Opens the picker (if it is closed) and returns its dialog. */
export async function openPicker(page: Page, which: WhichWindow): Promise<Locator> {
  const dialog = pickerDialog(page, which);
  if (!(await dialog.isVisible())) await pickerButton(page, which).click();
  await expect(dialog).toBeVisible();
  return dialog;
}

/** Types a period into the four boxes of the open (or opened) picker without pressing Apply. */
export async function typeRange(page: Page, which: WhichWindow, start: string, end: string): Promise<Locator> {
  const dialog = await openPicker(page, which);
  const [startDate, startTime] = start.split('T');
  const [endDate, endTime] = end.split('T');
  await pickerBox(page, which, 'start date').fill(startDate ?? '');
  await pickerBox(page, which, 'start time').fill(startTime ?? '');
  await pickerBox(page, which, 'end date').fill(endDate ?? '');
  await pickerBox(page, which, 'end time').fill(endTime ?? '');
  return dialog;
}

/** Sets one period the way a person does: open the picker, type the four boxes, press Apply. */
export async function setWindow(page: Page, which: WhichWindow, start: string, end: string): Promise<void> {
  const dialog = await typeRange(page, which, start, end);
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(dialog).toBeHidden();
}

/** Sets the incident window and then the comparison period: [incidentStart, incidentEnd, referenceStart, referenceEnd]. */
export async function fillWindows(page: Page, w: readonly string[]): Promise<void> {
  await setWindow(page, 'incident', w[0], w[1]);
  await setWindow(page, 'comparison', w[2], w[3]);
}

/** Reads one period back from the four boxes (opens the picker, then closes it with Escape). */
export async function readWindow(page: Page, which: WhichWindow): Promise<[string, string]> {
  await openPicker(page, which);
  const get = async (part: 'start date' | 'start time' | 'end date' | 'end time') => pickerBox(page, which, part).inputValue();
  const range: [string, string] = [`${await get('start date')}T${await get('start time')}`, `${await get('end date')}T${await get('end time')}`];
  await page.keyboard.press('Escape');
  await expect(pickerDialog(page, which)).toBeHidden();
  return range;
}

/** The four values in the same order as fillWindows. */
export async function windowValues(page: Page): Promise<string[]> {
  return [...(await readWindow(page, 'incident')), ...(await readWindow(page, 'comparison'))];
}

/**
 * Puts values into the kept windows of this browser tab that the picker itself will not let a person apply (an end before the start,
 * an empty box), then opens the Investigation Timeline again so it starts from them. This is the only way to reach the screen's own
 * refusals for those values, which a person gets by reloading a tab that holds bad values.
 */
export async function primeWindows(page: Page, w: readonly string[]): Promise<void> {
  // The screen keeps windows only after an edit, so make one first (the stored values are replaced just below).
  await setWindow(page, 'incident', '2026-10-18T00:00', '2026-10-18T00:01');
  const wrote = await page.evaluate((values) => {
    const keys = Object.keys(window.sessionStorage).filter((k) => k.startsWith('unravis.tracing.windows.'));
    for (const key of keys) {
      window.sessionStorage.setItem(key, JSON.stringify({ incidentStart: values[0], incidentEnd: values[1], referenceStart: values[2], referenceEnd: values[3] }));
    }
    return keys.length;
  }, w);
  expect(wrote, 'the screen kept the windows of this case').toBe(1);
  await page.getByRole('button', { name: 'Back to the case' }).click();
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
}
