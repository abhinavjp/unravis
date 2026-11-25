// BL-495j: the Resource counters panel (ResourcePanel, planned in docs/superpowers/plans/2026-10-25-importer-wave2-plan.md U1) is on the case page again.
// It had been mounted in the case tools of the old import page; BL-190 removed that page and left the panel with no place (sweep BLACKBOX-29). The data
// is real: a Windows Performance Monitor CSV goes through the real importer into a demo case, the panel reads /api/cases/{id}/resource and /series.
//   RESCASE-01  a case with a counters file shows "Resource counters" with the host and its summary (highest CPU 96.5 %); "Show lines" draws the chart from /series; Zoom in works on the panel's own window
//   RESCASE-02  a case with no files shows no panel, reads nothing and moves nothing
//   RESCASE-03  a case with only a web log (no counters) shows no panel, reads nothing from /resource and moves nothing (review of PR 253)
//   RESCASE-04  a very long host name wraps inside the block (on a phone, where 63 characters without a hyphen are wider than the block)
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test, expect, type Page } from '../support/fixtures';
import type { Response } from '@playwright/test';
import { createCase, ensureHost, ensureHostLabel, importFile, HOST } from '../support/api';
import { goHome, openCase, layoutSettled } from '../support/ui';
import { perfmonCsv } from '../support/perfmon';

const panelOf = (page: Page) => page.locator('section.tools[aria-labelledby="resource-heading"]');

/** Opens the case and waits until its page is fully read: the Files block (always there) shows what the case holds. */
async function openAndSettle(page: Page, title: string) {
  await goHome(page);
  await openCase(page, title);
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Files', level: 3 })).toBeVisible();
}

test('RESCASE-01: a counters file in a case shows the Resource counters panel on the case page, read from the real routes', async ({ page, api }) => {
  const title = 'QA Resource Panel';
  const id = await createCase(api, title);
  await ensureHost(api);
  const imported = await importFile(api, id, perfmonCsv(), { name: 'perf.csv', importer: 'win-counters', host: HOST });
  expect([200, 201, 202]).toContain(imported.status);
  // The route itself answers with the host, so the panel below is not drawing a made-up answer.
  const answer = await api('GET', `/api/cases/${id}/resource?from=${encodeURIComponent('2026-10-20T08:45:00.000Z')}&to=${encodeURIComponent('2026-10-20T09:00:00.000Z')}`);
  expect(answer.status).toBe(200);
  expect(JSON.stringify(answer.json.hosts)).toContain(HOST);

  await openAndSettle(page, title);
  const panel = panelOf(page);
  await expect(panel.getByRole('heading', { name: 'Resource counters', level: 3 })).toBeVisible();
  await expect(panel.getByRole('heading', { name: HOST, level: 4 })).toBeVisible();
  // The figures come from the server's answer for the made-up file: the high stretch is 96.5 %, the memory falls from 4096 MB to 3506 MB.
  await expect(panel.getByText('Highest host CPU in this window: 96.5 %.')).toBeVisible();
  await expect(panel.getByText(/^Lowest available memory in this window: 3\.4 GiB\./)).toBeVisible();

  const series = page.waitForResponse((r) => r.url().includes(`/api/cases/${id}/series`) && r.status() === 200);
  await panel.getByRole('button', { name: 'Show the resource lines for ' + HOST }).click();
  await series;
  await expect(panel.locator('canvas').first()).toBeVisible();
  await expect(panel.getByRole('button', { name: 'Hide the resource lines for ' + HOST })).toHaveAttribute('aria-expanded', 'true');

  // The panel keeps its own window on this page: Zoom in narrows it and Whole period brings it back (the analysis zoom is not on screen here).
  const range = panel.locator('.zoom .range');
  const whole = await range.innerText();
  await panel.getByRole('button', { name: 'Zoom in', exact: true }).click();
  await expect(range).not.toHaveText(whole);
  await panel.getByRole('button', { name: 'Whole period' }).click();
  await expect(range).toHaveText(whole);
});

test('RESCASE-02: a case with no files shows no panel, reads nothing from /resource and moves nothing', async ({ page, api }) => {
  const title = 'QA Resource None';
  await createCase(api, title);
  const asked: string[] = [];
  page.on('request', (r) => { if (/\/api\/cases\/[^/]+\/resource/.test(r.url())) asked.push(r.url()); });
  await openAndSettle(page, title);
  await expect(panelOf(page)).toHaveCount(0);
  await expect(page.locator('.resource-slot')).toHaveCount(0);
  expect(asked).toEqual([]);
});

test('RESCASE-03: a case with only a web log shows no panel and no empty block, reads nothing from /resource, and the page does not shift', async ({ page, api }) => {
  const title = 'QA Resource LogOnly';
  const id = await createCase(api, title);
  await ensureHost(api);
  const imported = await importFile(api, id, readFileSync(join(__dirname, '..', 'fixtures', 'files', 'access-good.log')), { name: 'access-good.log' });
  expect(imported.status, imported.text).toBeLessThan(300);
  const asked: string[] = [];
  page.on('request', (r) => { if (/\/api\/cases\/[^/]+\/resource/.test(r.url())) asked.push(r.url()); });
  await openAndSettle(page, title);
  await expect(page.getByText('access-good.log', { exact: true })).toBeVisible();
  await layoutSettled(page);
  const top = () => page.getByRole('heading', { name: 'Sources and connections' }).evaluate((el) => Math.round(el.getBoundingClientRect().top));
  const before = await top();
  // Give a late block time to show (the old behaviour drew "Reading the resource counters." and a "These files cover" offer for this case).
  await expect(panelOf(page)).toHaveCount(0, { timeout: 1500 });
  await layoutSettled(page);
  expect(await top()).toBe(before);
  await expect(page.getByText('These files cover')).toHaveCount(0);
  await expect(page.getByText('Reading the resource counters.')).toHaveCount(0);
  expect(asked).toEqual([]);
});

test('RESCASE-04: a very long host name wraps inside the block (no sideways overflow)', async ({ page, api }) => {
  const title = 'QA Resource LongHost';
  const id = await createCase(api, title);
  // The longest name the importer takes is 64 characters; with no hyphen to break at it is wider than a phone's block.
  const host = ('app' + '.server'.repeat(12)).slice(0, 63);
  await ensureHostLabel(api, host);
  const imported = await importFile(api, id, perfmonCsv(host), { name: 'perf.csv', importer: 'win-counters', host });
  expect(imported.status, imported.text).toBeLessThan(300);
  await openAndSettle(page, title);
  await page.setViewportSize({ width: 390, height: 844 });
  const panel = panelOf(page);
  const name = panel.getByRole('heading', { name: host, level: 4 });
  await expect(name).toBeVisible();
  const fits = await panel.evaluate((card, label) => {
    const box = card.getBoundingClientRect();
    const h4 = [...card.querySelectorAll('h4')].find((el) => el.textContent === label)!.getBoundingClientRect();
    const button = card.querySelector('.host-row .toggle')!.getBoundingClientRect();
    return { inside: h4.right <= box.right + 0.5 && button.right <= box.right + 0.5, apart: h4.right <= button.left + 0.5 || h4.bottom <= button.top + 0.5 || button.bottom <= h4.top + 0.5, // beside it or above it, never over it
       noScroll: card.scrollWidth <= card.clientWidth + 1 };
  }, host);
  expect(fits).toEqual({ inside: true, apart: true, noScroll: true });
});

test('RESCASE-05: visible width uses URS1, JSON fallback draws the same chart, and zoom reads are debounced and canceled', async ({ page, api }) => {
  const title = 'QA Resource Binary Chart';
  const id = await createCase(api, title);
  await ensureHost(api);
  expect((await importFile(api, id, perfmonCsv(), { name: 'perf.csv', importer: 'win-counters', host: HOST })).status).toBeLessThan(300);
  await openAndSettle(page, title);
  const panel = panelOf(page);
  const requests: URL[] = [];
  const responses: Response[] = [];
  page.on('request', (request) => { if (new URL(request.url()).pathname === `/api/cases/${id}/series`) requests.push(new URL(request.url())); });
  page.on('response', (response) => { if (new URL(response.url()).pathname === `/api/cases/${id}/series`) responses.push(response); });
  const waitForRead = async (count: number) => {
    await expect.poll(() => responses.length).toBeGreaterThanOrEqual(count);
    await expect(panel.getByText('Reading the lines for this window.')).toHaveCount(0);
    await expect(panel.locator('canvas').first()).toBeVisible();
    return responses[count - 1];
  };

  await panel.getByRole('button', { name: 'Show the resource lines for ' + HOST }).click();
  await waitForRead(1);
  await expect.poll(async () => {
    const box = await panel.locator('.lines').boundingBox();
    return requests.at(-1)?.searchParams.get('width') === String(Math.floor(box!.width));
  }).toBe(true);
  const binary = await waitForRead(requests.length);
  const url = new URL(binary.url());
  expect(url.searchParams.get('format')).toBe('binary');
  expect(url.searchParams.get('bucket')).toBe('1');
  expect(Number(url.searchParams.get('width'))).toBeGreaterThan(0);
  expect(binary.headers()['content-type']).toContain('application/octet-stream');
  const frame = await binary.body();
  expect(frame.subarray(0, 4).toString('ascii')).toBe('URS1');
  const headBytes = frame.readUInt32LE(4);
  expect(headBytes % 8).toBe(0);
  const header = JSON.parse(frame.subarray(8, 8 + headBytes).toString('utf8'));
  expect(header.version).toBe(1);
  expect(header.width).toBe(Number(url.searchParams.get('width')));
  expect(frame.length).toBe(8 + headBytes + header.series.reduce((sum: number, item: { points: number }) => sum + item.points, 0) * 64);
  await page.waitForTimeout(300);
  const canvas = panel.locator('canvas').first();
  const binaryPixels = await canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL());

  // An older server can answer the same read as JSON. Hide/show keeps the window but remounts the chart, so the pixels are directly comparable.
  const fallbackTarget = requests.length + 1;
  await panel.getByRole('button', { name: 'Hide the resource lines for ' + HOST }).click();
  await page.route(new RegExp(`/api/cases/${id}/series\?`), async (route) => {
    const params = new URL(route.request().url()).searchParams;
    params.delete('format'); params.delete('width'); params.set('bucket', String(header.bucketMs));
    const answer = await api('GET', `/api/cases/${id}/series?${params}`);
    expect(answer.status).toBe(200);
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(answer.json) });
  });
  await panel.getByRole('button', { name: 'Show the resource lines for ' + HOST }).click();
  await expect.poll(() => requests.length).toBe(fallbackTarget);
  const fallback = await waitForRead(fallbackTarget);
  expect(fallback.headers()['content-type']).toContain('application/json');
  await page.unroute(new RegExp(`/api/cases/${id}/series\?`));
  await page.waitForTimeout(300);
  expect(await canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL())).toBe(binaryPixels);

  // Several zoom clicks in one turn make one read, not one read per click.
  const beforeZoom = requests.length;
  await panel.getByRole('button', { name: 'Zoom in', exact: true }).evaluate((button) => { for (let i = 0; i < 3; i++) (button as HTMLButtonElement).click(); });
  await page.waitForTimeout(80);
  expect(requests).toHaveLength(beforeZoom);
  await expect.poll(() => requests.length).toBe(beforeZoom + 1);
  await waitForRead(beforeZoom + 1);
  expect(requests).toHaveLength(beforeZoom + 1);

  // A newer window aborts the held read; its late answer cannot replace the whole-period chart.
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  let held!: () => void;
  const heldSeen = new Promise<void>((resolve) => (held = resolve));
  await page.route(new RegExp(`/api/cases/${id}/series\?`), async (route) => {
    const response = await route.fetch();
    held();
    await gate;
    await route.fulfill({ response }).catch(() => undefined);
  });
  const beforeCancel = requests.length;
  const beforeCancelResponses = responses.length;
  await panel.getByRole('button', { name: 'Zoom in', exact: true }).click();
  await heldSeen;
  await panel.getByRole('button', { name: 'Whole period', exact: true }).click();
  await expect.poll(() => requests.length).toBe(beforeCancel + 2);
  release();
  await waitForRead(beforeCancelResponses + 1);
  await page.unroute(new RegExp(`/api/cases/${id}/series\?`));
  expect(requests).toHaveLength(beforeCancel + 2);
  await expect(panel.getByRole('alert')).toHaveCount(0);
  await expect(canvas).toBeVisible();
});
