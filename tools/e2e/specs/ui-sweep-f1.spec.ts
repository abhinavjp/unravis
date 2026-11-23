// UI sweep, package F1 (shell, shared CSS and the shared help components). Each test measures the real page in a real browser.
//   SWEEP-F1-01  More filters open at 1280x720: the results keep at least 240px (ANALYSIS-03)
//   SWEEP-F1-02  Opening a field help does not move or stretch the field beside it; select and input are one height (ANALYSIS-05)
//   SWEEP-F1-03  Every "i" of a tab has its own name (ANALYSIS-06)
//   SWEEP-F1-04  The "?" ring is behind its glyph, also on hover and when open, and Esc folds an open help and returns focus (CASES-06)
//   SWEEP-F1-05  Explain simply: a help block is open and its button still folds it (CASES-23)
//   SWEEP-F1-06  Forced colours: the chosen range chip is not drawn like the others (SETUP-A11Y-04)
//   SWEEP-F1-07  A modal drawer at 320x225 keeps its close button in view while the body scrolls (SETUP-A11Y-20)
//   SWEEP-F1-08  One chevron on a details row, pointing right closed and down open (ANALYSIS-14, BLACKBOX-03)
//   SWEEP-F1-09  The wizard bar sits on the window edge while the step scrolls (CASES-01)
//   SWEEP-F1-10  The seven hub tabs sit on one row and the chosen one has a bar under it (CASES-11)
//   SWEEP-F1-11  Secondary buttons and chips have a 3:1 border (SETUP-A11Y-10)
//   SWEEP-F1-12  One button size: sources buttons as tall as the global ones (SETUP-A11Y-11)
import { test, expect, type Page } from '../support/fixtures';

/** The relative luminance contrast of two "rgb(r, g, b)" strings. */
function contrast(a: string, b: string): number {
  const lum = (c: string) => {
    const [r, g, bl] = (/rgba?\(([^)]+)\)/.exec(c)![1].split(/[ ,/]+/).map(Number)).slice(0, 3).map((v) => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4); });
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
  };
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

async function openMoreFilters(page: Page): Promise<void> {
  if (!(await page.locator('details.more-filters[open]').count())) await page.locator('summary', { hasText: 'More filters' }).click();
  await expect(page.getByRole('tab', { name: /^What/ })).toBeVisible();
}

test('SWEEP-F1-01 More filters open at 1280x720: the results area keeps at least 240px', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await openMoreFilters(page);
  for (const tab of [/^Ranges/, /^Request details/, /^When/]) {
    await page.getByRole('tab', { name: tab }).click();
    const stage = await page.evaluate(() => document.querySelector('.stage')!.getBoundingClientRect().height);
    expect(stage, String(tab)).toBeGreaterThanOrEqual(240);
  }
});

test('SWEEP-F1-02 a field help opening does not move or stretch the field beside it', async ({ page }) => {
  await openMoreFilters(page);
  await page.getByRole('tab', { name: /^What/ }).click();
  const measure = () => page.evaluate(() => {
    // The label's place inside its own group of fields, not on the page: a shift of the whole filter panel between the two reads (CI saw one of 8.84 px, from something above
    // the panel, on a run where the same CSS had passed) is not the field beside the help moving, which is what this test is about.
    const label = document.querySelector<HTMLElement>('label[for="rf-completion"]')!.getBoundingClientRect();
    const grid = document.querySelector<HTMLElement>('label[for="rf-completion"]')!.closest('.field-grid')!.getBoundingClientRect();
    const select = document.querySelector<HTMLElement>('#rf-completion')!.getBoundingClientRect();
    const own = document.querySelector<HTMLElement>('#outcome')!.getBoundingClientRect();
    return { labelTop: label.top - grid.top, selectHeight: select.height, ownHeight: own.height };
  });
  const before = await measure();
  await page.getByRole('button', { name: 'About Status' }).click();
  const after = await measure();
  expect(after.labelTop).toBeCloseTo(before.labelTop, 0);
  expect(after.selectHeight).toBeCloseTo(before.selectHeight, 0);
  expect(before.selectHeight).toBe(before.ownHeight);
  // the open "i" is still an "i": its ring sits behind the glyph
  const ring = await page.evaluate(() => getComputedStyle(document.querySelector('.field-help-toggle[aria-expanded="true"]')!, '::before').zIndex);
  expect(Number(ring)).toBeLessThan(0);
});

test('SWEEP-F1-03 the "i" buttons of one tab each have their own name', async ({ page }) => {
  await openMoreFilters(page);
  for (const tab of [/^What/, /^Request details/, /^Chart/]) {
    await page.getByRole('tab', { name: tab }).click();
    const names = await page.evaluate(() => [...document.querySelectorAll<HTMLElement>('.rail-pane:not([hidden]) .field-help-toggle')].map((b) => b.getAttribute('aria-label') ?? ''));
    expect(names.length, String(tab)).toBeGreaterThan(1);
    expect(new Set(names).size, names.join(' | ')).toBe(names.length);
    for (const name of names) expect(name).toMatch(/^About /);
  }
});

test('SWEEP-F1-04 the "?" ring sits behind its glyph, and Esc folds an open help and gives focus back', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases' }).click();
  const toggle = page.locator('.help-toggle').first();
  await expect(toggle).toBeVisible();
  expect(Number(await toggle.evaluate((e) => getComputedStyle(e, '::before').zIndex))).toBeLessThan(0);
  expect(await toggle.evaluate((e) => parseFloat(getComputedStyle(e).fontSize))).toBeGreaterThanOrEqual(11);
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await page.keyboard.press('Escape');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  expect(await toggle.evaluate((e) => document.activeElement === e)).toBe(true);
});

test('SWEEP-F1-05 with Explain simply on, a help block is open and its button still folds it', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases' }).click();
  await page.getByLabel('Explain simply').check();
  const toggle = page.locator('.help-toggle').first();
  await expect(toggle).toBeEnabled();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  const block = page.locator('.help-simple').first();
  await expect(block).toBeVisible();
  expect((await block.boundingBox())!.width).toBeGreaterThan(400);
  await toggle.click();
  await expect(block).toBeHidden();
  await toggle.click();
  await expect(block).toBeVisible();
});

test('SWEEP-F1-06 in forced colours the chosen range chip is drawn differently from the others', async ({ page }) => {
  await page.emulateMedia({ forcedColors: 'active' });
  const colours = await page.evaluate(() => {
    const on = document.querySelector<HTMLElement>('.quick-ranges .chip[aria-pressed="true"]')!;
    const off = document.querySelector<HTMLElement>('.quick-ranges .chip[aria-pressed="false"]')!;
    return { on: getComputedStyle(on).backgroundColor, off: getComputedStyle(off).backgroundColor };
  });
  expect(colours.on).not.toBe(colours.off);
});

test('SWEEP-F1-07 a modal drawer at 320x225 keeps its close button in view while the body scrolls', async ({ page }) => {
  await page.getByRole('button', { name: 'Ledger' }).click();
  await expect(page.locator('.connections-drawer')).toBeVisible();
  await page.setViewportSize({ width: 320, height: 225 });
  const tops = await page.evaluate(() => {
    const drawer = document.querySelector<HTMLElement>('.connections-drawer')!;
    const close = () => document.querySelector<HTMLElement>('.drawer-close')!.getBoundingClientRect().top;
    const before = close();
    drawer.scrollTop = drawer.scrollHeight;
    return { before, after: close(), scrolled: drawer.scrollTop > 0 };
  });
  expect(tops.scrolled).toBe(true);
  expect(tops.after).toBeCloseTo(tops.before, 0);
  expect(tops.after).toBeGreaterThanOrEqual(0);
});

test('SWEEP-F1-08 a details row has one chevron: right when closed, down when open', async ({ page }) => {
  await page.getByRole('button', { name: 'Where is it?' }).first().click();
  const summary = page.locator('summary', { hasText: 'Details for each source and period' });
  await summary.scrollIntoViewIfNeeded();
  const state = () => summary.evaluate((s) => {
    const before = getComputedStyle(s, '::before');
    return { content: before.content, transform: before.transform, after: getComputedStyle(s, '::after').content, listStyle: getComputedStyle(s).listStyleType };
  });
  const closed = await state();
  expect(closed.content).toBe('""');
  expect(closed.after).toBe('none');
  expect(closed.listStyle).toBe('none');
  await summary.click();
  // the chevron turns over a short transition, so read it until it has settled
  await expect.poll(async () => (await state()).transform).not.toBe(closed.transform);
});

test('SWEEP-F1-09 the wizard bar sits on the window edge while a step scrolls', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases' }).click();
  await page.getByRole('button', { name: 'Create a new case' }).click();
  await page.locator('input[type=text]').first().fill('Sweep check');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.locator('.bar')).toBeVisible();
  const edge = await page.evaluate(() => {
    const spacer = document.createElement('div');
    spacer.style.height = '1500px';
    document.querySelector('.step-body')!.appendChild(spacer);
    const stage = document.querySelector<HTMLElement>('.stage')!;
    stage.scrollTop = 300;
    return { bar: Math.round(document.querySelector('.bar')!.getBoundingClientRect().bottom), stage: Math.round(stage.getBoundingClientRect().bottom) };
  });
  expect(edge.bar).toBe(edge.stage);
});

test('SWEEP-F1-10 the hub tabs sit on one row and the chosen one has a bar under it', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases' }).click();
  await page.getByRole('button', { name: /Open case Night batch/ }).click();
  const tabs = page.locator('.src .tabs [role=tab]');
  await expect(tabs.first()).toBeVisible();
  const tops = await tabs.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top)));
  expect(new Set(tops).size, tops.join(',')).toBe(1);
  const selected = page.locator('.src .tabs [role=tab][aria-selected="true"]');
  const bar = await selected.evaluate((e) => getComputedStyle(e).borderBottomWidth);
  expect(bar).toBe('3px');
  // Pointing at the chosen tab must keep its accent bar (the plain button hover turned it grey), and an other tab shows no accent bar.
  const rest = await selected.evaluate((e) => getComputedStyle(e).borderBottomColor);
  await selected.hover();
  expect(await selected.evaluate((e) => getComputedStyle(e).borderBottomColor)).toBe(rest);
  const other = page.locator('.src .tabs [role=tab][aria-selected="false"]').first();
  await other.hover();
  expect(await other.evaluate((e) => getComputedStyle(e).borderBottomColor)).not.toBe(rest);
});

test('SWEEP-F1-12 one button size on the case home: the sources buttons are as tall as the global ones, with the same text size', async ({ page }) => {
  await page.getByRole('button', { name: 'All cases' }).click();
  await page.getByRole('button', { name: /Open case Night batch/ }).click();
  const global = page.getByRole('button', { name: 'Save case file' });
  const source = page.locator('.src button.btn:not([role=tab])').first();
  await expect(global).toBeVisible();
  await expect(source).toBeVisible();
  const [a, b] = await Promise.all([global, source].map((l) => l.evaluate((e) => ({ h: e.getBoundingClientRect().height, f: getComputedStyle(e).fontSize }))));
  expect(Math.abs(a.h - b.h), JSON.stringify({ a, b })).toBeLessThanOrEqual(1);
  expect(a.f).toBe(b.f);
});

test('SWEEP-F1-11 secondary buttons and chips have a border that is at least 3:1 against the page', async ({ page }) => {
  for (const scheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const facts = await page.evaluate(() => {
      const chip = document.querySelector<HTMLElement>('.quick-ranges .chip[aria-pressed="false"]')!;
      const cs = getComputedStyle(chip);
      let bg = 'rgb(0, 0, 0)';
      for (let e: Element | null = chip; e; e = e.parentElement) { const c = getComputedStyle(e).backgroundColor; if (!/rgba\(.*, 0\)|transparent/.test(c)) { bg = c; break; } }
      return { border: cs.borderTopColor, bg };
    });
    expect(contrast(facts.border, facts.bg), scheme).toBeGreaterThanOrEqual(3);
  }
});
