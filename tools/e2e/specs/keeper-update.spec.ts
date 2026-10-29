// Key keeper edge cases and the update check hooks: what the demo previews ignore, the keeper routes' plain refusals,
// the note about earlier cases, and proof that nothing calls out until the update button is pressed.
// Nothing here changes demo data.
import { test, expect, type Page } from '../support/fixtures';
import { goHome } from '../support/ui';

const CASE = 'Checkout slowdown, 17 October';

async function storedPreview(page: Page, key: string): Promise<string | null> {
  return page.evaluate((k) => sessionStorage.getItem(k), key);
}

async function openHelp(page: Page) {
  await page.getByRole('button', { name: 'Glossary', exact: true }).click();
  const dialog = page.getByRole('dialog').first();
  await expect(dialog).toBeVisible();
  return dialog;
}

async function pressCheck(page: Page) {
  const dialog = await openHelp(page);
  await dialog.getByTestId('update-check-button').click();
  const result = dialog.locator('[data-state]');
  await expect(result).toBeVisible();
  return result;
}

// ---- KEEP ----

test('KEEP-03: an impostor preview does not exist in the demo, so the name is ignored and no keeper card shows', async ({ page }) => {
  await page.evaluate(() => {
    location.hash = '#keeper=impostor';
  });
  await goHome(page);
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  await expect(page.locator('[data-keeper]')).toHaveCount(0);
  await expect(page.locator('#conn-head')).toHaveText('Saved connections');
  expect(await storedPreview(page, 'unravis.keeperPreview')).toBeNull();
});

test('KEEP-03a: capitals in the keeper hash are ignored, and the status route ignores the impostor name too', async ({ page, api }) => {
  await page.evaluate(() => {
    location.hash = '#KEEPER=Stopped';
  });
  await goHome(page);
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  await expect(page.locator('[data-keeper]')).toHaveCount(0);
  expect(await storedPreview(page, 'unravis.keeperPreview')).toBeNull();
  const status = await api('GET', '/api/keeper/status?preview=impostor');
  expect(status.status).toBe(200);
  expect(status.json).toMatchObject({ mode: 'user', running: false });
});

test('KEEP-07: the removal route refuses a missing, empty, unconfirmed or odd request in plain words, and GET', async ({ api }) => {
  const get = await api('GET', '/api/keeper/remove-old');
  expect(get.status).toBe(405);
  const none = await api('POST', '/api/keeper/remove-old');
  expect(none.status).toBe(415);
  expect(none.json.error).toBe('Content-Type must be application/json.');
  const empty = await api('POST', '/api/keeper/remove-old', {});
  expect(empty.status).toBe(400);
  expect(empty.json.error).toBe('Confirm that the old copies should be removed.');
  const extra = await api('POST', '/api/keeper/remove-old', { confirmDuplicates: true, extra: 1 });
  expect(extra.status).toBe(400);
  expect(extra.json.error).toBe('Request body has a field this server does not know.');
  const noToken = await api('POST', '/api/keeper/remove-old', { confirm: true }, undefined, { token: null });
  expect(noToken.status).toBe(401);
  for (const r of [get, none, empty, extra, noToken]) expect(r.text).not.toMatch(/panic|goroutine|\.go:\d+/);
});

test('KEEP-08: each keeper preview answers with plain fields only, and never a password or key', async ({ api }) => {
  const expected: Record<string, object> = {
    '': { mode: 'user', running: false, pending: { sql: 0, mongo: 0, blackbox: 0 }, oldFilesPresent: false },
    saved: { mode: 'shared', running: true, pending: { sql: 0, mongo: 0, blackbox: 0 }, oldFilesPresent: false },
    stopped: { mode: 'shared', running: false, problem: 'stopped', pending: { sql: 0, mongo: 0, blackbox: 0 }, oldFilesPresent: false },
    migrate: { mode: 'shared', running: true, pending: { sql: 2, mongo: 1, blackbox: 1 }, oldFilesPresent: true },
    moved: { mode: 'shared', running: true, pending: { sql: 0, mongo: 0, blackbox: 0 }, oldFilesPresent: true }
  };
  for (const [preview, body] of Object.entries(expected)) {
    const r = await api('GET', '/api/keeper/status' + (preview ? '?preview=' + preview : ''));
    expect(r.status, preview).toBe(200);
    expect(r.json, preview).toEqual(body);
    expect(r.text, preview).not.toMatch(/password|secret|ubk-|token/i);
  }
});

test('KEEP-08a: the status route refuses POST and a missing or wrong session, and a hostile query gives safe JSON', async ({ api }) => {
  const post = await api('POST', '/api/keeper/status', {});
  expect(post.status).toBe(405);
  for (const token of [null, 'wrong-token']) {
    const r = await api('GET', '/api/keeper/status', undefined, undefined, { token });
    expect(r.status).toBe(401);
    expect(r.json.error).toBe('Invalid or missing session token.');
  }
  const odd = await api('GET', '/api/keeper/status?x=<script>alert(1)</script>');
  expect(odd.status).toBe(200);
  expect(odd.headers.get('content-type')).toContain('application/json');
  expect(odd.text).not.toContain('<script');
});

test('KEEP-08b: the stopped card never shows the raw error code', async ({ page }) => {
  await page.evaluate(() => {
    location.hash = '#keeper=stopped';
  });
  await goHome(page);
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  const card = page.locator('[data-keeper=stopped]');
  await expect(card).toBeVisible();
  const text = await page.locator('body').innerText();
  for (const code of ['keeper-stopped', 'keeper-impostor', 'keeper-source-gone', 'not-verified', 'duplicates-unconfirmed']) {
    expect(text, code).not.toContain(code);
  }
});

test('KEEP-15: a count of earlier cases shows as a plain note with no way to open or move them, and zero shows nothing', async ({ page }) => {
  let old = 3;
  await page.route(/\/api\/cases(\?.*)?$/, async (route) => {
    if (route.request().method() !== 'GET') return route.continue();
    const res = await route.fetch();
    const body = await res.json();
    body.oldCases = old;
    await route.fulfill({ response: res, json: body });
  });
  await goHome(page);
  await expect(page.getByRole('button', { name: /^Open case / }).first()).toBeVisible();
  const rows = await page.getByRole('button', { name: /^Open case / }).count();
  expect(rows).toBeGreaterThan(0);
  const note = page.locator('[data-old-cases]');
  await expect(note).toHaveText('3 cases from before this PC was set up for everyone are still in your own Windows folder, sealed for you alone. Unravis here does not show or move them, and it has not deleted them. Ask the person who looks after this PC before you remove that folder.');
  await expect(note).toHaveAttribute('role', 'note');
  await expect(note.getByRole('button')).toHaveCount(0);
  await expect(note.getByRole('link')).toHaveCount(0);
  await expect(page.getByRole('button', { name: /^Open case / })).toHaveCount(rows);

  old = 1;
  await page.reload();
  await goHome(page);
  await expect(page.locator('[data-old-cases]')).toContainText('1 case from before this PC was set up for everyone is still in your own Windows folder');
  old = 0;
  await page.reload();
  await goHome(page);
  await expect(page.getByRole('button', { name: /^Open case / }).first()).toBeVisible();
  await expect(page.locator('[data-old-cases]')).toHaveCount(0);
});

test('KEEP-15a: a case an older version made is marked as legacy, is kept apart from the rest, and a reload keeps it so', async ({ page, allow }) => {
  allow(/409/, 'opening a legacy case is refused on purpose');
  await goHome(page);
  const row = page.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Open case Case demountr' }) });
  await expect(row).toContainText('Legacy case from an older version');
  await expect(page.getByRole('listitem').filter({ hasText: 'Legacy case from an older version' })).toHaveCount(1);
  await page.reload();
  await goHome(page);
  await expect(row).toContainText('Legacy case from an older version');
  await expect(page.getByRole('button', { name: 'Open case ' + CASE })).toHaveCount(1);
  await page.getByRole('button', { name: 'Open case Case demountr' }).click();
  await expect(page.getByText('This case cannot be opened.')).toBeVisible();
  await expect(page.getByText('It was made by an older version that did not seal cases, so Unravis cannot tell whether it was changed.')).toBeVisible();
});

// ---- UPD ----

test('UPD-05: a bad update value is ignored on a fresh tab and clears one that was set', async ({ page }) => {
  await page.evaluate(() => {
    location.hash = '#update=nonsense';
  });
  const first = await pressCheck(page);
  await expect(first).toHaveAttribute('data-state', 'dev');
  expect(await storedPreview(page, 'unravis.updatePreview')).toBeNull();
  await page.keyboard.press('Escape');

  await page.evaluate(() => {
    location.hash = '#update=latest';
  });
  const latest = await pressCheck(page);
  await expect(latest).toHaveAttribute('data-state', 'latest');
  expect(await storedPreview(page, 'unravis.updatePreview')).toBe('latest');
  await page.keyboard.press('Escape');

  await page.evaluate(() => {
    location.hash = '#update=nonsense';
  });
  await page.reload();
  const cleared = await pressCheck(page);
  await expect(cleared).toHaveAttribute('data-state', 'dev');
  expect(await storedPreview(page, 'unravis.updatePreview')).toBeNull();
});

test('UPD-05a: capitals in the update value are ignored, and a second tab has its own state', async ({ page, demo }) => {
  await page.evaluate(() => {
    location.hash = '#update=LATEST';
  });
  const result = await pressCheck(page);
  await expect(result).toHaveAttribute('data-state', 'dev');
  expect(await storedPreview(page, 'unravis.updatePreview')).toBeNull();
  await page.keyboard.press('Escape');

  await page.evaluate(() => {
    location.hash = '#update=available';
  });
  await expect(await pressCheck(page)).toHaveAttribute('data-state', 'available');
  const second = await page.context().newPage();
  await second.goto(demo.base + '/#t=' + demo.token);
  await expect(second.getByRole('button', { name: 'Glossary', exact: true })).toBeVisible();
  expect(await storedPreview(second, 'unravis.updatePreview')).toBeNull();
  await expect(await pressCheck(second)).toHaveAttribute('data-state', 'dev');
  await second.close();
});

test('UPD-05b: both hooks work together in one address, in either order', async ({ page }) => {
  await page.evaluate(() => {
    location.hash = '#update=latest&keeper=moved';
  });
  await expect(await pressCheck(page)).toHaveAttribute('data-state', 'latest');
  await page.keyboard.press('Escape');
  expect(await storedPreview(page, 'unravis.keeperPreview')).toBe('moved');
  await goHome(page);
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('[data-keeper=moved]')).toBeVisible();

  await page.evaluate(() => {
    location.hash = '#keeper=moved&update=available';
  });
  await expect(await pressCheck(page)).toHaveAttribute('data-state', 'available');
});

test('UPD-07: moving around the app and opening Help ten times makes no update call and no call to any other host', async ({ page, demo }) => {
  const seen: string[] = [];
  page.on('request', (r) => seen.push(r.url()));
  const host = new URL(demo.base).host;
  await goHome(page);
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
  await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
  for (const tab of ['Checklist', 'Export', 'Steps']) await page.getByRole('tab', { name: tab, exact: true }).click();
  for (let i = 0; i < 10; i++) {
    await openHelp(page);
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0);
  }
  expect(seen.length).toBeGreaterThan(5);
  expect(seen.filter((u) => u.includes('update-check'))).toEqual([]);
  expect(seen.filter((u) => new URL(u).host !== host)).toEqual([]);
});

test('UPD-07a: with an update preview in the address there is still no call until the button is pressed', async ({ page }) => {
  const seen: string[] = [];
  page.on('request', (r) => seen.push(r.url()));
  await page.evaluate(() => {
    location.hash = '#update=available';
  });
  await openHelp(page);
  await expect(page.getByTestId('update-check-button')).toBeVisible();
  expect(seen.filter((u) => u.includes('update-check'))).toEqual([]);
  const wait = page.waitForRequest((r) => r.url().includes('update-check'));
  await page.getByTestId('update-check-button').click();
  const request = await wait;
  expect(request.method()).toBe('POST');
  await expect(page.locator('[data-state=available]')).toBeVisible();
  expect(seen.filter((u) => u.includes('update-check'))).toHaveLength(1);
});
