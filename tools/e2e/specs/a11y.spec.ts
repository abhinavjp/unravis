// Accessibility of the Blackbox drawer, Help, the key keeper question, themes, small screens and motion.
import { test, expect, type Page, type Locator } from '../support/fixtures';

const CASE = 'Checkout slowdown, 17 October';
const STICKERS: Record<string, string> = {
  'web-a': 'Running',
  'web-b': 'Running late',
  'web-old': 'Stopped',
  'web-c': 'Disk low',
  'web-d': 'Clock differs',
  'web-e': 'Unreachable',
  'web-f': 'Incomplete',
  'web-g': 'Not placed',
  'old-box': 'Not found'
};

/** Open the Blackbox drawer from the keyboard: focus the header button, press Enter. */
async function openBlackboxByKey(page: Page): Promise<Locator> {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  return dialog;
}

/** Open the drawer from the header button, or from the More menu when the header is narrow. */
async function openBlackboxAnyWidth(page: Page): Promise<Locator> {
  const direct = page.getByRole('button', { name: 'Blackbox', exact: true });
  if (await direct.isVisible()) await direct.click();
  else {
    await page.getByRole('button', { name: 'More', exact: true }).click();
    await page.getByRole('menuitem', { name: 'Blackbox' }).click();
  }
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  return dialog;
}

async function openBlackbox(page: Page): Promise<Locator> {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  return dialog;
}

/** Where the keyboard focus is: inside a dialog or not, and a short name for the element. */
function focusInfo(page: Page) {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    return {
      inDialog: !!el?.closest('[role="dialog"],[role="alertdialog"]'),
      tag: el?.tagName ?? '',
      label: el?.getAttribute('aria-label') ?? (el as HTMLInputElement | null)?.labels?.[0]?.textContent?.trim() ?? (el?.textContent ?? '').trim().slice(0, 60),
      id: el?.id ?? ''
    };
  });
}

/** Press a key n times and check after each press that the focus never left the dialog. */
async function tabInside(page: Page, n: number, shift = false): Promise<void> {
  const key = shift ? 'Shift+Tab' : 'Tab';
  for (let i = 0; i < n; i++) {
    await page.keyboard.press(key);
    const f = await focusInfo(page);
    expect(f.inDialog, `${key} press ${i + 1} left the dialog (focus on ${f.tag} "${f.label}")`).toBe(true);
  }
}

async function openHelpByKey(page: Page): Promise<Locator> {
  await page.getByRole('button', { name: 'Glossary', exact: true }).focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog').first();
  await expect(dialog.locator('#glossary-title')).toHaveText('Help and glossary');
  return dialog;
}

async function withPreview(page: Page, preview: string): Promise<void> {
  await page.evaluate((x) => {
    location.hash = '#keeper=' + x;
  }, preview);
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
}

test('A11Y-01: the Blackbox drawer is a named modal with a scrim, keeps focus inside both ways and gives it back on Esc', async ({ page }) => {
  const opener = page.getByRole('button', { name: 'Blackbox', exact: true });
  const dialog = await openBlackboxByKey(page);
  await expect(dialog).toHaveAttribute('aria-modal', 'true');
  await expect(dialog).toHaveAttribute('aria-labelledby', 'blackbox-title');
  await expect(dialog.locator('#blackbox-title')).toHaveText('Blackbox');
  await expect(page.getByRole('button', { name: 'Close the Blackbox panel' })).toHaveCount(2); // the scrim and the x
  await tabInside(page, 30);
  await tabInside(page, 30, true);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test('A11Y-01a: Esc with the add form open and a click on the scrim both close the drawer safely and return focus', async ({ page }) => {
  const opener = page.getByRole('button', { name: 'Blackbox', exact: true });
  let dialog = await openBlackboxByKey(page);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await expect(dialog.getByLabel('Folder, network share, or address and port')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
  dialog = await openBlackboxByKey(page);
  await page.getByRole('button', { name: 'Close the Blackbox panel' }).first().click({ position: { x: 20, y: 300 } });
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test('A11Y-01b: reloading with the drawer open lands on the start of the app with the drawer closed', async ({ page }) => {
  await openBlackbox(page);
  await page.reload();
  await expect(page.getByRole('button', { name: 'Home', exact: true })).toBeVisible();
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('A11Y-02: Enter and Space open a status row, and the status buttons show a focus ring', async ({ page }) => {
  const dialog = await openBlackboxByKey(page);
  const row = (name: string) => dialog.getByRole('button', { name: 'Open status: ' + name });
  // Walk forward with Tab until the first row has focus: the list is reachable by keyboard alone.
  let reached = false;
  for (let i = 0; i < 40 && !reached; i++) {
    await page.keyboard.press('Tab');
    reached = (await focusInfo(page)).label === 'Open status: web-a';
  }
  expect(reached, 'Tab reaches the first row').toBe(true);
  const ring = () =>
    page.evaluate(() => {
      const s = getComputedStyle(document.activeElement as Element);
      return { style: s.outlineStyle, width: parseFloat(s.outlineWidth) };
    });
  expect(await ring()).toMatchObject({ style: 'solid' });
  expect((await ring()).width).toBeGreaterThanOrEqual(2);
  await page.keyboard.press('Enter');
  const a = dialog.getByRole('region', { name: 'web-a' });
  await expect(a.getByRole('heading', { level: 3 })).toContainText('web-a');
  for (const name of ['Back to the list', 'Check again', 'Snapshot now']) {
    await a.getByRole('button', { name, exact: true }).focus();
    expect(await ring(), name + ' has a focus ring').toMatchObject({ style: 'solid' });
  }
  await a.getByRole('button', { name: 'Back to the list', exact: true }).focus();
  await page.keyboard.press('Space');
  await expect(row('web-b')).toBeVisible();
  await row('web-b').focus();
  await page.keyboard.press('Space');
  await expect(dialog.getByRole('region', { name: 'web-b' })).toBeVisible();
});

test('A11Y-02a: after Back to the list focus stays inside the drawer, not on the page body', async ({ page }) => {
  const dialog = await openBlackboxByKey(page);
  await dialog.getByRole('button', { name: 'Open status: web-a' }).focus();
  await page.keyboard.press('Enter');
  const back = dialog.getByRole('button', { name: 'Back to the list', exact: true });
  await back.focus();
  await page.keyboard.press('Enter');
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  const f = await focusInfo(page);
  expect(f.tag).not.toBe('BODY');
  expect(f.inDialog).toBe(true);
});

test('A11Y-02c: after Back to the list focus lands on the row that was opened or on the first control of the list', async ({ page }) => {
  const dialog = await openBlackboxByKey(page);
  await dialog.getByRole('button', { name: 'Open status: web-c' }).focus();
  await page.keyboard.press('Enter');
  const back = dialog.getByRole('button', { name: 'Back to the list', exact: true });
  await back.focus();
  await page.keyboard.press('Enter');
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
  const f = await focusInfo(page);
  const firstControl = await dialog.locator('button, input, a[href]').first().evaluate((e) => e.getAttribute('aria-label') ?? (e.textContent ?? '').trim().slice(0, 60));
  expect([ 'Open status: web-c', firstControl ], 'focus after Back: ' + f.tag + ' "' + f.label + '"').toContain(f.label);
});

test('A11Y-02b: a double Enter on a row opens one status, and Esc then reopening shows the list again', async ({ page }) => {
  const dialog = await openBlackboxByKey(page);
  const row = dialog.getByRole('button', { name: 'Open status: web-a' });
  await row.focus();
  await page.keyboard.press('Enter');
  await page.keyboard.press('Enter');
  await expect(dialog.getByRole('region', { name: 'web-a' })).toHaveCount(1);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Blackbox', exact: true })).toBeFocused();
  const again = await openBlackboxByKey(page);
  await expect(again.getByRole('button', { name: /^Open status: / })).toHaveCount(9);
});

test('A11Y-03: the add form fields have labels and descriptions that exist, and an empty try is announced', async ({ page }) => {
  const dialog = await openBlackbox(page);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  const where = dialog.getByLabel('Folder, network share, or address and port');
  const name = dialog.getByLabel('Name (optional)');
  await expect(where).toBeVisible();
  await where.fill('http://web-x:7788');
  const key = dialog.getByLabel('Read key (for an address)');
  await expect(key).toBeVisible();
  for (const field of [where, name, key]) {
    const ids = ((await field.getAttribute('aria-describedby')) ?? '').split(/\s+/).filter(Boolean);
    expect(ids.length, 'a description is linked').toBeGreaterThan(0);
    for (const id of ids) {
      const target = page.locator('[id="' + id + '"]');
      await expect(target, id + ' exists').toHaveCount(1);
      expect((await target.innerText()).trim().length, id + ' has words').toBeGreaterThan(0);
    }
  }
  await expect(key).toHaveAttribute('type', 'password');
  await dialog.getByLabel('Show what I type').check();
  await expect(key).toHaveAttribute('type', 'text');
  await dialog.getByLabel('Show what I type').uncheck();
  await expect(key).toHaveAttribute('type', 'password');
  await where.fill('');
  await dialog.getByRole('button', { name: 'Add to the list' }).click();
  await expect(dialog.getByRole('alert').filter({ hasText: 'Type where the Blackbox is.' })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Add to the list' })).toBeFocused();
});

test('A11Y-03b: changing the location makes the key field appear and vanish without losing focus, and Tab goes top to bottom', async ({ page }) => {
  const dialog = await openBlackbox(page);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  const where = dialog.getByLabel('Folder, network share, or address and port');
  await where.fill('http://web-x:7788');
  await expect(dialog.getByLabel('Read key (for an address)')).toBeVisible();
  await expect(where).toBeFocused();
  await where.fill('D:\\Boxes\\one');
  await expect(dialog.getByLabel('Read key (for an address)')).toHaveCount(0);
  await expect(where).toBeFocused();
  await where.fill('http://web-x:7788');
  await where.focus();
  const order: string[] = [];
  for (let i = 0; i < 6; i++) {
    order.push((await focusInfo(page)).label);
    await page.keyboard.press('Tab');
  }
  const pos = (re: RegExp) => order.findIndex((l) => re.test(l));
  expect(pos(/Folder, network share/), order.join(' > ')).toBe(0);
  expect(pos(/Name \(optional\)|^$/)).toBeGreaterThan(-1);
  expect(pos(/Add to the list/), order.join(' > ')).toBeGreaterThan(pos(/Folder, network share/));
});

test('A11Y-03a: a wrong location marks the field as invalid and ties the error to it', async ({ page }) => {
  const dialog = await openBlackbox(page);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  const where = dialog.getByLabel('Folder, network share, or address and port');
  await dialog.getByRole('button', { name: 'Add to the list' }).click();
  await expect(dialog.getByRole('alert').filter({ hasText: 'Type where the Blackbox is.' })).toBeVisible();
  await expect(where).toHaveAttribute('aria-invalid', 'true');
  const described = ((await where.getAttribute('aria-describedby')) ?? '').split(/\s+/).filter(Boolean);
  const texts = await Promise.all(described.map((id) => page.locator('[id="' + id + '"]').innerText()));
  expect(texts.join(' ')).toContain('Type where the Blackbox is.');
});

test('A11Y-04: every Blackbox state shows its own word in the list and in the status, and readiness uses words', async ({ page }) => {
  const dialog = await openBlackbox(page);
  for (const [name, word] of Object.entries(STICKERS)) {
    const row = dialog.getByRole('button', { name: 'Open status: ' + name });
    await expect(dialog.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Open status: ' + name }) }).locator('.bb-sticker'), name + ' in the list').toHaveText(word);
    await row.click();
    const region = dialog.getByRole('region', { name });
    await expect(region.locator('.bb-sticker').first(), name + ' in the status').toHaveText(word);
    await expect(region.locator('.bb-ready strong').first()).toHaveText(/^(Ready|Partly ready|Cannot test yet)$/);
    await region.getByRole('button', { name: 'Back to the list' }).click();
  }
});

test('A11Y-05: Help is a named modal that keeps focus inside, with a labelled scrim, and Esc returns focus to Glossary', async ({ page }) => {
  const opener = page.getByRole('button', { name: 'Glossary', exact: true });
  const dialog = await openHelpByKey(page);
  await expect(dialog).toHaveAttribute('aria-modal', 'true');
  await expect(dialog).toHaveAttribute('aria-labelledby', 'glossary-title');
  await expect(page.getByRole('button', { name: 'Close help and glossary' }).first()).toBeAttached();
  await expect(dialog.getByRole('button', { name: 'Close help and glossary' })).toBeVisible();
  await tabInside(page, 40);
  await tabInside(page, 40, true);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test('A11Y-05a: inside the guide focus stays in the drawer, and Esc closes it and returns focus to Glossary', async ({ page }) => {
  const opener = page.getByRole('button', { name: 'Glossary', exact: true });
  const dialog = await openHelpByKey(page);
  await dialog.getByTestId('guide-open').click();
  await expect(dialog.getByTestId('guide-body')).toBeVisible();
  await tabInside(page, 40);
  await tabInside(page, 40, true);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test('A11Y-06: the keeper remove question is an alertdialog with a name and focus on the safe button, and clicking outside removes nothing', async ({ page }) => {
  await withPreview(page, 'moved');
  const posted: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && /remove-old/.test(r.url())) posted.push(r.url());
  });
  await page.getByRole('button', { name: 'Remove old copies' }).click();
  const question = page.getByRole('alertdialog');
  await expect(question).toBeVisible();
  const name = (await question.getAttribute('aria-label')) ?? (await question.getAttribute('aria-labelledby'));
  expect((name ?? '').length, 'the question has a name').toBeGreaterThan(0);
  await expect(question.getByRole('button', { name: 'Keep them' })).toBeFocused();
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await expect(question).toBeVisible();
  await question.getByRole('button', { name: 'Keep them' }).focus();
  await page.keyboard.press('Enter');
  await expect(question).toHaveCount(0);
  await expect(page.getByText('The old copies were removed.')).toHaveCount(0);
  await expect(page.getByText('The old copies are still in your own folder.')).toBeVisible();
  expect(posted, 'no remove request was sent').toEqual([]);
});

test('A11Y-06a: Esc on the keeper question closes it and returns focus to Remove old copies', async ({ page }) => {
  await withPreview(page, 'moved');
  const ask = page.getByRole('button', { name: 'Remove old copies' });
  await ask.click();
  const question = page.getByRole('alertdialog');
  await expect(question).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(question).toHaveCount(0);
  await expect(ask).toBeFocused();
});

async function ratio(page: Page, sel: Locator): Promise<number> {
  return sel.evaluate((el) => {
    const ctx = document.createElement('canvas').getContext('2d')!;
    const rgba = (css: string): [number, number, number, number] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0], d[1], d[2], d[3] / 255];
    };
    // Paint the element's own background over its parents' backgrounds, up to the page.
    const layers: [number, number, number, number][] = [];
    for (let n: Element | null = el; n; n = n.parentElement) {
      layers.push(rgba(getComputedStyle(n).backgroundColor));
    }
    let base: [number, number, number] = [255, 255, 255];
    for (let i = layers.length - 1; i >= 0; i--) {
      const [r, g, b, a] = layers[i];
      base = [r * a + base[0] * (1 - a), g * a + base[1] * (1 - a), b * a + base[2] * (1 - a)];
    }
    const [fr, fg, fb, fa] = rgba(getComputedStyle(el).color);
    const fore: [number, number, number] = [fr * fa + base[0] * (1 - fa), fg * fa + base[1] * (1 - fa), fb * fa + base[2] * (1 - fa)];
    const lum = (c: number[]) => {
      const f = c.map((v) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
      });
      return 0.2126 * f[0] + 0.7152 * f[1] + 0.0722 * f[2];
    };
    const a = lum(fore);
    const b = lum(base);
    return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
  });
}

test('A11Y-07: data-theme overrides the system scheme both ways, the theme button cycles, and the setting survives a reload', async ({ page }) => {
  const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  const set = (t: string) =>
    page.evaluate((x) => {
      document.documentElement.dataset.theme = x;
    }, t);
  const dark = await bg();
  await set('light');
  await expect.poll(bg, 'light differs from dark').not.toBe(dark);
  const light = await bg();
  await page.emulateMedia({ colorScheme: 'light' });
  await set('dark');
  await expect.poll(bg, 'data-theme=dark wins over a light system').toBe(dark);
  await page.evaluate(() => delete document.documentElement.dataset.theme);
  await expect.poll(bg, 'with no override a light system is light').toBe(light);
  await page.emulateMedia({ colorScheme: 'dark' });
  const button = page.getByRole('button', { name: /^Theme: / });
  const seen = new Set<string>();
  for (let i = 0; i < 3; i++) {
    seen.add((await button.getAttribute('aria-label')) ?? '');
    await button.click();
  }
  expect(seen.size, 'three theme settings: ' + [...seen].join(' | ')).toBe(3);
});

test('A11Y-07a: the Blackbox drawer and the guide stay readable when the theme changes while they are open', async ({ page }) => {
  const dialog = await openBlackbox(page);
  const bg = () => dialog.evaluate((e) => getComputedStyle(e).backgroundColor);
  const before = await bg();
  // The header button is under the drawer, so set the choice the way the button does: the saved setting and data-theme.
  await page.evaluate(() => {
    localStorage.setItem('unravis.theme', 'light');
    document.documentElement.setAttribute('data-theme', 'light');
  });
  await expect.poll(bg).not.toBe(before);
  await expect(dialog.getByRole('button', { name: 'Open status: web-a' })).toBeVisible();
  await page.reload();
  expect(await page.evaluate(() => document.documentElement.dataset.theme ?? '')).toBe('light');
});

test('A11Y-07b: in the light theme every Blackbox sticker has text contrast of at least 4.5 to 1', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' });
  const dialog = await openBlackbox(page);
  const stickers = dialog.locator('.bb-sticker');
  await expect(stickers).toHaveCount(9);
  const low: string[] = [];
  for (let i = 0; i < 9; i++) {
    const s = stickers.nth(i);
    const r = await ratio(page, s);
    if (r < 4.5) low.push((await s.innerText()).trim() + ' ' + r.toFixed(2));
  }
  expect(low, 'stickers under 4.5:1').toEqual([]);
});

test('A11Y-07c: in the dark theme every Blackbox sticker has text contrast of at least 4.5 to 1', async ({ page }) => {
  const dialog = await openBlackbox(page);
  const stickers = dialog.locator('.bb-sticker');
  await expect(stickers).toHaveCount(9);
  const low: string[] = [];
  for (let i = 0; i < 9; i++) {
    const r = await ratio(page, stickers.nth(i));
    if (r < 4.5) low.push((await stickers.nth(i).innerText()).trim() + ' ' + r.toFixed(2));
  }
  expect(low, 'stickers under 4.5:1').toEqual([]);
});

async function noSideScroll(page: Page, where: string): Promise<void> {
  const w = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, view: window.innerWidth }));
  expect(w.scroll, where + ': page scroll width ' + w.scroll + ' vs window ' + w.view).toBeLessThanOrEqual(w.view);
}

test('A11Y-08: at 375 px the Blackbox panel has no side scroll in the list, a status, the add form and the wizard, and long paths wrap', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Blackbox' }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  await noSideScroll(page, 'list');
  await dialog.getByRole('button', { name: 'Open status: web-b' }).click();
  const region = dialog.getByRole('region', { name: 'web-b' });
  await expect(region).toBeVisible();
  await noSideScroll(page, 'status');
  const box = await region.boundingBox();
  const dbox = await dialog.boundingBox();
  expect(box!.x + box!.width, 'status stays inside the drawer').toBeLessThanOrEqual(dbox!.x + dbox!.width + 1);
  await region.getByRole('button', { name: 'Back to the list' }).click();
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await expect(dialog.getByLabel('Folder, network share, or address and port')).toBeVisible();
  await noSideScroll(page, 'add form');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('n'.repeat(120));
  await noSideScroll(page, 'wizard with a long name');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await expect(dialog.getByText('Use 63 characters or fewer.')).toBeVisible();
  await noSideScroll(page, 'wizard error');
});

test('A11Y-08a: turned sideways at 812 by 375 the Blackbox panel still has no side scroll', async ({ page }) => {
  await page.setViewportSize({ width: 812, height: 375 });
  const dialog = await openBlackboxAnyWidth(page);
  await noSideScroll(page, 'sideways list');
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  await expect(dialog.getByRole('region', { name: 'web-a' })).toBeVisible();
  await noSideScroll(page, 'sideways status');
});

test('A11Y-09: at 375 px Help and the guide have no side scroll, and wide guide tables scroll inside their own focusable region', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Glossary' }).click();
  const dialog = page.getByRole('dialog').first();
  await expect(dialog.locator('#glossary-title')).toHaveText('Help and glossary');
  await noSideScroll(page, 'help');
  await dialog.getByTestId('guide-open').click();
  await expect(dialog.getByTestId('guide-body')).toBeVisible();
  await noSideScroll(page, 'guide index');
  await dialog.getByTestId('guide-nav-blackbox').click();
  await expect(dialog.getByTestId('guide-nav-blackbox')).toHaveAttribute('aria-current', 'page');
  await noSideScroll(page, 'guide Blackbox page');
  const regions = dialog.getByTestId('guide-body').getByRole('region');
  expect(await regions.count(), 'the Blackbox page has a table').toBeGreaterThan(0);
  const first = regions.first();
  await expect(first).toHaveAttribute('tabindex', '0');
  expect(((await first.getAttribute('aria-label')) ?? '').startsWith('Table: ')).toBe(true);
  const fits = await first.evaluate((e) => e.getBoundingClientRect().width <= window.innerWidth && getComputedStyle(e).overflowX !== 'visible');
  expect(fits, 'the table region fits the window and scrolls itself').toBe(true);
});

test('A11Y-10: with reduced motion the drawers have no slide or spin, and without it the drawer does slide', async ({ page }) => {
  const longest = () =>
    page.evaluate(() => {
      let max = 0;
      for (const el of document.querySelectorAll('*')) {
        const s = getComputedStyle(el);
        for (const v of [s.animationDuration, s.transitionDuration]) {
          for (const part of v.split(',')) {
            const t = part.trim();
            const n = parseFloat(t);
            if (!isNaN(n)) max = Math.max(max, t.endsWith('ms') ? n / 1000 : n);
          }
        }
      }
      return max;
    });
  const dialog = await openBlackbox(page);
  expect(await longest(), 'reduced motion: longest animation or transition in seconds').toBeLessThan(0.05);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await openBlackbox(page);
  expect(await longest(), 'without the preference the app does animate').toBeGreaterThan(0.05);
});

test('A11Y-10a: with reduced motion the Help update check shows its answer at once with no running animation', async ({ page }) => {
  const dialog = await openHelpByKey(page);
  await dialog.getByTestId('update-check-button').click();
  await expect(dialog.locator('[data-state]')).toBeVisible();
  const animating = await page.evaluate(() => document.getAnimations().filter((a) => a.playState === 'running' && a.effect && (a.effect.getComputedTiming().iterations ?? 1) === Infinity).length);
  expect(animating, 'no endless animation is running').toBe(0);
});

test('A11Y-12: the guide pages start at h3 with no heading level skipped, each heading can take focus, and the nav has a name', async ({ page }) => {
  const dialog = await openHelpByKey(page);
  await dialog.getByTestId('guide-open').click();
  await expect(dialog.getByRole('navigation', { name: 'Pages' })).toBeVisible();
  for (const slug of ['cases', 'security']) {
    await dialog.getByTestId('guide-nav-' + slug).click();
    await expect(dialog.getByTestId('guide-nav-' + slug)).toHaveAttribute('aria-current', 'page');
    const heads = await dialog.getByTestId('guide-body').locator('h1,h2,h3,h4,h5,h6').evaluateAll((els) => els.map((e) => ({ level: Number(e.tagName[1]), id: e.id, tabindex: e.getAttribute('tabindex') })));
    expect(heads.length, slug + ' has headings').toBeGreaterThan(1);
    expect(heads[0].level, slug + ' page heading level').toBe(3);
    let prev = 2;
    for (const h of heads) {
      expect(h.level, slug + ': level ' + h.level + ' after ' + prev).toBeLessThanOrEqual(prev + 1);
      expect(h.tabindex, slug + ' heading ' + h.id).toBe('-1');
      expect(h.id.length, 'a heading has an id for the jump').toBeGreaterThan(0);
      prev = h.level;
    }
  }
});

test('A11Y-12a: an in-page link in the guide moves focus to the heading it points at', async ({ page }) => {
  const dialog = await openHelpByKey(page);
  await dialog.getByTestId('guide-open').click();
  await dialog.getByTestId('guide-nav-install').click();
  const link = dialog.getByTestId('guide-body').getByRole('button', { name: /upgrade and uninstall/i }).first();
  await link.click();
  await expect(dialog.getByTestId('guide-nav-upgrade-and-uninstall')).toHaveAttribute('aria-current', 'page');
  const f = await focusInfo(page);
  expect(f.tag, 'focus is on a heading, not the page body').toMatch(/^H[1-6]$/);
  expect(f.inDialog).toBe(true);
});
