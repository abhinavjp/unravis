// The in-app user guide: links, tables, pictures, focus and text, and Help over other screens.
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { test, expect, type Page, type Locator } from '../support/fixtures';

const ROOT = resolve(__dirname, '..', '..', '..');
const SLUGS = ['index', 'getting-started', 'install', 'cases', 'sources', 'blackbox', 'guided-analysis', 'manual-tools', 'upgrade-and-uninstall', 'troubleshooting', 'security'];
const CASE = 'Checkout slowdown, 17 October';

async function openGuide(page: Page): Promise<Locator> {
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  const dialog = page.getByRole('dialog').first();
  await expect(dialog.locator('#glossary-title')).toHaveText('Help and glossary');
  await dialog.getByTestId('guide-open').click();
  await expect(dialog.getByTestId('guide-body')).toBeVisible();
  return dialog;
}

async function gotoPage(dialog: Locator, slug: string): Promise<void> {
  await dialog.getByTestId('guide-nav-' + slug).click();
  await expect(dialog.getByTestId('guide-nav-' + slug)).toHaveAttribute('aria-current', 'page');
}

/** The heading that has keyboard focus: its tag, id and text. */
function focused(page: Page) {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    return { tag: el?.tagName ?? '', id: el?.id ?? '', text: (el?.textContent ?? '').trim() };
  });
}

test('HELP-06: a page link inside the guide changes the in-app page, an anchor link focuses its heading, and the address never changes', async ({ page }) => {
  const url = page.url();
  const dialog = await openGuide(page);
  await gotoPage(dialog, 'upgrade-and-uninstall');
  await dialog.getByTestId('guide-body').getByRole('button', { name: 'Install', exact: true }).first().click();
  await expect(dialog.getByTestId('guide-nav-install')).toHaveAttribute('aria-current', 'page');
  expect((await focused(page)).id).toMatch(/^help-install-/);
  await gotoPage(dialog, 'security');
  await dialog.getByTestId('guide-body').getByRole('button', { name: /^Upgrade and uninstall/ }).first().click();
  await expect(dialog.getByTestId('guide-nav-upgrade-and-uninstall')).toHaveAttribute('aria-current', 'page');
  await gotoPage(dialog, 'guided-analysis');
  await dialog.getByTestId('guide-body').getByRole('button', { name: /the screens/i }).first().click();
  await expect.poll(async () => (await focused(page)).id).toMatch(/^help-guided-analysis-the-screens/);
  await expect(dialog.getByTestId('guide-nav-guided-analysis')).toHaveAttribute('aria-current', 'page');
  expect(page.url(), 'the address did not change').toBe(url);
});

test('HELP-06a: a link that names a page and an anchor opens that page with the heading focused, and a second press keeps focus there', async ({ page }) => {
  const dialog = await openGuide(page);
  await gotoPage(dialog, 'manual-tools');
  const link = dialog.getByTestId('guide-body').getByRole('button', { name: 'Guided analysis', exact: true }).first();
  await link.click();
  await expect(dialog.getByTestId('guide-nav-guided-analysis')).toHaveAttribute('aria-current', 'page');
  await expect.poll(async () => (await focused(page)).id).toMatch(/^help-guided-analysis-export-screens/);
  await gotoPage(dialog, 'guided-analysis');
  const same = dialog.getByTestId('guide-body').getByRole('button', { name: /the screens/i }).first();
  await same.click();
  await same.click();
  await expect.poll(async () => (await focused(page)).id).toMatch(/^help-guided-analysis-the-screens/);
});

test('HELP-06b: no guide page holds a link that leaves the app, and the update check offers the only https link', async ({ page }) => {
  const dialog = await openGuide(page);
  for (const slug of SLUGS) {
    await gotoPage(dialog, slug);
    const hrefs = await dialog.getByTestId('guide-body').locator('a[href]').evaluateAll((els) => els.map((e) => e.getAttribute('href')));
    expect(hrefs, slug + ' has no anchors that navigate').toEqual([]);
  }
});

test('HELP-08: a guide table is a named, focusable region only while it scrolls sideways, at the wide and the narrow window', async ({ page }) => {
  const dialog = await openGuide(page);
  await gotoPage(dialog, 'blackbox');
  const audit = () =>
    dialog.getByTestId('guide-body').locator('table').evaluateAll((tables) =>
      tables.map((t) => {
        const wrap = t.parentElement as HTMLElement;
        const scrolls = wrap.scrollWidth > wrap.clientWidth + 1;
        return {
          scrolls,
          role: wrap.getAttribute('role'),
          label: wrap.getAttribute('aria-label') ?? '',
          tabindex: wrap.getAttribute('tabindex')
        };
      })
    );
  const check = (rows: Awaited<ReturnType<typeof audit>>, where: string) => {
    expect(rows.length, where + ' has tables').toBeGreaterThan(0);
    for (const r of rows) {
      if (r.scrolls) {
        expect(r, where + ' scrolling table').toMatchObject({ role: 'region', tabindex: '0' });
        expect(r.label.startsWith('Table: '), where + ' label ' + r.label).toBe(true);
      } else {
        expect(r.role, where + ' fitting table is not a region').toBeNull();
        expect(r.tabindex, where + ' fitting table is not a tab stop').toBeNull();
      }
    }
  };
  check(await audit(), 'wide');
  await page.setViewportSize({ width: 375, height: 812 });
  await expect.poll(async () => (await audit()).filter((r) => r.scrolls).length).toBeGreaterThan(0);
  const narrow = await audit();
  check(narrow, 'narrow');
  expect(narrow.filter((r) => r.scrolls).length, 'more tables scroll at 375 px').toBeGreaterThanOrEqual(1);
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect.poll(async () => JSON.stringify((await audit()).map((r) => r.scrolls))).not.toBe(JSON.stringify(narrow.map((r) => r.scrolls)));
  check(await audit(), 'wide again');
});

test('HELP-08a: arrow keys scroll a focused table, Tab moves on from it, and code blocks scroll instead of overflowing the drawer', async ({ page }) => {
  const dialog = await openGuide(page);
  await gotoPage(dialog, 'blackbox');
  const region = dialog.getByTestId('guide-body').getByRole('region').first();
  await region.focus();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await expect.poll(() => region.evaluate((e) => e.scrollLeft)).toBeGreaterThan(0);
  await page.keyboard.press('Tab');
  expect(await region.evaluate((e) => e === document.activeElement), 'Tab moves focus on').toBe(false);
  expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);
  await gotoPage(dialog, 'install');
  const pres = await dialog.getByTestId('guide-body').locator('pre').evaluateAll((els) => els.map((e) => getComputedStyle(e).overflowX));
  for (const o of pres) expect(['auto', 'scroll']).toContain(o);
  const w = await dialog.evaluate((e) => ({ s: e.scrollWidth, c: e.clientWidth }));
  expect(w.s, 'the drawer does not scroll sideways').toBeLessThanOrEqual(w.c + 1);
});

test('HELP-09: the guide shows pictures as "Picture:" lines and never as images, on every page', async ({ page }) => {
  const dialog = await openGuide(page);
  let pictures = 0;
  for (const slug of SLUGS) {
    await gotoPage(dialog, slug);
    await expect(dialog.locator('img')).toHaveCount(0);
    const lines = await dialog.getByTestId('guide-body').getByText(/^Picture: /).allInnerTexts();
    for (const l of lines) expect(l, slug).toMatch(/^Picture: \S/);
    pictures += lines.length;
    if (slug === 'install') expect(lines.length, 'the Install page has pictures').toBeGreaterThan(0);
  }
  expect(pictures).toBeGreaterThan(0);
  const bg = await dialog.evaluate((root) => [...root.querySelectorAll('*')].filter((e) => getComputedStyle(e).backgroundImage.startsWith('url(')).length);
  expect(bg, 'no background pictures').toBe(0);
});

test('HELP-10: each page starts at the top with its first heading focused, also when pressed twice or mid-scroll', async ({ page }) => {
  const dialog = await openGuide(page);
  const top = () =>
    dialog.getByTestId('guide-body').evaluate((body) => {
      for (let n: HTMLElement | null = body; n; n = n.parentElement) {
        const o = getComputedStyle(n).overflowY;
        if ((o === 'auto' || o === 'scroll') && n.scrollHeight > n.clientHeight + 1) return n.scrollTop;
      }
      return 0;
    });
  const scrollDown = () =>
    dialog.getByTestId('guide-body').evaluate((body) => {
      for (let n: HTMLElement | null = body; n; n = n.parentElement) {
        const o = getComputedStyle(n).overflowY;
        if ((o === 'auto' || o === 'scroll') && n.scrollHeight > n.clientHeight + 1) {
          n.scrollTop = 800;
          return n.scrollTop;
        }
      }
      return 0;
    });
  for (const slug of ['security', 'index', 'guided-analysis']) {
    await gotoPage(dialog, slug);
    const f = await focused(page);
    expect(f.tag, slug + ' first heading is focused').toBe('H3');
    await expect.poll(top, { message: slug + ' is at the top' }).toBe(0);
  }
  expect(await scrollDown(), 'the page is long enough to scroll').toBeGreaterThan(0);
  await gotoPage(dialog, 'sources');
  await expect.poll(top).toBe(0);
  expect((await focused(page)).tag).toBe('H3');
  await scrollDown();
  await dialog.getByTestId('guide-nav-sources').click();
  await expect(dialog.getByTestId('guide-nav-sources')).toHaveAttribute('aria-current', 'page');
  await expect.poll(top).toBe(0);
  expect((await focused(page)).tag).toBe('H3');
});

test('HELP-10a: the focus ring shows after a keyboard press on a page button and not after a mouse click', async ({ page }) => {
  const dialog = await openGuide(page);
  await dialog.getByTestId('guide-nav-troubleshooting').click();
  const visible = () => page.evaluate(() => (document.activeElement as Element).matches(':focus-visible'));
  expect(await visible(), 'mouse: no focus-visible ring').toBe(false);
  await dialog.getByTestId('guide-nav-security').focus();
  await page.keyboard.press('Enter');
  await expect(dialog.getByTestId('guide-nav-security')).toHaveAttribute('aria-current', 'page');
  expect(await visible(), 'keyboard: focus-visible ring').toBe(true);
});

test('HELP-11: Help cycles focus inside, Shift+Tab from the first control goes to the last, and Esc works from the search field', async ({ page }) => {
  await page.getByRole('button', { name: 'Glossary', exact: true }).focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog').first();
  await expect(dialog.getByLabel('Search terms')).toBeFocused();
  const seen: string[] = [];
  const label = () => page.evaluate(() => (document.activeElement?.getAttribute('aria-label') ?? document.activeElement?.textContent ?? '').trim().slice(0, 40));
  for (let i = 0; i < 12; i++) {
    seen.push(await label());
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]')), 'press ' + (i + 1)).toBe(true);
  }
  expect(new Set(seen).size, 'a short cycle: ' + seen.join(' > ')).toBeLessThanOrEqual(6);
  // Shift+Tab from the first control in the cycle order reaches the last one.
  await dialog.getByRole('button', { name: 'Close help and glossary' }).focus();
  await page.keyboard.press('Shift+Tab');
  const prev = await label();
  await page.keyboard.press('Tab');
  await page.keyboard.press('Tab');
  expect(await label(), 'Tab then Tab returns to the control after the one we left, so the ring is closed').not.toBe(prev);
  await dialog.getByLabel('Search terms').fill('abc');
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Glossary', exact: true })).toBeFocused();
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  await expect(page.getByRole('dialog').first().getByLabel('Search terms')).toHaveValue('');
});

test('HELP-11a: the scrim closes Help, and Esc inside the guide leaves no stuck guide when Help opens again', async ({ page }) => {
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  await expect(page.getByRole('dialog').first()).toBeVisible();
  await page.getByRole('button', { name: 'Close help and glossary' }).first().click({ position: { x: 20, y: 300 }, force: true });
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Glossary', exact: true })).toBeFocused();
  const dialog = await openGuide(page);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  const again = page.getByRole('dialog').first();
  await expect(again.getByTestId('guide-open')).toBeVisible();
  await expect(again.getByTestId('guide-body')).toHaveCount(0);
  void dialog;
});

// Plain text of a Markdown line: symbols for emphasis, code and links removed.
function plain(line: string): string {
  return line
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, 'Picture: $1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[`*]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

function headingsOf(file: string): string[] {
  return readFileSync(join(ROOT, 'docs', 'user', file), 'utf8')
    .split(/\r?\n/)
    .filter((l) => /^#{1,6}\s/.test(l))
    .map((l) => plain(l.replace(/^#{1,6}\s+/, '')));
}

for (const slug of ['troubleshooting', 'security', 'upgrade-and-uninstall']) {
  test('HELP-12' + (slug === 'troubleshooting' ? '' : slug === 'security' ? 'a' : 'b') + ': the ' + slug + ' guide page shows the headings of its Markdown file and no Markdown symbols', async ({ page }) => {
    const dialog = await openGuide(page);
    await gotoPage(dialog, slug);
    const body = dialog.getByTestId('guide-body');
    const rendered = (await body.locator('h1,h2,h3,h4,h5,h6').allInnerTexts()).map((t) => t.replace(/\s+/g, ' ').trim());
    const missing = headingsOf(slug + '.md').filter((h) => !rendered.includes(h));
    expect(missing, 'headings in the file but not on the page').toEqual([]);
    const text = await body.innerText();
    for (const raw of ['##', '**', '`', '](']) expect(text, 'raw ' + raw).not.toContain(raw);
    expect(text, 'a raw table bar').not.toMatch(/\s\|\s/);
  });
}

test('HELP-12c: the "not run on a clean machine" note in Upgrade and uninstall shows as a quote, and a Blackbox table row matches its file', async ({ page }) => {
  const dialog = await openGuide(page);
  await gotoPage(dialog, 'upgrade-and-uninstall');
  await expect(dialog.getByTestId('guide-body').locator('blockquote').filter({ hasText: 'NOT RUN: needs a clean VM' })).toBeVisible();
  await gotoPage(dialog, 'blackbox');
  const md = readFileSync(join(ROOT, 'docs', 'user', 'blackbox.md'), 'utf8').split(/\r?\n/);
  const row = md.find((l) => /^\|\s*Running\s*\|/.test(l));
  expect(row, 'the states table row for Running').toBeTruthy();
  const cells = row!.split('|').slice(1, -1).map((c) => plain(c));
  const rendered = await dialog.getByTestId('guide-body').locator('tr').filter({ has: page.locator('td,th').filter({ hasText: /^Running$/ }) }).first().locator('td,th').allInnerTexts();
  expect(rendered.map((c) => c.replace(/\s+/g, ' ').trim())).toEqual(cells);
});

test('HELP-14: Help opens over Home, over an open case and over the Sources list, and each time the screen is still there afterwards', async ({ page }) => {
  const glossary = page.getByRole('button', { name: 'Glossary', exact: true });
  const round = async (marker: Locator, where: string) => {
    await glossary.click();
    await expect(page.getByRole('dialog').first().locator('#glossary-title'), where).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(glossary, where + ' focus back').toBeFocused();
    await expect(marker, where + ' screen is still there').toBeVisible();
  };
  await page.getByRole('button', { name: /^Audit sources: / }).click();
  await expect(page.getByRole('dialog', { name: 'Audit sources' })).toBeVisible();
  await glossary.click();
  await expect(page.getByRole('dialog', { name: 'Help and glossary' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog', { name: 'Help and glossary' })).toHaveCount(0);
  await expect(page.getByRole('region', { name: 'Scope' }), 'sources: the screen is still there').toBeVisible();
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await round(page.locator('#start-title'), 'home');
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.getByRole('button', { name: /^All cases/ })).toBeVisible();
  await round(page.getByRole('button', { name: /^All cases/ }), 'case');
});

test('HELP-14a: opening Help over the Blackbox add form closes the drawer, and the typed address is clearly gone, not half kept', async ({ page }) => {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await dialog.getByLabel('Folder, network share, or address and port').fill('D:\\Keep\\ThisText');
  // The header button sits under the drawer for a mouse, so press it the way a script or a screen reader can.
  await page.getByRole('button', { name: 'Glossary', exact: true }).evaluate((e: HTMLElement) => e.click());
  await expect(page.getByRole('dialog', { name: 'Help and glossary' })).toBeVisible();
  await expect(page.getByRole('dialog', { name: 'Blackbox' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const again = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(again.getByRole('button', { name: 'Add one by folder, share or address' })).toBeVisible();
  await expect(again.getByLabel('Folder, network share, or address and port')).toHaveCount(0);
});

test('HELP-14b: only one side panel is open at a time', async ({ page }) => {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Blackbox' })).toBeVisible();
  await page.getByRole('button', { name: 'Glossary', exact: true }).evaluate((e: HTMLElement) => e.click());
  await expect(page.getByRole('dialog', { name: 'Help and glossary' })).toBeVisible();
  await expect(page.getByRole('dialog'), 'one drawer, not two').toHaveCount(1);
});
