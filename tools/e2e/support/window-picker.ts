// How the specs drive the Investigation Timeline's two period pickers (WindowPicker.svelte, popover variant).
// Each picker is a button that opens a dialog with the quick ranges, a calendar, two date text boxes and two 12-hour time boxes (each time
// is three drop-downs: hour 1 to 12, minute, AM or PM; all UTC). Apply commits; Cancel, Escape and a click outside discard.
// The specs keep speaking 24-hour `HH:mm`: fillTime and readTime translate to and from the three drop-downs.
import { expect, type Locator, type Page } from '@playwright/test';

export type WhichWindow = 'incident' | 'comparison';

/** The picker names without the "(UTC)" tag; every spoken name says UTC once, at the end. */
export const WINDOW_NAME: Record<WhichWindow, string> = { incident: 'Incident window', comparison: 'Comparison period' };
const DIALOG_NAME: Record<WhichWindow, string> = { incident: 'Choose the incident window (UTC)', comparison: 'Choose the comparison period (UTC)' };

/** The button that shows the period and opens the picker. */
export const pickerButton = (page: Page, which: WhichWindow): Locator => page.getByRole('button', { name: new RegExp('^' + WINDOW_NAME[which] + ': ') });
export const pickerDialog = (page: Page, which: WhichWindow): Locator => page.getByRole('dialog', { name: DIALOG_NAME[which] });

/** One of the two date text boxes inside the open picker (the times are 12-hour boxes: see pickerTime). */
export const pickerBox = (page: Page, which: WhichWindow, part: 'start date' | 'end date'): Locator =>
  pickerDialog(page, which).getByRole('textbox', { name: `${WINDOW_NAME[which]} ${part}`, exact: true });

/** The 12-hour time box (a group of three drop-downs) inside the open picker. */
export const pickerTime = (page: Page, which: WhichWindow, part: 'start time' | 'end time'): Locator =>
  pickerDialog(page, which).getByRole('group', { name: `${WINDOW_NAME[which]} ${part}`, exact: true });

const timeSelectIn = (scope: Locator, boxName: string, word: 'hour' | 'minute' | 'AM or PM'): Locator => scope.getByRole('combobox', { name: `${boxName} ${word}`, exact: true });

/** Sets a 12-hour time box (found by its spoken name, for example "Incident window start time") inside `scope` from a 24-hour `HH:mm`. 22:30 picks 10, 30, PM; '' clears the hour and the minute. */
export async function fillTimeIn(scope: Locator, boxName: string, hhmm: string): Promise<void> {
  const m = /^(\d{2}):(\d{2})$/.exec(hhmm);
  if (!m) {
    await timeSelectIn(scope, boxName, 'hour').selectOption('');
    await timeSelectIn(scope, boxName, 'minute').selectOption('');
    return;
  }
  const h = +m[1];
  await timeSelectIn(scope, boxName, 'AM or PM').selectOption(h >= 12 ? 'PM' : 'AM');
  await timeSelectIn(scope, boxName, 'hour').selectOption(String(h % 12 === 0 ? 12 : h % 12));
  await timeSelectIn(scope, boxName, 'minute').selectOption(String(+m[2]));
}

/** Reads a 12-hour time box back as a 24-hour `HH:mm`, or '' when the hour and the minute show "--". */
export async function readTimeIn(scope: Locator, boxName: string): Promise<string> {
  const hour = await timeSelectIn(scope, boxName, 'hour').inputValue();
  const minute = await timeSelectIn(scope, boxName, 'minute').inputValue();
  const period = await timeSelectIn(scope, boxName, 'AM or PM').inputValue();
  if (hour === '' || minute === '') return '';
  return `${String((+hour % 12) + (period === 'PM' ? 12 : 0)).padStart(2, '0')}:${minute.padStart(2, '0')}`;
}

/** Sets one time box of an open picker from a 24-hour `HH:mm`. */
export const fillTime = (page: Page, which: WhichWindow, part: 'start time' | 'end time', hhmm: string): Promise<void> =>
  fillTimeIn(pickerDialog(page, which), `${WINDOW_NAME[which]} ${part}`, hhmm);

/** Reads one time box of an open picker as a 24-hour `HH:mm`. */
export const readTime = (page: Page, which: WhichWindow, part: 'start time' | 'end time'): Promise<string> =>
  readTimeIn(pickerDialog(page, which), `${WINDOW_NAME[which]} ${part}`);

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
  await fillTime(page, which, 'start time', startTime ?? '');
  await pickerBox(page, which, 'end date').fill(endDate ?? '');
  await fillTime(page, which, 'end time', endTime ?? '');
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
  const date = async (part: 'start date' | 'end date') => pickerBox(page, which, part).inputValue();
  const range: [string, string] = [`${await date('start date')}T${await readTime(page, which, 'start time')}`, `${await date('end date')}T${await readTime(page, which, 'end time')}`];
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
  await page.getByRole('button', { name: 'Case home', exact: true }).click();
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
}
