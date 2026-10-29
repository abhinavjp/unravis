// Security promises that can be checked from the demo: no saved key or password in the page,
// none in the demo log or in the case files, the bind address notes, read-only verbs, banned words.
// Fake keys only. The demo's own fake keys are ubk-demo-<x>-NOT-A-REAL-KEY.
import { test, expect, type Page, type Locator } from '../support/fixtures';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const CASE = 'Checkout slowdown, 17 October';
const NINE = ['web-a', 'web-b', 'web-old', 'web-c', 'web-d', 'web-e', 'web-f', 'web-g', 'old-box'];
const PLACE = 'Folder, network share, or address and port';

async function openDrawer(page: Page) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await expect(dialog.getByRole('button', { name: /^Open status: / }).first()).toBeVisible();
  return dialog;
}

async function openStatus(page: Page, name: string) {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
  const region = dialog.getByRole('region', { name });
  await expect(region).toBeVisible();
  return { dialog, region };
}

async function preview(page: Page, name: string) {
  await page.evaluate((x) => {
    location.hash = '#keeper=' + x;
  }, name);
}

/** Everything a key could hide in: the markup, the visible text, field values, the accessibility tree and browser storage. */
async function surfaces(page: Page): Promise<Record<string, string>> {
  const dom = await page.evaluate(() => {
    const values = Array.from(document.querySelectorAll('input,textarea,select')).map((e) => (e as HTMLInputElement).value);
    const store = (s: Storage) => Object.keys(s).map((k) => k + '=' + s.getItem(k));
    return {
      html: document.documentElement.outerHTML,
      text: document.body.innerText,
      values: values.join('\n'),
      storage: store(localStorage).concat(store(sessionStorage)).filter((x) => !x.startsWith('unravis.session=')).join('\n'),
      title: document.title
    };
  });
  return { ...dom, aria: await page.locator('body').ariaSnapshot() };
}

const SECRETS = [/ubk-/i, /NOT-A-REAL-KEY/i, /secret/i];

async function expectClean(page: Page, where: string) {
  const s = await surfaces(page);
  for (const [kind, text] of Object.entries(s)) {
    for (const re of SECRETS) expect(re.test(text), where + ': ' + kind + ' holds ' + re).toBe(false);
  }
}

test('SEC-01: no saved key or secret text on the start screen, the sources, the case home, the list, any status or the forms', async ({ page }) => {
  await preview(page, 'saved');
  await expectClean(page, 'start screen');

  await page.getByRole('button', { name: /^Audit sources:/ }).click();
  await expect(page.getByRole('dialog', { name: /Audit sources/ })).toBeVisible();
  await expectClean(page, 'sources');
  await page.keyboard.press('Escape');

  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  await expectClean(page, 'case home with the keeper');
  for (const p of ['stopped', 'migrate', 'moved']) {
    await preview(page, p);
    await page.getByRole('button', { name: /^All cases/ }).click();
    await page.getByRole('button', { name: 'Open case ' + CASE }).click();
    await expect(page.locator('#home-sources')).toBeVisible();
    await expectClean(page, 'case home, keeper ' + p);
  }

  const dialog = await openDrawer(page);
  await expectClean(page, 'Blackbox list');
  for (const name of NINE) {
    await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
    await expect(dialog.getByRole('region', { name })).toBeVisible();
    await expectClean(page, 'status of ' + name);
    await dialog.getByRole('button', { name: 'Back to the list' }).click();
  }
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-sec');
  await expectClean(page, 'wizard, name');
  for (const next of ['Next: Where', 'Next: Settings', 'Next: Review']) {
    await dialog.getByRole('button', { name: next }).click();
    await expectClean(page, 'wizard ' + next);
  }
  await dialog.getByRole('button', { name: 'Leave set-up' }).click();
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await dialog.getByLabel(PLACE).fill('http://web-c:7788');
  await expectClean(page, 'add form');
});

test('SEC-01a: the saved key is a mask until Show, appears only in the Read key block, and is gone after Hide, Back and closing', async ({ page }) => {
  const { dialog, region } = await openStatus(page, 'web-a');
  const block = region.getByRole('region', { name: 'Read key' });
  await expectClean(page, 'before Show');
  await block.getByRole('button', { name: 'Show', exact: true }).click();
  await expect(block.getByText('ubk-demo-a-NOT-A-REAL-KEY')).toBeVisible();
  // while shown, it is nowhere else on the page
  const outside = await region.evaluate((el) => {
    const copy = document.body.cloneNode(true) as HTMLElement;
    copy.querySelectorAll('[aria-label="Read key"]').forEach((n) => n.remove());
    return copy.innerHTML + copy.innerText;
  });
  expect(outside.includes('NOT-A-REAL-KEY'), 'the key shows only inside the Read key block').toBe(false);
  await block.getByRole('button', { name: 'Hide', exact: true }).click();
  await expectClean(page, 'after Hide');
  await block.getByRole('button', { name: 'Show', exact: true }).click();
  await expect(block.getByText('ubk-demo-a-NOT-A-REAL-KEY')).toBeVisible();
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await expectClean(page, 'after Back to the list');
  await dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  await expectClean(page, 'after opening web-a again');
  await page.keyboard.press('Escape');
  await expectClean(page, 'after closing the drawer');
});

test('SEC-01b: a key being typed lives only in its field, and is gone after Cancel, Back and closing the drawer', async ({ page, allow }) => {
  allow(/422/, 'the demo refuses a key it does not know');
  const typed = 'ubk-test-TYPED-ONLY-IN-FIELD';
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await dialog.getByLabel(PLACE).fill('http://web-sec.invalid:7788');
  const key = dialog.getByLabel('Read key (for an address)');
  await expect(key).toHaveAttribute('type', 'password');
  await key.fill(typed);
  const live = await surfaces(page);
  expect(live.html.includes(typed), 'the markup never holds a typed key').toBe(false);
  expect(live.text.includes(typed), 'the visible text never holds a typed key').toBe(false);
  expect(live.storage.includes(typed), 'storage never holds a typed key').toBe(false);
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  const after = await surfaces(page);
  for (const [kind, text] of Object.entries(after)) expect(text.includes(typed), 'after Cancel: ' + kind).toBe(false);
  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  const region = dialog.getByRole('region', { name: 'web-c' });
  await region.getByRole('button', { name: /Type a new read key/ }).click();
  await region.getByLabel('New read key').fill(typed);
  await expect(region.getByLabel('New read key')).toHaveAttribute('type', 'password');
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await page.keyboard.press('Escape');
  const closed = await surfaces(page);
  for (const [kind, text] of Object.entries(closed)) expect(text.includes(typed), 'after closing: ' + kind).toBe(false);
});

test('SEC-03: a typed read key, a refused one and one for an unreachable address never reach the demo log', async ({ page, demo, allow }) => {
  allow(/409|422/, 'a duplicate and a refusal are deliberate');
  const key = 'ubk-test-LOGSEARCH';
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await dialog.getByLabel(PLACE).fill('http://web-c:7788');
  await dialog.getByLabel('Read key (for an address)').fill(key);
  await dialog.getByRole('button', { name: 'Add to the list' }).click();
  await expect(dialog.getByText('That Blackbox is already in the list')).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel' }).click();

  await dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  const region = dialog.getByRole('region', { name: 'web-c' });
  await region.getByRole('button', { name: /Type a new read key/ }).click();
  await region.getByLabel('New read key').fill(key);
  await region.getByRole('button', { name: 'Save the new key' }).click();
  await expect(region.getByRole('alert')).toBeVisible();
  await dialog.getByRole('button', { name: 'Back to the list' }).click();

  await dialog.getByRole('button', { name: 'Add one by folder, share or address' }).click();
  await dialog.getByLabel(PLACE).fill('http://web-new.invalid:7788');
  await dialog.getByLabel('Read key (for an address)').fill(key);
  await dialog.getByRole('button', { name: /^(Check|Look) again$/ }).click();
  await expect(dialog.getByText('Nothing could be read there')).toBeVisible();

  const log = demo.log();
  expect(log.length, 'the demo prints at least its link line').toBeGreaterThan(0);
  const forms = [key, 'LOGSEARCH', 'ubk-test', encodeURIComponent(key), Buffer.from(key).toString('base64'), Buffer.from(key).toString('base64').replace(/=+$/, ''), Buffer.from(key).toString('hex'), 'NOT-A-REAL-KEY'];
  for (const f of forms) expect(log.includes(f), 'the log holds "' + f + '"').toBe(false);
});

/** The ways a key can sit in a file: plain, two bytes per letter, base64 (any alignment) and hex. */
function keyForms(key: string): { label: string; bytes: Buffer }[] {
  const forms = [
    { label: 'plain', bytes: Buffer.from(key, 'utf8') },
    { label: 'utf16', bytes: Buffer.from(key, 'utf16le') },
    { label: 'hex', bytes: Buffer.from(Buffer.from(key).toString('hex')) },
    { label: 'prefix', bytes: Buffer.from('ubk-') },
    { label: 'fragment', bytes: Buffer.from('NOT-A-REAL-KEY') }
  ];
  for (const k of [0, 1, 2]) {
    const enc = Buffer.from('xxx'.slice(0, k) + key).toString('base64');
    forms.push({ label: 'base64+' + k, bytes: Buffer.from(enc.slice([0, 2, 3][k], enc.length - 4)) });
  }
  return forms;
}

function allFiles(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...allFiles(p));
    else out.push(p);
  }
  return out;
}

test('SEC-04: using a Blackbox in a case and saving a typed key leave no key in any file of the config folder', async ({ page, demo }) => {
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  const a = await openStatus(page, 'web-a');
  await a.region.getByRole('region', { name: 'Read key' }).getByRole('button', { name: 'Show', exact: true }).click();
  await expect(a.region.getByText('ubk-demo-a-NOT-A-REAL-KEY')).toBeVisible();
  await a.region.getByRole('button', { name: 'Use in this case' }).click();
  await expect(a.region.getByText('This case uses this Blackbox for Step 9.')).toBeVisible();
  await a.region.getByRole('button', { name: 'Stop using it in this case' }).click();
  await expect(a.region.getByRole('button', { name: 'Use in this case' })).toBeEnabled();
  await a.region.getByRole('button', { name: 'Use in this case' }).click();
  await expect(a.region.getByText('This case uses this Blackbox for Step 9.')).toBeVisible();
  await a.dialog.getByRole('button', { name: 'Back to the list' }).click();

  const typed = 'ubk-demo-c-NOT-A-REAL-KEY';
  await a.dialog.getByRole('button', { name: 'Open status: web-c' }).click();
  const c = a.dialog.getByRole('region', { name: 'web-c' });
  await c.getByRole('button', { name: /Type a new read key/ }).click();
  await c.getByLabel('New read key').fill(typed);
  await c.getByRole('button', { name: 'Save the new key' }).click();
  await expect(c.getByText('The new key was tried and saved.')).toBeVisible();

  const files = allFiles(demo.configDir);
  expect(files.length, 'the config folder holds the case files').toBeGreaterThan(0);
  for (const key of ['ubk-demo-a-NOT-A-REAL-KEY', typed]) {
    for (const f of files) {
      const data = readFileSync(f);
      for (const form of keyForms(key)) expect(data.includes(form.bytes), f + ' holds the key as ' + form.label).toBe(false);
    }
  }
});

test('SEC-05: a read route refuses a write verb with 405, and the Blackbox routes toward another server only ask it to report', async ({ api }) => {
  const reads: [string, string][] = [
    ['PUT', '/api/sources'],
    ['DELETE', '/api/sources'],
    ['POST', '/api/version'],
    ['PUT', '/api/blackbox'],
    ['DELETE', '/api/blackbox'],
    ['POST', '/api/keeper/status']
  ];
  for (const [verb, path] of reads) {
    const r = await api(verb, path);
    expect(r.status, verb + ' ' + path).toBe(405);
    expect(r.headers.get('allow'), verb + ' ' + path + ' names the verbs it takes').toBeTruthy();
  }
  const list = await api('GET', '/api/blackbox');
  expect(list.status).toBe(200);
  const web = (list.json.items ?? list.json).find((x: any) => x.name === 'web-b');
  expect(web, 'web-b is in the demo list').toBeTruthy();
  const wrong = await api('PUT', '/api/blackbox/' + web.id);
  expect(wrong.status, 'a Blackbox entry is not rewritten with PUT').toBe(405);
  expect(wrong.headers.get('allow')).toContain('DELETE');
});

test('SEC-09: the wizard says 127.0.0.1 is only this computer and a firewall rule is the person\'s own choice, and a wide bind is refused or called out', async ({ page }) => {
  const dialog = await openDrawer(page);
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-fw');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await expect(dialog.getByLabel('Listens on')).toHaveValue('127.0.0.1');
  await expect(dialog.getByText(/127\.0\.0\.1 means only this computer can ask it/)).toBeVisible();
  await expect(dialog.getByText('Unravis does not add a firewall rule. Adding one is your own choice.')).toBeVisible();
  const next = dialog.getByRole('button', { name: 'Next: Review' });
  for (const wide of ['*', '0.0.0.0/0']) {
    await dialog.getByLabel('Listens on').fill(wide);
    await next.click();
    await expect(dialog.getByRole('list', { name: 'Part 3 of 4' }), wide).toBeVisible();
    await expect(dialog.getByText('Listens on needs an address such as 127.0.0.1, or a server name. Spaces and other symbols are not allowed.')).toBeVisible();
  }
  await dialog.getByLabel('Listens on').fill('0.0.0.0');
  await next.click();
  await expect(dialog.getByText('This Blackbox listens on every address, so other computers can reach it. The read key is the only protection.')).toBeVisible();
});

const BANNED = /\b(cause[sd]?|causing|because|due to|waiting on|blocked by|led to|results? in|proof|prove[sn]?)\b/i;

test('SEC-11: the words on the Blackbox screens and the key keeper cards avoid the banned overclaiming words', async ({ page }) => {
  const seen: string[] = [];
  const check = async (where: string, scope: Locator) => {
    const text = await scope.innerText();
    const hit = BANNED.exec(text);
    if (hit) seen.push(where + ': "' + hit[0] + '"');
  };
  await preview(page, 'saved');
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + CASE }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
  await check('case home', page.locator('#home-sources'));
  for (const p of ['stopped', 'migrate', 'moved']) {
    await preview(page, p);
    await page.getByRole('button', { name: /^All cases/ }).click();
    await page.getByRole('button', { name: 'Open case ' + CASE }).click();
    await expect(page.locator('#home-sources')).toBeVisible();
    await check('keeper ' + p, page.locator('#home-sources'));
  }
  const dialog = await openDrawer(page);
  await check('Blackbox list', dialog);
  for (const name of NINE) {
    await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
    await expect(dialog.getByRole('region', { name })).toBeVisible();
    await check('status of ' + name, dialog);
    await dialog.getByRole('button', { name: 'Back to the list' }).click();
  }
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-lint');
  await check('wizard 1', dialog);
  for (const next of ['Next: Where', 'Next: Settings', 'Next: Review']) {
    await dialog.getByRole('button', { name: next }).click();
    await check('wizard ' + next, dialog);
  }
  expect(seen, 'banned words on screen').toEqual([]);
});
