// BL-495 part: the When filter has a 24-hour time box (BL-181: every time box is 24-hour) in place of the browser's own time box, which showed
// "08:45:00.000 AM" on a 12-hour browser or computer setting. These run the real page in Chromium with the en-US locale (a 12-hour locale, the case
// the old box failed in) and read what the app sends to the API: the typed time must reach the real query exactly as the old box's value did.
//   UIFIX-18a  The four time boxes are 24-hour text boxes: labelled with their zone, no AM or PM anywhere, value in HH:MM:SS.mmm
//   UIFIX-18b  Typed time reaches the real analysis request (start, end, baselineStart, baselineEnd) in the Times in zone (UTC, London, India)
//   UIFIX-18c  Typing short forms, pasting and the arrow keys; the box shows the normal form after
//   UIFIX-18d  Empty, 12-hour and malformed text: the box and its message say so (aria-invalid, alert), the run is held back, and a fix clears it
//   UIFIX-18f  Bad text then a date change: the box and its error stay, Run stays off, and the fixed time sends the new date and time together
//   UIFIX-18e  Tap target 24 px or more, focus order Date then Time, labels, 320/360/390 px do not scroll sideways, both themes keep 4.5:1 text
import { test, expect, type Page } from '../support/fixtures';

test.use({ viewport: { width: 1440, height: 900 }, locale: 'en-US' });

const zoneSelect = (page: Page) => page.getByLabel('Show and type times in');
const boxes = (page: Page) => page.locator('input.time24');
const ids = ['current-start', 'current-end', 'reference-start', 'reference-end'] as const;
const box = (page: Page, id: (typeof ids)[number]) => page.locator(`#${id}-time`);
const echo = (page: Page, which: 'first' | 'last') => page.locator('.utc-echo')[which]();

async function openWhen(page: Page) {
  await page.getByText('More filters').first().click();
  await page.getByText('When', { exact: true }).first().click();
  await expect(box(page, 'current-start')).toBeVisible();
}

/** Press Run analysis and return the body the app sent to /api/analyze. */
async function runAndRead(page: Page): Promise<{ start: string; end: string; baselineStart: string; baselineEnd: string }> {
  const sent = page.waitForRequest((r) => r.url().includes('/api/analyze') && r.method() === 'POST');
  await page.getByRole('button', { name: 'Run analysis' }).click();
  return (await sent).postDataJSON();
}

const clock = (iso: string) => iso.slice(11, 23);

test('UIFIX-18a The When time boxes are 24-hour text boxes, named by their zone, with no AM or PM', async ({ page }) => {
  // The precondition of the bug: this browser really is on a 12-hour clock.
  expect(await page.evaluate(() => new Intl.DateTimeFormat(undefined, { timeStyle: 'short' }).resolvedOptions().hour12)).toBe(true);
  await openWhen(page);
  await expect(page.locator('input[type=time]')).toHaveCount(0);
  await expect(boxes(page)).toHaveCount(4);
  for (const id of ids) {
    const input = box(page, id);
    await expect(input).toHaveAttribute('type', 'text');
    await expect(input).toHaveValue(/^([01]\d|2[0-3]):[0-5]\d:[0-5]\d\.\d{3}$/);
    await expect(input).toHaveAttribute('placeholder', 'HH:MM:SS.mmm');
    expect(await input.getAttribute('aria-describedby')).toContain(`${id}-time-hint`);
    // The label names the zone at the box's own date, the group names From or To.
    await expect(page.getByRole('group', { name: /^(From|To)$/ }).first()).toBeVisible();
    await expect(page.locator(`label[for="${id}-time"]`)).toHaveText(/^Time \((UTC|GMT[+-]?\d*)\)$/);
  }
  const text = await page.locator('.when-windows').innerText();
  expect(text).not.toMatch(/\b[AP]M\b/i);
  const values = await boxes(page).evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value));
  for (const v of values) expect(v).not.toMatch(/[AP]M/i);
});

test('UIFIX-18b Typed times reach the real analysis request in the Times in zone, with the same wire value as the old box', async ({ page }) => {
  await openWhen(page);
  const before = await runAndRead(page);
  await zoneSelect(page).selectOption({ label: 'India' });
  // India is UTC+5:30 all year: 13:05 there is 07:35 UTC. The date part does not change, so only the time part moves.
  const wanted: Record<(typeof ids)[number], string> = { 'current-start': '13:05', 'current-end': '23:59:59.999', 'reference-start': '00:00', 'reference-end': '12:00' };
  for (const id of ids) {
    await box(page, id).fill(wanted[id]);
    await box(page, id).blur();
  }
  await expect(box(page, 'current-start')).toHaveValue('13:05:00.000');
  await expect(box(page, 'reference-start')).toHaveValue('00:00:00.000');
  const sent = await runAndRead(page);
  const dateIndia = async (id: (typeof ids)[number]) => page.locator(`#${id}-date`).inputValue();
  const expectedUtc = (date: string, time: string) => {
    const [h, m, rest = '0'] = time.split(':');
    const [s, ms = '0'] = rest.split('.');
    return new Date(Date.UTC(+date.slice(0, 4), +date.slice(5, 7) - 1, +date.slice(8, 10), +h, +m - 330, +s, +ms.padEnd(3, '0'))).toISOString();
  };
  expect(sent.start).toBe(expectedUtc(await dateIndia('current-start'), '13:05'));
  expect(sent.end).toBe(expectedUtc(await dateIndia('current-end'), '23:59:59.999'));
  expect(sent.baselineStart).toBe(expectedUtc(await dateIndia('reference-start'), '00:00'));
  expect(sent.baselineEnd).toBe(expectedUtc(await dateIndia('reference-end'), '12:00'));
  expect(clock(sent.start)).toMatch(/:(05|35|0|3)/);
  expect(sent.start).not.toBe(before.start);
  await expect(echo(page, 'first')).toContainText(sent.start);
  // The same typed text in UTC means that clock time in UTC, with nothing shifted.
  await zoneSelect(page).selectOption({ label: 'UTC' });
  await box(page, 'current-start').fill('13:05');
  await box(page, 'current-start').blur();
  const utc = await runAndRead(page);
  expect(clock(utc.start)).toBe('13:05:00.000');
  expect(utc.start.slice(0, 10)).toBe(await page.locator('#current-start-date').inputValue());
  // Quick ranges still win over a typed value, and the box follows them.
  await page.getByRole('button', { name: 'Last hour' }).first().click();
  const range = await runAndRead(page);
  await expect(box(page, 'current-start')).toHaveValue(clock(range.start));
});

test('UIFIX-18c Short forms, paste and the arrow keys', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await openWhen(page);
  await zoneSelect(page).selectOption({ label: 'UTC' });
  const input = box(page, 'current-start');
  await input.fill('9:07:08.5');
  await input.press('Enter');
  await expect(input).toHaveValue('09:07:08.500');
  await expect(echo(page, 'first')).toContainText('T09:07:08.500Z');
  // Typing key by key is never rewritten under the caret: the full text is there at the end and the query holds it.
  await input.fill('');
  await input.pressSequentially('13:05:30.250');
  await expect(input).toHaveValue('13:05:30.250');
  await expect(echo(page, 'first')).toContainText('T13:05:30.250Z');
  await input.press('Enter');
  await expect(input).not.toHaveAttribute('aria-invalid', 'true');
  // Paste: committed at once, without leaving the box.
  await page.evaluate(() => navigator.clipboard.writeText(' 21:45:10.250 '));
  await input.focus();
  await input.press('Control+a');
  await page.keyboard.press('Control+v');
  await expect(input).toHaveValue('21:45:10.250');
  await expect(echo(page, 'first')).toContainText('T21:45:10.250Z');
  // Arrow keys step the part under the caret and wrap inside it; each press is a change of the real value.
  await input.focus();
  await input.evaluate((e) => (e as HTMLInputElement).setSelectionRange(1, 1));
  await page.keyboard.press('ArrowUp');
  await expect(input).toHaveValue('22:45:10.250');
  await expect(echo(page, 'first')).toContainText('T22:45:10.250Z');
  await page.keyboard.press('ArrowUp');
  await page.keyboard.press('ArrowUp');
  await expect(input).toHaveValue('00:45:10.250');
  await page.keyboard.press('ArrowDown');
  await expect(input).toHaveValue('23:45:10.250');
  await input.evaluate((e) => (e as HTMLInputElement).setSelectionRange(4, 4));
  await page.keyboard.press('ArrowUp');
  await expect(input).toHaveValue('23:46:10.250');
  expect(await input.evaluate((e) => [(e as HTMLInputElement).selectionStart, (e as HTMLInputElement).selectionEnd])).toEqual([3, 5]);
  // Alt and Ctrl keep their own meaning: no step.
  await page.keyboard.press('Control+ArrowUp');
  await expect(input).toHaveValue('23:46:10.250');
});

test('UIFIX-18d Empty, 12-hour and malformed text is refused with a message, holds the run back, and a valid time clears it', async ({ page }) => {
  await openWhen(page);
  await zoneSelect(page).selectOption({ label: 'UTC' });
  const input = box(page, 'current-end');
  const good = await input.inputValue();
  const goodEcho = await echo(page, 'first').innerText();
  let sent = 0;
  page.on('request', (r) => { if (r.url().includes('/api/analyze')) sent++; });
  const refused: [string, RegExp][] = [
    ['8:45 PM', /24-hour form/],
    ['25:10', /24-hour form/],
    ['13:60', /24-hour form/],
    ['banana', /24-hour form/],
    ['', /Enter both a date and time/]
  ];
  for (const [typed, message] of refused) {
    await input.fill(typed);
    await input.blur();
    await expect(input, JSON.stringify(typed)).toHaveAttribute('aria-invalid', 'true');
    const alert = page.locator('#when-time-error');
    await expect(alert).toHaveAttribute('role', 'alert');
    await expect(alert).toHaveText(message);
    expect(await input.getAttribute('aria-describedby')).toContain('when-time-error');
    // The run button is off, so no request can be sent.
    await expect(page.getByRole('button', { name: 'Run analysis' })).toBeDisabled();
  }
  expect(sent, 'requests sent while a time is invalid').toBe(0);
  await input.fill('10:30');
  await input.blur();
  await expect(input).not.toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#when-time-error')).toHaveCount(0);
  await expect(input).toHaveValue('10:30:00.000');
  expect(good).not.toBe('10:30:00.000');
  expect(goodEcho).toContain('UTC');
  const body = await runAndRead(page);
  expect(clock(body.end)).toBe('10:30:00.000');
});

const contrast = (a: number[], b: number[]) => {
  const lum = (c: number[]) => { const [r, g, bl] = c.map((v) => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * bl; };
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

test('UIFIX-18e The boxes are 24 px or more, in order, readable in both themes, and 320 360 390 px do not scroll sideways', async ({ page }) => {
  await openWhen(page);
  // Focus order inside a From box: Date, then Time, then the next control.
  await page.locator('#current-start-date').focus();
  await page.keyboard.press('Tab'); // the date input is a three-part box in Chromium: walk on until the time box
  for (let i = 0; i < 6 && !(await box(page, 'current-start').evaluate((e) => e === document.activeElement)); i++) await page.keyboard.press('Tab');
  await expect(box(page, 'current-start')).toBeFocused();
  for (const id of ids) {
    const r = await box(page, id).boundingBox();
    expect(r!.height, id + ' height').toBeGreaterThanOrEqual(24);
    expect(r!.width, id + ' width').toBeGreaterThanOrEqual(24);
  }
  for (const scheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const facts = await box(page, 'current-start').evaluate((e) => {
      const rgb = (s: string) => (s.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
      const s = getComputedStyle(e);
      return { text: rgb(s.color), back: rgb(s.backgroundColor) };
    });
    expect(contrast(facts.text, facts.back), scheme + ' text contrast').toBeGreaterThanOrEqual(4.5);
    // The error colour is also visible against the background in this theme.
    await box(page, 'current-start').fill('x');
    await box(page, 'current-start').blur();
    const border = await box(page, 'current-start').evaluate((e) => (getComputedStyle(e).borderTopColor.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number));
    expect(contrast(border, facts.back), scheme + ' error border').toBeGreaterThanOrEqual(3);
    await box(page, 'current-start').fill('08:00');
    await box(page, 'current-start').blur();
  }
  for (const width of [1440, 1100, 390, 360, 320]) {
    await page.setViewportSize({ width, height: 844 });
    const facts = await page.evaluate(() => {
      const root = document.documentElement;
      const boxes = [...document.querySelectorAll<HTMLElement>('input.time24')].map((e) => { const r = e.getBoundingClientRect(); return { left: r.left, right: r.right, w: r.width, h: r.height }; });
      // Name the widest things when it scrolls, so a failure says what overflows.
      const wide = root.scrollWidth > root.clientWidth
        ? [...document.querySelectorAll<HTMLElement>('body *')].filter((e) => e.getBoundingClientRect().right > root.clientWidth + 0.5).slice(0, 5).map((e) => e.tagName.toLowerCase() + '.' + String(e.className).slice(0, 40) + ' ' + Math.round(e.getBoundingClientRect().right))
        : [];
      return { scrolls: root.scrollWidth > root.clientWidth, inner: window.innerWidth, boxes, wide };
    });
    expect(facts.scrolls, width + ' px: the page scrolls sideways: ' + JSON.stringify(facts.wide)).toBe(false);
    for (const b of facts.boxes) {
      expect(b.left, width + ' px: box inside the window').toBeGreaterThanOrEqual(0);
      expect(b.right, width + ' px: box inside the window').toBeLessThanOrEqual(facts.inner + 0.5);
      expect(b.h).toBeGreaterThanOrEqual(24);
      expect(b.w, width + ' px: wide enough to show 12 characters').toBeGreaterThanOrEqual(90);
    }
  }
});

test('UIFIX-18f Refused text stays refused when only the date changes; fixing the time then sends the new date with it', async ({ page }) => {
  await openWhen(page);
  await zoneSelect(page).selectOption({ label: 'UTC' });
  const input = box(page, 'current-end');
  const date = page.locator('#current-end-date');
  let sent = 0;
  page.on('request', (r) => { if (r.url().includes('/api/analyze')) sent++; });
  await input.fill('2:30 PM');
  await input.blur();
  await expect(input).toHaveAttribute('aria-invalid', 'true');
  await date.fill('2026-10-20');
  await date.blur();
  // Nothing may look fine while the box still shows text the run would not use.
  await expect(input).toHaveValue('2:30 PM');
  await expect(input).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#when-time-error')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run analysis' })).toBeDisabled();
  expect(sent).toBe(0);
  // Fixing the time sends the date the person chose, not the older one.
  await input.fill('14:30');
  await input.blur();
  await expect(input).not.toHaveAttribute('aria-invalid', 'true');
  const body = await runAndRead(page);
  expect(body.end).toBe('2026-10-20T14:30:00.000Z');
  // An empty box is the same: a date change keeps it refused.
  await input.fill('');
  await input.blur();
  await date.fill('2026-10-20');
  await date.blur();
  await expect(input).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByRole('button', { name: 'Run analysis' })).toBeDisabled();
  // ArrowUp on an empty box fills a time, as the native box did.
  await input.focus();
  await page.keyboard.press('ArrowUp');
  await expect(input).toHaveValue('00:00:00.000');
  await expect(input).not.toHaveAttribute('aria-invalid', 'true');
});

test('UIFIX-18g The time box uses the same mono face as the Date box and a keypad that has a colon', async ({ page }) => {
  await openWhen(page);
  const fonts = await page.evaluate(() => [document.querySelector('#current-start-date'), document.querySelector('#current-start-time')].map((e) => getComputedStyle(e!).fontFamily));
  expect(fonts[1]).toBe(fonts[0]);
  expect(await box(page, 'current-start').getAttribute('inputmode')).toBeNull();
});
