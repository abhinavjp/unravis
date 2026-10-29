// Case keys in the browser: Lock this case, archiving a keyed case, erasing a locked one.
// Every test makes its own keyed case, so the demo cases are left alone.
import { test, expect, type Page } from '../support/fixtures';
import { createCase, fwd, type ApiFn } from '../support/api';
import { goHome, openCase } from '../support/ui';
import { mkdirSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const KEY = 'qa case key for the browser';

async function keyedCase(api: ApiFn, title: string): Promise<{ id: string; code: string; ending: string }> {
  const id = await createCase(api, title);
  const set = await api('POST', `/api/cases/${id}/key/set`, { passphrase: KEY, confirm: KEY });
  expect(set.status).toBe(200);
  const key = (await api('GET', `/api/cases/${id}/key`)).json;
  return { id, code: id.slice(0, 8), ending: key.ending };
}

async function lockViaPage(page: Page, title: string) {
  await goHome(page);
  await openCase(page, title);
  await page.getByRole('button', { name: 'Change or remove the case key' }).click();
  await page.getByRole('button', { name: 'Lock this case' }).click();
  await expect(page.getByRole('heading', { name: 'This case has its own key' })).toBeVisible();
}

test('KEY-06: Lock this case sends the page to the unlock page, the API answers 423 and the right key brings the case back', async ({ page, api, allow }) => {
  allow(/423/, 'a locked case answers 423 on purpose');
  const k = await keyedCase(api, 'QA Lock Me');
  await lockViaPage(page, 'QA Lock Me');
  await expect(page.getByText('Key ending ' + k.ending)).toBeVisible();

  const got = await api('GET', `/api/cases/${k.id}`);
  expect(got.status).toBe(423);
  expect(JSON.stringify(got.json)).toContain('case-locked');
  expect((await api('GET', `/api/cases/${k.id}/ledger`)).status).toBe(423);
  const key = await api('GET', `/api/cases/${k.id}/key`);
  expect(key.status).toBe(200);
  expect(key.json.locked).toBe(true);
  expect(Object.keys(key.json).sort()).toEqual(['ending', 'locked', 'ownKey']);
  expect(JSON.stringify(key.json)).not.toContain(KEY);
  // Other cases are unaffected.
  const other = await createCase(api, 'QA Not Locked');
  expect((await api('GET', `/api/cases/${other}`)).status).toBe(200);

  // The list hides the title of the locked case.
  const list = ((await api('GET', '/api/cases')).json.cases as any[]).find((c) => c.id === k.id);
  expect(list.title).toBe('(locked)');

  // The right key opens the same case.
  await page.getByLabel('Case key').fill(KEY);
  await page.getByRole('button', { name: 'Open case' }).click();
  await expect(page.getByRole('heading', { level: 2, name: 'QA Lock Me' })).toBeVisible();
  await expect(page.getByText('Case started').first()).toBeVisible();
  expect((await api('GET', `/api/cases/${k.id}`)).status).toBe(200);
});

test('KEY-06a: a locked case shows its code and key ending on the start screen, never its title', async ({ page, api, allow }) => {
  allow(/423/, 'a locked case answers 423 on purpose');
  const k = await keyedCase(api, 'QA Hidden Title');
  expect((await api('POST', `/api/cases/${k.id}/key/lock`)).status).toBe(200);
  await goHome(page);
  await expect(page.getByText('QA Hidden Title')).toHaveCount(0);
  const row = page.getByRole('button', { name: new RegExp('^Open case Case ' + k.code) });
  await expect(row).toBeVisible();
  await expect(page.getByText('Key ending ' + k.ending).first()).toBeVisible();
  await row.click();
  await expect(page.getByRole('heading', { name: 'This case has its own key' })).toBeVisible();
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.locator('#start-title')).toBeVisible();
});

test('KEY-06b: a wrong key on the unlock page says nothing was opened and keeps the page', async ({ page, api, allow }) => {
  allow(/423|401/, 'a wrong case key is answered 401 and a locked case 423, both on purpose');
  const k = await keyedCase(api, 'QA Wrong Key Page');
  await lockViaPage(page, 'QA Wrong Key Page');
  await page.getByLabel('Case key').fill('a different wrong key');
  await page.getByRole('button', { name: 'Open case' }).click();
  await expect(page.getByText('That key did not open this case. Nothing was opened or changed.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'This case has its own key' })).toBeVisible();
  expect((await api('POST', `/api/cases/${k.id}/key/unlock`, { passphrase: 'another wrong key' })).status).toBe(401);
  expect((await api('GET', `/api/cases/${k.id}`)).status).toBe(423);
  await page.getByLabel('Case key').fill('');
  await page.getByRole('button', { name: 'Open case' }).click();
  await expect(page.getByText('Type the case key first.')).toBeVisible();
});

test('KEY-07: a keyed case is archived sealed with its own key, with no passphrase field and the same key ending', async ({ page, api, demo }) => {
  const k = await keyedCase(api, 'QA Archive Keyed');
  const folder = join(demo.scratch, 'arch-keyed');
  mkdirSync(folder);
  await goHome(page);
  await openCase(page, 'QA Archive Keyed');
  await page.getByRole('button', { name: 'Archive this case' }).click();
  const drawer = page.getByRole('dialog');
  await expect(drawer.getByText('The case key of this case')).toBeVisible();
  await expect(drawer.getByText('The file opens with the case key, on this or any computer. Keep the key: without it the file cannot be opened.')).toBeVisible();
  await expect(drawer.getByLabel('Passphrase for this file')).toHaveCount(0);
  await drawer.getByLabel('Folder for the case file').fill(folder);
  await drawer.getByRole('button', { name: 'Archive the case' }).click();
  await expect(page.getByText('Key ending ' + k.ending).first()).toBeVisible();
  await expect.poll(() => readdirSync(folder).length).toBe(1);
  expect(readdirSync(folder)[0]).toMatch(/\.unravis-case$/);
  const c = (await api('GET', `/api/cases/${k.id}`)).json;
  expect(c.archive.current.keyKind).not.toBe('typed');
  expect(JSON.stringify(c)).not.toContain(KEY);
});

test('KEY-07a: a locked case cannot be archived and a typed passphrase is refused for a keyed case', async ({ api, demo }) => {
  const k = await keyedCase(api, 'QA Archive Locked');
  const folder = join(demo.scratch, 'arch-locked');
  mkdirSync(folder);
  expect((await api('POST', `/api/cases/${k.id}/key/lock`)).status).toBe(200);
  const locked = await api('POST', `/api/cases/${k.id}/archive`, { path: fwd(folder), passphrase: 'qa passphrase one' });
  expect(locked.status).toBe(423);
  expect(JSON.stringify(locked.json)).toContain('case-locked');
  expect(readdirSync(folder)).toEqual([]);
  expect((await api('POST', `/api/cases/${k.id}/key/unlock`, { passphrase: KEY })).status).toBe(200);
  const typed = await api('POST', `/api/cases/${k.id}/archive`, { path: fwd(folder), passphrase: 'qa passphrase one' }, 'open');
  expect(typed.status).toBe(400);
  expect(JSON.stringify(typed.json)).toContain('bad-passphrase');
  expect(readdirSync(folder)).toEqual([]);
  const ok = await api('POST', `/api/cases/${k.id}/archive`, { path: fwd(folder) }, 'open');
  expect(ok.status).toBe(202);
  const done = await (async () => {
    for (let i = 0; i < 40; i++) {
      const c = (await api('GET', `/api/cases/${k.id}`)).json;
      if (c.archive?.current) return c;
      await new Promise((r) => setTimeout(r, 250));
    }
    throw new Error('the archive did not finish');
  })();
  expect(done.archive.current.keyKind).not.toBe('typed');
});

test('KEY-08: a locked keyed case can be erased by typing its code, without the case key', async ({ page, api, allow }) => {
  allow(/423/, 'a locked case answers 423 on purpose');
  const k = await keyedCase(api, 'QA Erase Locked');
  expect((await api('POST', `/api/cases/${k.id}/key/lock`)).status).toBe(200);
  await goHome(page);
  await page.getByRole('button', { name: new RegExp('^Open case Case ' + k.code) }).click();
  await expect(page.getByText('Case keys cannot be recovered. If it is lost, only erasing the case frees the space.')).toBeVisible();
  await page.getByRole('button', { name: 'Erase this case' }).click();
  await expect(page.getByText('This removes the working copy, notes, suspects and checklist from this computer.')).toBeVisible();
  await expect(page.getByText('Type the case code ' + k.code + ' to erase')).toBeVisible();
  const confirm = page.getByRole('button', { name: 'Erase this case' }).last();
  await expect(confirm).toBeDisabled();
  await page.getByLabel(/Type the case code/).fill('wrong');
  await expect(confirm).toBeDisabled();
  await page.getByLabel(/Type the case code/).fill(k.code);
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(page.getByText(k.code + ' is removed from this computer. Any case file you saved was not touched.')).toBeVisible();
  await page.getByRole('button', { name: 'Back to the list' }).click();
  const line = page.getByText(new RegExp('^Erased case ' + k.code + ' · erased .* UTC\\. Only this line is left\\.'));
  await expect(line).toBeVisible();
  // The line holds an id and a time and nothing else: no key ending, no key.
  expect(await line.innerText()).not.toContain(k.ending + ' ');
  expect((await api('GET', `/api/cases/${k.id}`)).status).toBe(404);
  expect((await api('GET', `/api/cases/${k.id}/key`)).status).toBe(404);
});

test('KEY-08a: erasing the same case twice over the API is harmless and the wrong code is refused', async ({ api }) => {
  const k = await keyedCase(api, 'QA Erase Twice');
  expect((await api('POST', `/api/cases/${k.id}/key/lock`)).status).toBe(200);
  const wrong = await api('DELETE', `/api/cases/${k.id}`, { confirm: 'WRONG123' });
  expect(wrong.status).toBe(400);
  expect(JSON.stringify(wrong.json)).toContain('confirm-mismatch');
  const first = await api('DELETE', `/api/cases/${k.id}`, { confirm: k.code });
  expect(first.status).toBe(204);
  const second = await api('DELETE', `/api/cases/${k.id}`, { confirm: k.code });
  expect(second.status).toBe(404);
});

test('KEY-10: the key route returns only ownKey, locked and ending, and never the typed key', async ({ api }) => {
  const k = await keyedCase(api, 'QA Key Shape');
  for (const step of ['unlocked', 'locked', 'unlocked again']) {
    if (step === 'locked') expect((await api('POST', `/api/cases/${k.id}/key/lock`)).status).toBe(200);
    if (step === 'unlocked again') {
      const u = await api('POST', `/api/cases/${k.id}/key/unlock`, { passphrase: KEY });
      expect(u.status).toBe(200);
      expect(JSON.stringify(u.json)).not.toContain(KEY);
    }
    const r = await api('GET', `/api/cases/${k.id}/key`);
    expect(r.status, step).toBe(200);
    expect(Object.keys(r.json).sort(), step).toEqual(['ending', 'locked', 'ownKey']);
    expect(r.json.ending, step).toMatch(/^[0-9a-f]{4}$/);
    expect(JSON.stringify(r.json), step).not.toContain(KEY);
  }
});
