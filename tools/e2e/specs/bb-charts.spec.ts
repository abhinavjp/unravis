// Blackbox charts pop-up: the first-time help, status chips, plain scale words, the "What happened" view with crash dumps and
// Windows events (text from the recorded PC stays plain text), Latest compared, Everything, and the table's "Highest at".
// Reads the demo Blackbox "web-a" (its recording carries two crash dumps, a restart and some Windows events). No values are compared
// with live Perfmon here: that stays a check on a real PC.
import { test, expect, type Page } from '../support/fixtures';
import type { Request, Response } from '@playwright/test';

// The charts need no case store.
test.use({ demoOptions: { cases: false } });

async function openCharts(page: Page) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Blackbox' });
  await drawer.getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  const charts = page.locator('dialog.bbc[open]');
  await expect(charts.locator('canvas').first()).toBeVisible();
  return charts;
}

test('Blackbox charts: help card, plain scale words with a tooltip each, quick questions and the highest-at column', async ({ page }) => {
  const charts = await openCharts(page);
  await expect(charts.getByRole('heading', { name: 'How to read these charts' })).toBeVisible();
  await expect(charts.getByRole('button', { name: 'Normal' })).toHaveAttribute('title', /linear scale/);
  await expect(charts.getByRole('button', { name: 'Log (x10 steps)' })).toHaveAttribute('title', /log scale/);
  await expect(charts.getByRole('button', { name: 'Linear', exact: true })).toHaveCount(0);
  await expect(charts.getByRole('columnheader', { name: 'Highest at' })).toBeVisible();
  await charts.getByRole('button', { name: 'Is the disk slow?' }).click();
  await expect(charts.locator('.bbc-figs tbody tr')).toHaveCount(2);
  await charts.locator('.bbc-rowbtn').first().click();
  await expect(charts.locator('.bbc-meaning')).toContainText('disk');
  // the help card is dismissed for good, and can be brought back
  await charts.getByRole('button', { name: 'Got it' }).click();
  await expect(charts.getByRole('heading', { name: 'How to read these charts' })).toHaveCount(0);
  await charts.getByRole('button', { name: 'Show help' }).click();
  await expect(charts.getByRole('heading', { name: 'How to read these charts' })).toBeVisible();
  // Esc closes only the charts pop-up
  await page.keyboard.press('Escape');
  await expect(page.locator('dialog.bbc')).toHaveCount(0);
});

test('Blackbox charts: What happened lists dumps, restarts and Windows events, and recorded names stay plain text', async ({ page }) => {
  const charts = await openCharts(page);
  await charts.getByRole('button', { name: 'What happened', exact: true }).click();
  await expect(charts.getByRole('heading', { name: 'Timeline of this time range' })).toBeVisible();
  await expect(charts.getByText('Crash dump: w3wp.exe.4412.dmp')).toBeVisible();
  // the folder also holds a month-old dump; it is not part of this range
  await expect(charts.getByText(/old-crash-from-last-month/)).toHaveCount(0);
  // a hostile file name is text, never an element
  await expect(charts.getByText('Crash dump: <img src=x onerror=alert(1)>.dmp')).toBeVisible();
  await expect(charts.locator('img')).toHaveCount(0);
  await expect(charts.getByText('The recorder started again')).toBeVisible();
  const events = charts.getByRole('table').nth(1);
  await expect(events.getByRole('row', { name: /System Critical 41 1 Usually means: Windows restarted without a clean shutdown/ })).toBeVisible();
  await expect(charts.getByRole('row', { name: /Time with no recording/ })).toBeVisible();
  // no folder from the recorded PC reaches the page
  expect(await charts.innerText()).not.toMatch(/[A-Z]:\\|\/Users\//);
});

test('Blackbox charts: Latest compared and Everything read the same recording', async ({ page }) => {
  const charts = await openCharts(page);
  await charts.getByRole('button', { name: 'Latest compared', exact: true }).click();
  await expect(charts.getByRole('img', { name: /^Processor busy \(% Processor Time\): (Near its highest|In its usual range|Near its lowest|Did not change)/ })).toBeVisible();
  await charts.getByRole('button', { name: 'Everything', exact: true }).click();
  await expect(charts.getByText('Memory that programs could use right away')).toBeVisible();
  await charts.getByRole('searchbox', { name: 'Find a counter' }).fill('disk queue');
  await expect(charts.getByRole('rowheader', { name: /Disk queue/ })).toBeVisible();
  await charts.getByRole('searchbox', { name: 'Find a counter' }).fill('zzz-no-such-counter');
  await expect(charts.getByText('No counter matches.')).toBeVisible();
  await charts.getByRole('button', { name: 'Charts', exact: true }).click();
  await expect(charts.locator('canvas').first()).toBeVisible();
});

test('Blackbox charts: visible width uses binary M4, JSON draws the same pixels, and detail reads debounce and cancel', async ({ page, api }) => {
  const pattern = /\/api\/blackbox\/[^/]+\/series$/;
  const requests: Request[] = [];
  const responses: Response[] = [];
  page.on('request', (request) => { if (pattern.test(new URL(request.url()).pathname)) requests.push(request); });
  page.on('response', (response) => { if (pattern.test(new URL(response.url()).pathname)) responses.push(response); });
  const charts = await openCharts(page);
  const canvas = charts.locator('canvas').first();
  const settled = async (count: number) => {
    await expect.poll(() => responses.length).toBeGreaterThanOrEqual(count);
    await expect(charts.getByRole('status')).toHaveCount(0);
    await expect(canvas).toBeVisible();
    return responses[count - 1];
  };

  const binary = await settled(1);
  expect(binary.headers()['content-type']).toContain('application/vnd.unravis.series+f64');
  expect(requests[0].headers()['accept']).toContain('application/vnd.unravis.series+f64');
  const body = requests[0].postDataJSON();
  expect(body.shape).toBe('m4');
  expect(body.width).toBeGreaterThan(0);
  expect(body.points).toBeGreaterThan(0);
  expect(body.points).toBeLessThanOrEqual(body.width);
  const frame = await binary.body();
  // The frame starts with the magic "UBB1" and a u16 version, then the u32 header length (10 bytes in all).
  expect(frame.subarray(0, 4).toString('latin1')).toBe('UBB1');
  expect(frame.readUInt16LE(4)).toBe(1);
  const headBytes = frame.readUInt32LE(6);
  expect(headBytes).toBeGreaterThan(1);
  expect(10 + headBytes).toBeLessThanOrEqual(frame.length);
  const header = JSON.parse(frame.subarray(10, 10 + headBytes).toString('utf8'));
  const pointCount = [
    ...header.lines.map((line: { points: { n: number } }) => line.points.n),
    ...header.processes.flatMap((proc: { cpu: { n: number }; workingSet: { n: number }; privateBytes: { n: number } }) => [proc.cpu.n, proc.workingSet.n, proc.privateBytes.n])
  ].reduce((sum: number, n: number) => sum + n, 0);
  expect(frame.length).toBe(10 + headBytes + pointCount * 48);
  await expect.poll(() => page.evaluate(() => (window as any).__unravisPerf?.some((sample: { kind: string }) => sample.kind === 'blackbox-paint') ?? false)).toBe(true);
  await page.waitForTimeout(300);
  const binaryPixels = await canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL());

  // An older server can answer the same window as JSON; the fallback must draw exactly the same chart.
  const path = new URL(requests[0].url()).pathname;
  await page.route(pattern, async (route) => {
    const answer = await api('POST', path, route.request().postDataJSON());
    await route.fulfill({ status: answer.status, contentType: 'application/json', body: answer.text });
  });
  await page.keyboard.press('Escape');
  await expect(page.locator('dialog.bbc')).toHaveCount(0);
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  const reopened = page.locator('dialog.bbc[open]');
  await expect(reopened.locator('canvas').first()).toBeVisible();
  const fallback = await settled(2);
  expect(fallback.headers()['content-type']).toContain('application/json');
  await page.waitForTimeout(300);
  expect(await reopened.locator('canvas').first().evaluate((node) => (node as HTMLCanvasElement).toDataURL())).toBe(binaryPixels);
  await page.unroute(pattern);

  // Three detail-window changes in one turn make one read after the 150 ms debounce.
  const beforeDetail = requests.length;
  await reopened.getByRole('button', { name: 'Zoom in', exact: true }).click();
  await reopened.getByRole('button', { name: 'Read this range in detail', exact: true }).evaluate((button) => {
    for (let i = 0; i < 3; i++) (button as HTMLButtonElement).click();
  });
  await page.waitForTimeout(80);
  expect(requests).toHaveLength(beforeDetail);
  await expect.poll(() => requests.length).toBe(beforeDetail + 1);
  await settled(beforeDetail + 1);

  // Going back aborts a held detail read. Its late answer cannot replace the wider chart.
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  let held!: () => void;
  const heldSeen = new Promise<void>((resolve) => (held = resolve));
  let heldCount = 0;
  await page.route(pattern, async (route) => {
    const response = await route.fetch();
    heldCount += 1;
    if (heldCount === 1) { held(); await gate; }
    await route.fulfill({ response }).catch(() => undefined);
  });
  const beforeCancel = requests.length;
  const widerBody = requests.at(-1)!.postDataJSON();
  try {
    await reopened.getByRole('button', { name: 'Zoom in', exact: true }).click();
    await reopened.getByRole('button', { name: 'Read this range in detail', exact: true }).click();
    await heldSeen;
    await reopened.getByRole('button', { name: 'Back to the wider range', exact: true }).click();
    await expect.poll(() => requests.length).toBe(beforeCancel + 2);
    release();
    await expect(reopened.getByRole('status')).toHaveCount(0);
    await expect(reopened.locator('canvas').first()).toBeVisible();
    expect(requests.at(-1)!.postDataJSON().start).toBe(widerBody.start);
    expect(requests.at(-1)!.postDataJSON().end).toBe(widerBody.end);
    expect(requests).toHaveLength(beforeCancel + 2);
    await expect(reopened.getByRole('alert')).toHaveCount(0);
  } finally {
    release();
    await page.unroute(pattern);
  }
});
