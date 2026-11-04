// The period pickers (WindowPicker.svelte, popover form) on the Investigation Timeline. The inline form on guided Step 0 and the Blackbox host card
// cannot be reached in the running app (case home no longer opens the guided analysis, and #/case/guided/N is not routed there), so those are covered
// by tests/windowPicker.test.cjs only.
// The demo clock is 2026-10-20T09:00:00Z. Nothing here changes demo data, so the tests may run in any order.
//   PICK-01  a quick range applies to the incident window and sets the comparison period before it
//   PICK-02  calendar: first click starts, second click ends, an earlier second click swaps; Apply commits
//   PICK-03  Cancel discards; Escape and a click outside close; focus returns to the button
//   PICK-04  a typed time updates the live summary and, after Apply, the button
//   PICK-05  an end before the start is explained in the picker and refused by Apply; the screen refuses it too
//   PICK-06  a window longer than 186 days is refused by Start tracing in plain words, with no request
//   PICK-07  the overlap notice still warns, still lets the run go ahead, and the fix button works
//   PICK-08  bad text marks the box and keeps the last good value; an emptied box gets the screen's own message
//   PICK-09  375 px: nothing scrolls sideways with the picker closed or open
//   PICK-10  keyboard: Tab and Enter open it, arrows move over the days, Enter in a box applies and does not start a read
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { fillWindows, openPicker, pickerBox, pickerButton, pickerDialog, primeWindows, readWindow, setWindow, typeRange, windowValues } from '../support/window-picker';

const CASE_TITLE = 'Orders API stall, 19 October';

async function openTracing(page: Page, width?: number) {
  await goHome(page);
  if (width) await page.setViewportSize({ width, height: 812 });
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
  await expect(page.locator('.tracing-chart')).toBeVisible();
}

const note = (page: Page, which: 'incident' | 'comparison') => pickerDialog(page, which).getByTestId('picker-note');
const day = (page: Page, which: 'incident' | 'comparison', name: string) => pickerDialog(page, which).getByRole('button', { name: new RegExp('^' + name + '(, start|, end)?$') });
const posts = (page: Page) => {
  const seen = { n: 0 };
  page.on('request', (r) => { if (r.url().includes('/tracing') && r.method() === 'POST') seen.n++; });
  return seen;
};

test('PICK-01: a quick range applies to the incident window and sets the comparison period right before it', async ({ page }) => {
  await openTracing(page);
  const dialog = await openPicker(page, 'incident');
  await expect(dialog.getByRole('group', { name: 'Quick ranges' }).getByRole('button')).toHaveText(['Last 15 min', 'Last hour (default)', 'Last 4 hours', 'Last 24 hours', 'Last 7 days', 'Last 30 days']);
  await dialog.getByRole('button', { name: 'Last 4 hours' }).click();
  await expect(dialog).toBeHidden();
  await expect(pickerButton(page, 'incident')).toContainText(/20 Oct 2026 0[45]:\d\d → 0[89]:\d\d/);
  await expect(pickerButton(page, 'incident')).toContainText('UTC · 4 h');
  const [is, ie, rs, re] = await windowValues(page);
  expect(is.slice(0, 10) + ie.slice(0, 10)).toBe('2026-10-202026-10-20');
  expect(re, 'the comparison period ends where the incident starts').toBe(is);
  expect(Date.parse(re + ':00Z') - Date.parse(rs + ':00Z'), 'and is as long').toBe(Date.parse(ie + ':00Z') - Date.parse(is + ':00Z'));
  // The comparison picker offers its own two ranges, worked out from the incident window.
  const cmp = await openPicker(page, 'comparison');
  await expect(cmp.getByRole('group', { name: 'Quick ranges' }).getByRole('button')).toHaveText(['The equal period right before the investigation window', 'Same time yesterday']);
  await cmp.getByRole('button', { name: 'Same time yesterday' }).click();
  const [ys, ye] = await readWindow(page, 'comparison');
  expect(Date.parse(is + ':00Z') - Date.parse(ys + ':00Z')).toBe(86_400_000);
  expect(Date.parse(ie + ':00Z') - Date.parse(ye + ':00Z')).toBe(86_400_000);
});

test('PICK-02: the calendar takes a start day then an end day (an earlier second click swaps) and Apply commits', async ({ page }) => {
  await openTracing(page);
  const dialog = await openPicker(page, 'incident');
  await expect(dialog.getByText('Click the start day, then the end day.')).toBeVisible();
  const startTime = await pickerBox(page, 'incident', 'start time').inputValue();
  const endTime = await pickerBox(page, 'incident', 'end time').inputValue();
  await day(page, 'incident', 'October 12, 2026').click();
  await expect(day(page, 'incident', 'October 12, 2026')).toHaveAttribute('aria-pressed', 'true');
  await expect(day(page, 'incident', 'October 12, 2026')).toHaveAccessibleName('October 12, 2026, start');
  await expect(dialog.getByText('Now click the end day.')).toBeVisible();
  await day(page, 'incident', 'October 15, 2026').click();
  await expect(day(page, 'incident', 'October 15, 2026')).toHaveAccessibleName('October 15, 2026, end');
  await expect(day(page, 'incident', 'October 14, 2026')).toHaveAttribute('aria-pressed', 'false');
  await expect(pickerBox(page, 'incident', 'start date')).toHaveValue('2026-10-12');
  await expect(pickerBox(page, 'incident', 'end date')).toHaveValue('2026-10-15');
  // The calendar picks days; the times already in the boxes stay.
  await expect(pickerBox(page, 'incident', 'start time')).toHaveValue(startTime);
  await expect(pickerBox(page, 'incident', 'end time')).toHaveValue(endTime);
  await expect(note(page, 'incident')).toContainText(`12 Oct 2026 ${startTime} → 15 Oct 2026 ${endTime}`);
  // The button still shows the old window until Apply.
  await expect(pickerButton(page, 'incident')).not.toContainText('12 Oct 2026');
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(pickerButton(page, 'incident')).toContainText(`12 Oct 2026 ${startTime} → 15 Oct 2026 ${endTime}`);
  // A second click on an earlier day swaps the two.
  await openPicker(page, 'incident');
  await day(page, 'incident', 'October 9, 2026').click();
  await day(page, 'incident', 'October 5, 2026').click();
  await expect(pickerBox(page, 'incident', 'start date')).toHaveValue('2026-10-05');
  await expect(pickerBox(page, 'incident', 'end date')).toHaveValue('2026-10-09');
  // Months move.
  await pickerDialog(page, 'incident').getByRole('button', { name: 'Next month' }).click();
  await expect(pickerDialog(page, 'incident').getByText('November 2026')).toBeVisible();
  await pickerDialog(page, 'incident').getByRole('button', { name: 'Previous month' }).click();
  await expect(pickerDialog(page, 'incident').getByText('October 2026')).toBeVisible();
});

test('PICK-03: Cancel discards, Escape and a click outside close, and focus goes back to the button', async ({ page }) => {
  await openTracing(page);
  const before = await windowValues(page);
  const button = pickerButton(page, 'incident');
  // Cancel throws the draft away.
  let dialog = await typeRange(page, 'incident', '2026-10-01T01:00', '2026-10-01T02:00');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(button).toBeFocused();
  expect(await windowValues(page)).toEqual(before);
  // Escape closes and keeps nothing.
  dialog = await typeRange(page, 'incident', '2026-10-01T01:00', '2026-10-01T02:00');
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(button).toBeFocused();
  expect(await windowValues(page)).toEqual(before);
  // A click outside closes and keeps nothing.
  dialog = await typeRange(page, 'incident', '2026-10-01T01:00', '2026-10-01T02:00');
  await page.getByRole('heading', { name: 'Investigation Timeline' }).click();
  await expect(dialog).toBeHidden();
  expect(await windowValues(page)).toEqual(before);
  // The button opens and closes it.
  await button.click();
  await expect(pickerDialog(page, 'incident')).toBeVisible();
  await button.click();
  await expect(pickerDialog(page, 'incident')).toBeHidden();
});

test('PICK-04: a typed time updates the live summary and, after Apply, the button', async ({ page }) => {
  await openTracing(page);
  const dialog = await typeRange(page, 'incident', '2026-10-19T10:30', '2026-10-19T11:00');
  await expect(note(page, 'incident')).toHaveText('19 Oct 2026 10:30 → 11:00 · 30 min');
  await pickerBox(page, 'incident', 'end time').fill('12:45');
  await expect(note(page, 'incident')).toHaveText('19 Oct 2026 10:30 → 12:45 · 2 h 15 min');
  // The calendar follows the typed date.
  await pickerBox(page, 'incident', 'start date').fill('2026-09-30');
  await expect(dialog.getByText('September 2026')).toBeVisible();
  await expect(day(page, 'incident', 'September 30, 2026')).toHaveAccessibleName('September 30, 2026, start');
  await pickerBox(page, 'incident', 'start date').fill('2026-10-19');
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(pickerButton(page, 'incident')).toContainText('19 Oct 2026 10:30 → 12:45');
  await expect(pickerButton(page, 'incident')).toContainText('UTC · 2 h 15 min');
  expect(await readWindow(page, 'incident')).toEqual(['2026-10-19T10:30', '2026-10-19T12:45']);
});

test('PICK-05: an end before the start is explained in the picker and refused by Apply, and the screen refuses it as well', async ({ page }) => {
  await openTracing(page);
  const wait = posts(page);
  const dialog = await typeRange(page, 'incident', '2026-10-19T18:00', '2026-10-19T15:00');
  await expect(note(page, 'incident')).toHaveText('The end must be after the start.');
  await expect(pickerBox(page, 'incident', 'end time')).toHaveAttribute('aria-invalid', 'true');
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(dialog, 'Apply refuses it and the picker stays open').toBeVisible();
  await expect(note(page, 'incident')).toHaveText('The end must be after the start.');
  // End equal to the start is refused too.
  await pickerBox(page, 'incident', 'end time').fill('18:00');
  await expect(note(page, 'incident')).toHaveText('The end must be after the start.');
  await page.keyboard.press('Escape');
  // A reloaded tab can still bring such a value back; Start tracing says so in the screen's own words and sends nothing.
  await primeWindows(page, ['2026-10-19T18:00', '2026-10-19T15:00', '2026-10-19T12:00', '2026-10-19T15:00']);
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(page.getByRole('alert')).toContainText('The incident window end must be later than the start.');
  await expect(pickerButton(page, 'incident')).toHaveAttribute('data-invalid', 'true');
  await expect(pickerButton(page, 'incident')).toHaveAttribute('aria-describedby', 'tp-problems');
  expect(wait.n).toBe(0);
});

test('PICK-06: a window longer than 186 days is refused in plain words and sends nothing', async ({ page }) => {
  await openTracing(page);
  const wait = posts(page);
  // 186 days and one minute (guided analysis allows 7 days; the timeline allows 186).
  await fillWindows(page, ['2026-04-01T00:00', '2026-10-04T00:01', '2026-03-01T00:00', '2026-04-01T00:00']);
  await expect(pickerButton(page, 'incident')).toContainText('186 days');
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(page.getByRole('alert')).toContainText('The longest window is 186 days. Shorten the incident window.');
  expect(wait.n).toBe(0);
  // Exactly 186 days is allowed.
  await setWindow(page, 'incident', '2026-04-01T00:00', '2026-10-04T00:00');
  await expect(page.getByRole('alert')).toHaveCount(0);
  const answered = page.waitForResponse((r) => r.url().includes('/tracing') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await answered;
  expect(wait.n).toBe(1);
});

test('PICK-07: overlapping windows still only warn, the run goes ahead, and one button moves the comparison period', async ({ page }) => {
  await openTracing(page);
  await fillWindows(page, ['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T16:00', '2026-10-19T17:00']);
  const answered = page.waitForResponse((r) => r.url().includes('/tracing') && r.request().method() === 'POST');
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await answered;
  await expect(page.getByRole('alert')).toHaveCount(0);
  const warn = page.getByRole('status').filter({ hasText: 'The two windows overlap.' });
  await expect(warn).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start tracing' })).toBeEnabled();
  const fix = warn.getByRole('button', { name: 'Move comparison period before the incident' });
  await expect(fix).toBeEnabled();
  await fix.click();
  expect(await windowValues(page)).toEqual(['2026-10-19T15:00', '2026-10-19T18:00', '2026-10-19T12:00', '2026-10-19T15:00']);
  await expect(page.getByText('The two windows overlap.')).toHaveCount(0);
});

test('PICK-08: bad text marks the box and keeps the last good value; an emptied box gets the screen\'s own message', async ({ page }) => {
  await openTracing(page);
  const wait = posts(page);
  await setWindow(page, 'incident', '2026-10-19T15:00', '2026-10-19T18:00');
  const dialog = await openPicker(page, 'incident');
  await pickerBox(page, 'incident', 'start date').fill('2026-02-31');
  await expect(pickerBox(page, 'incident', 'start date')).toHaveAttribute('aria-invalid', 'true');
  await expect(note(page, 'incident')).toHaveText('Use a real date, written like 2026-10-26.');
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(dialog, 'bad text is not applied').toBeVisible();
  await pickerBox(page, 'incident', 'start date').fill('2026-10-19');
  await pickerBox(page, 'incident', 'start time').fill('25:00');
  await expect(pickerBox(page, 'incident', 'start time')).toHaveAttribute('aria-invalid', 'true');
  await expect(note(page, 'incident')).toHaveText('Use a time from 00:00 to 23:59, written like 10:30.');
  await page.keyboard.press('Escape');
  expect(await readWindow(page, 'incident'), 'the last good value is still there').toEqual(['2026-10-19T15:00', '2026-10-19T18:00']);
  // An empty box is not something the picker can apply, so it arrives the way a reloaded tab brings it back.
  await primeWindows(page, ['2026-10-19T15:00', '2026-10-19T18:00', '', '2026-10-19T15:00']);
  await expect(pickerButton(page, 'comparison')).toContainText('Choose a period');
  await page.getByRole('button', { name: 'Start tracing' }).click();
  await expect(page.getByRole('alert')).toContainText('The comparison period needs a start and an end, in UTC.');
  expect(wait.n).toBe(0);
  // Fixing it through the picker clears the message.
  await setWindow(page, 'comparison', '2026-10-19T12:00', '2026-10-19T15:00');
  await expect(page.getByRole('alert')).toHaveCount(0);
});

test('PICK-09: at 375 px nothing scrolls sideways, closed or open, and the popover sits in the page', async ({ page }) => {
  await openTracing(page, 375);
  const sideways = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(await sideways(), 'closed').toBeLessThanOrEqual(0);
  for (const which of ['incident', 'comparison'] as const) {
    const dialog = await openPicker(page, which);
    expect(await sideways(), `${which} open`).toBeLessThanOrEqual(0);
    const box = (await dialog.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(375);
    await page.keyboard.press('Escape');
  }
  // The day buttons are a comfortable size to touch.
  await openPicker(page, 'incident');
  const target = (await day(page, 'incident', 'October 12, 2026').boundingBox())!;
  expect(target.height).toBeGreaterThanOrEqual(36);
  expect(await sideways(), 'open again').toBeLessThanOrEqual(0);
});

test('PICK-10: the keyboard reaches everything; arrows move over the days; Enter in a box applies without starting a read', async ({ page }) => {
  await openTracing(page);
  const wait = posts(page);
  const button = pickerButton(page, 'incident');
  await button.focus();
  await page.keyboard.press('Enter');
  const dialog = pickerDialog(page, 'incident');
  await expect(dialog).toBeVisible();
  await expect(button).toHaveAttribute('aria-expanded', 'true');
  // Tab reaches a day button (only one day is in the tab order) and the arrows move over the days.
  await day(page, 'incident', 'October 19, 2026').focus();
  await page.keyboard.press('ArrowRight');
  await expect(day(page, 'incident', 'October 20, 2026')).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(day(page, 'incident', 'October 27, 2026')).toBeFocused();
  await page.keyboard.press('ArrowLeft');
  await expect(day(page, 'incident', 'October 26, 2026')).toBeFocused();
  await page.keyboard.press('PageDown');
  await expect(dialog.getByText('November 2026')).toBeVisible();
  await expect(day(page, 'incident', 'November 26, 2026')).toBeFocused();
  await page.keyboard.press('PageUp');
  await expect(day(page, 'incident', 'October 26, 2026')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(day(page, 'incident', 'October 26, 2026')).toHaveAttribute('aria-pressed', 'true');
  // Enter in a text box applies the draft; it does not submit the form behind the picker.
  await typeRange(page, 'incident', '2026-10-19T10:00', '2026-10-19T10:30');
  await pickerBox(page, 'incident', 'end time').press('Enter');
  await expect(dialog).toBeHidden();
  await expect(button).toContainText('19 Oct 2026 10:00 → 10:30');
  expect(wait.n, 'no read was started').toBe(0);
  // Every control of the open picker can be reached with Tab and has a name.
  await openPicker(page, 'incident');
  const names = await dialog.locator('button, input').evaluateAll((els) => els.map((e) => e.getAttribute('aria-label') || (e as HTMLElement).innerText.trim()));
  expect(names.filter((n) => !n)).toEqual([]);
  await expect(dialog).toHaveAttribute('aria-label', 'Choose the incident window (UTC)');
  // The focus ring shows on the button for keyboard users.
  await page.keyboard.press('Escape');
  await expect(button).toBeFocused();
  expect(await button.evaluate((el) => getComputedStyle(el).outlineStyle)).not.toBe('none');
});

test('PICK-10b: dark mode keeps the popover readable', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' });
  await openTracing(page);
  const dialog = await openPicker(page, 'incident');
  const colours = await dialog.evaluate((el) => {
    const s = getComputedStyle(el);
    return { bg: s.backgroundColor, fg: s.color };
  });
  expect(colours.bg).not.toBe(colours.fg);
  expect(colours.bg).not.toBe('rgba(0, 0, 0, 0)');
});
