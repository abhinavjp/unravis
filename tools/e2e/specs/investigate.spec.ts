// The investigation explorer in the demo: first run, windows, ranked groups, quality counts,
// split by, evidence tabs, request history, record detail, activity chart, Pulse versus the
// ranked list, time zone toggle, multi-API compare. The demo opens already analysed
// (Last 15 min, Sync, 20 Oct 08:45 to 09:00 UTC); nothing here changes demo data, so the
// tests may run in any order.
import { test, expect, type Page } from '../support/fixtures';

async function run(page: Page) {
  const done = page.waitForResponse((r) => r.url().includes('/api/analyze'));
  await page.getByRole('button', { name: 'Run analysis' }).click();
  return done;
}

async function openWhere(page: Page) {
  await page.getByRole('button', { name: 'Where is it?', exact: true }).click();
  const list = page.getByRole('list', { name: 'Operations, worst first' });
  await expect(list).toBeVisible();
  return list;
}

async function openEvidence(page: Page, name = 'Payroll.Run') {
  await openWhere(page);
  await page.getByRole('button', { name: new RegExp('^' + name.replace('.', '\\.') + ', synchronous') }).click();
  const panel = page.getByRole('complementary', { name });
  await expect(panel).toBeVisible();
  return panel;
}

async function openFilters(page: Page, tab: string) {
  if (!(await page.getByRole('tablist', { name: 'Filter groups' }).isVisible())) {
    await page.getByText('More filters').first().click();
  }
  await page.getByRole('tab', { name: tab }).click();
}

test('INV-01: the demo opens analysed; five figures, a headline and the quick ranges all work', async ({ page }) => {
  const pulse = page.getByRole('region', { name: 'Pulse' });
  await expect(pulse.getByRole('button', { name: /^Requests 508 in the selected period/ })).toBeVisible();
  await expect(pulse.getByRole('button', { name: /^Completion 98\.4% 500 completed/ })).toBeVisible();
  await expect(pulse.getByRole('button', { name: /^Observed P95 256 ms 5\.0x the earlier period/ })).toBeVisible();
  await expect(pulse.getByRole('button', { name: /^4xx 0 / })).toBeVisible();
  await expect(pulse.getByRole('button', { name: /^5xx 0 / })).toBeVisible();
  await expect(pulse.getByRole('status').first()).toContainText('P95 is about 5 times what it was in the previous 15 minutes');
  await expect(pulse.getByRole('status').first()).toContainText('The biggest rise is in Payroll.Run');
  await expect(page.getByRole('button', { name: 'Last 15 min' })).toHaveAttribute('aria-pressed', 'true');
  // Every quick range runs and moves the pressed state.
  for (const label of ['Last hour', 'Last 4 hours', 'Last 24 hours', 'Last 7 days', 'Last 15 min']) {
    await page.getByRole('button', { name: label, exact: true }).click();
    await run(page);
    await expect(page.getByRole('button', { name: label, exact: true })).toHaveAttribute('aria-pressed', 'true');
    await expect(pulse.getByRole('button', { name: /^Requests / })).toBeVisible();
  }
  await expect(pulse.getByRole('button', { name: /^Requests 508 in the selected period/ })).toBeVisible();
});

test('INV-02: the date and time fields are plain boxes and refuse bad windows', async ({ page }) => {
  await openFilters(page, 'When');
  const investigate = page.getByRole('region', { name: 'Investigate' });
  const reference = page.getByRole('region', { name: 'Reference' });
  await expect(investigate.getByRole('group', { name: 'From' }).getByRole('textbox', { name: 'Date' })).toHaveValue('2026-10-20');
  await expect(investigate.getByRole('group', { name: 'From' }).getByRole('textbox', { name: 'Time' })).toHaveValue('08:45:00.000');
  await expect(reference.getByRole('group', { name: 'From' }).getByRole('textbox', { name: 'Date' })).toBeVisible();
  const run_ = page.getByRole('button', { name: 'Run analysis' });

  // An edit alone only waits for Run analysis.
  const toDate = investigate.getByRole('group', { name: 'To' }).getByRole('textbox', { name: 'Date' });
  await toDate.fill('2026-10-20');
  await investigate.getByRole('group', { name: 'To' }).getByRole('textbox', { name: 'Time' }).fill('08:59:00.000');
  await expect(page.getByText('Changes here apply when you press Run analysis.')).toBeVisible();

  // The end before the start is named, and Run analysis refuses to send it.
  const toTime = investigate.getByRole('group', { name: 'To' }).getByRole('textbox', { name: 'Time' });
  let sent = 0;
  page.on('request', (r) => {
    if (r.url().includes('/api/analyze')) sent++;
  });
  await toTime.fill('08:00:00.000');
  await expect(page.getByText('Each window must start before it ends.')).toBeVisible();
  await run_.click();
  await expect(page.getByText('Comparison not run: fix the filter first')).toBeVisible();

  // More than seven days is refused in the same way.
  await toTime.fill('09:00:00.000');
  const fromDate = investigate.getByRole('group', { name: 'From' }).getByRole('textbox', { name: 'Date' });
  await fromDate.fill('2026-10-10');
  await expect(page.getByText('Each window can be at most 7 days.')).toBeVisible();
  await run_.click();
  await expect(page.getByText('Comparison not run: fix the filter first')).toBeVisible();
  expect(sent, 'analysis requests sent for refused windows').toBe(0);

  // A cleared date explains itself and switches Run off.
  await fromDate.fill('');
  await expect(page.getByText('Enter both a date and time for every range.', { exact: false })).toBeVisible();
  await expect(page.getByText('Complete the dates to run the analysis.')).toBeVisible();
  await expect(run_).toBeDisabled();

  // Putting a good value back lets it run again.
  await fromDate.fill('2026-10-20');
  await expect(run_).toBeEnabled();
  await run(page);
});

test('INV-03: groups are ranked worst first, the rank chips reorder them, the note says these are estimates', async ({ page }) => {
  const list = await openWhere(page);
  const rows = list.getByRole('button');
  await expect(rows.first()).toHaveAccessibleName(/^Payroll\.Run, synchronous, 256 requests/);
  await expect(rows.nth(1)).toHaveAccessibleName(/^Employees\.List, synchronous, 252 requests/);
  const rank = page.getByRole('group', { name: 'Rank by' });
  for (const label of ['Requests', 'Completion', 'P95', '4xx', '5xx']) {
    await expect(rank.getByRole('button', { name: new RegExp('^' + label) })).toBeVisible();
  }
  // Requests rank puts the group with the most requests first.
  await rank.getByRole('button', { name: /^Requests/ }).click();
  await expect(rank.getByRole('button', { name: /^Requests/ })).toHaveAttribute('aria-pressed', 'true');
  await expect(list.getByRole('button').first()).toHaveAccessibleName(/^Payroll\.Run/);
  // P95 puts the slow API first again.
  await rank.getByRole('button', { name: /^P95/ }).click();
  await expect(list.getByRole('button').first()).toHaveAccessibleName(/^Payroll\.Run/);
  // The full table of groups is behind a disclosure; the estimates note is on the page.
  await expect(page.getByText('All 8 API groups').first()).toBeVisible();
  await expect(page.getByText('Response times are estimates. A comparison shows change, not a health verdict.').first()).toBeAttached();
});

test('INV-04: the quality strip counts missing, unusable and unclassified work', async ({ page }) => {
  await openWhere(page);
  const quality = page.locator('details').filter({ has: page.locator('summary', { hasText: 'Quality of the data' }) });
  await quality.locator('summary').first().click();
  await expect(quality).toContainText(/8[ ]*API groups/);
  await expect(quality).toContainText(/6[ ]*Missing timing/);
  await expect(quality).toContainText(/2[ ]*Unusable timing/);
  await expect(quality).toContainText(/8[ ]*Unclassified result/);
  await expect(quality).toContainText('What these counts mean');
});

test('INV-05: Split by lists the dimensions and each one shows rows that add up', async ({ page }) => {
  await openWhere(page);
  const split = page.locator('details').filter({ has: page.locator('summary', { hasText: /Split by/ }) }).last();
  await split.locator('summary').first().click();
  for (const name of ['Status code', 'User', 'UI version', 'User agent', 'Polling type', 'User IP', 'Hour of day', 'Request size']) {
    await expect(split.getByRole('button', { name, exact: true })).toBeVisible();
  }
  await split.getByRole('button', { name: 'Status code', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Request breakdown' }).getByText('200 OK').first()).toBeVisible();
  await expect(page.getByRole('region', { name: 'Request breakdown' }).getByText('508 requests · 2 distinct values')).toBeVisible();
  await expect(split.locator('summary').first()).toContainText('Status code');
  // "No split" takes it away again.
  await split.locator('summary').first().click();
  await split.getByRole('button', { name: 'No split', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Request breakdown' }).getByText('200 OK')).toHaveCount(0);
  // Another dimension shows its own rows.
  await split.locator('summary').first().click();
  await split.getByRole('button', { name: 'Hour of day', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Request breakdown' }).getByText('08:00').first()).toBeVisible();
});

test('INV-06: the evidence frame has five tabs and each opens its own panel', async ({ page }) => {
  const panel = await openEvidence(page);
  const tabs = panel.getByRole('tablist', { name: 'Evidence' });
  for (const name of ['Timeline', 'Phases', 'Incomplete', 'Slowest', 'Breakdown']) {
    await tabs.getByRole('tab', { name }).click();
    await expect(tabs.getByRole('tab', { name })).toHaveAttribute('aria-selected', 'true');
    await expect(panel.getByRole('tabpanel', { name })).toBeVisible();
  }
  await tabs.getByRole('tab', { name: 'Slowest' }).click();
  await expect(panel.getByRole('region', { name: 'Traffic versus slowness' })).toBeVisible();
  await expect(panel.getByRole('spinbutton', { name: 'Slow request threshold in milliseconds' })).toHaveValue('1000');
  await panel.getByRole('button', { name: 'Close and go back' }).click();
  await expect(page.getByRole('complementary', { name: 'Payroll.Run' })).toHaveCount(0);
});

test('INV-07: request history is newest first and Export CSV hands over a file with no payload', async ({ page }) => {
  const panel = await openEvidence(page);
  await panel.getByRole('tab', { name: 'Slowest' }).click();
  const table = panel.getByRole('table').last();
  const rows = table.getByRole('row');
  await expect(rows.nth(1)).toContainText('20 Oct 08:59:52');
  await expect(rows.nth(1)).toContainText('Payroll.Run');
  await expect(rows.nth(1)).toContainText('200 OK');
  await expect(table.getByRole('columnheader', { name: 'Initiated (UTC)' })).toBeVisible();
  // Newest first: the first column of each row never goes back up.
  const stamps = await table.getByRole('row').evaluateAll((trs) =>
    trs.slice(1).map((tr) => (tr.querySelector('td')?.textContent ?? '').match(/\d+ \w+ \d\d:\d\d:\d\d/)?.[0] ?? ''),
  );
  expect(stamps.length).toBeGreaterThan(5);
  const sorted = [...stamps].sort().reverse();
  expect(stamps).toEqual(sorted);

  const [download] = await Promise.all([
    page.waitForEvent('download'),
    panel.getByRole('button', { name: 'Export CSV' }).click(),
  ]);
  expect(download.suggestedFilename()).toBe('unravis-requests.csv');
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const c of stream) chunks.push(c as Buffer);
  const csv = Buffer.concat(chunks).toString('utf8');
  const lines = csv.trim().split(/\r?\n/);
  expect(lines[0]).toContain('sourceId');
  expect(lines[0]).toContain('durationMs');
  expect(lines.length).toBe(257);
  expect(csv).not.toMatch(/Illustrative demo (request|response)/);
  // No cell starts with a formula character (a leading minus is left out: durations can be negative numbers).
  expect(lines.slice(1).filter((l) => /(^|,)"?[=+@]/.test(l))).toEqual([]);
});

test('INV-08: Inspect opens the whole record and Close or Esc hands back the focus', async ({ page }) => {
  const panel = await openEvidence(page);
  await panel.getByRole('tab', { name: 'Slowest' }).click();
  const inspect = panel.getByRole('button', { name: /^Inspect record demo-west-Payroll-current-124$/ });
  await inspect.click();
  await expect(page.getByRole('heading', { name: 'Record demo-west-Payroll-current-124' })).toBeVisible();
  await expect(page.getByText('Every field stored for this audit record. Payloads are limited by the server.')).toBeVisible();
  await expect(page.getByText('Stored fields · times in UTC')).toBeVisible();
  await expect(page.getByText('Derived by Unravis')).toBeVisible();
  await expect(page.getByText('Illustrative demo request for demo-west-Payroll-current-124')).toBeVisible();
  await expect(page.getByText('Illustrative demo response for demo-west-Payroll-current-124')).toBeVisible();
  await page.getByRole('button', { name: 'Close record details' }).click();
  await expect(page.getByRole('heading', { name: 'Record demo-west-Payroll-current-124' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /^Inspect record demo-west-Payroll-current-124$/ })).toBeFocused();
  // Esc does the same.
  await page.getByRole('button', { name: /^Inspect record demo-west-Payroll-current-124$/ }).click();
  await expect(page.getByRole('heading', { name: 'Record demo-west-Payroll-current-124' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('heading', { name: 'Record demo-west-Payroll-current-124' })).toHaveCount(0);
});

test('INV-08a: a record with no end time shows "not recorded", never a blank', async ({ page }) => {
  const panel = await openEvidence(page);
  await panel.getByRole('tab', { name: 'Incomplete' }).click();
  await panel.getByRole('button', { name: /^List incomplete, oldest first/ }).click();
  await panel.getByRole('button', { name: /^Inspect record demo-.*-Payroll-stale-0$/ }).first().click();
  await expect(page.getByRole('heading', { name: /^Record demo-.*-Payroll-stale-0$/ })).toBeVisible();
  await expect(page.getByText('not recorded', { exact: true }).first()).toBeVisible();
  // Every row of both tables has a value cell with text.
  const blanks = await page.locator('table tr').evaluateAll((trs) =>
    trs.filter((tr) => tr.children.length === 2 && !(tr.children[1].textContent ?? '').trim()).length,
  );
  expect(blanks).toBe(0);
});

test('INV-09: the activity chart on Last 4 hours names its zone and its series', async ({ page }) => {
  await page.getByRole('button', { name: 'Last 4 hours', exact: true }).click();
  await run(page);
  const chart = page.getByRole('region', { name: 'Requests over time' });
  await expect(chart.getByRole('heading', { name: /Requests over time/ })).toContainText('Times in UTC');
  await expect(chart).toContainText('Requests per interval');
  await expect(chart).toContainText('No timed requests between');
  await expect(chart).toContainText('Slowest request in the interval');
  await expect(chart.getByRole('img', { name: /Requests per interval with the 95th percentile and the slowest request of each interval, in UTC/ })).toBeVisible();
  await expect(page.getByText('Showing 20 Oct 05:00 to 09:00 UTC.', { exact: false }).first()).toBeVisible();
});

test('INV-12: the Pulse P95 change and the Payroll.Run row tell the same story', async ({ page }) => {
  const tile = page.getByRole('button', { name: /^Observed P95 256 ms 5\.0x the earlier period/ });
  await tile.click();
  const list = page.getByRole('list', { name: 'Operations, worst first' });
  await expect(list).toBeVisible();
  await page.getByRole('button', { name: /^Payroll.Run, synchronous/ }).click();
  const panel = page.getByRole('complementary', { name: 'Payroll.Run' });
  await panel.getByRole('tab', { name: 'Slowest' }).click();
  // 256 ms now and about 205 ms more than before: the earlier time is about 51 ms, which is 5.0x.
  await expect(panel.getByText(/the typical request took about 256 milliseconds/)).toBeVisible();
  await expect(panel.getByText(/about 205 milliseconds longer than in the earlier period/)).toBeVisible();
  await panel.getByRole('button', { name: 'Close and go back' }).click();
  await expect(list.getByRole('button').first()).toHaveAccessibleName(/^Payroll\.Run, synchronous, 256 ms P95/);
});

test('INV-12a: a window with no data shows dashes, not zeros', async ({ page }) => {
  await openFilters(page, 'When');
  const investigate = page.getByRole('region', { name: 'Investigate' });
  const reference = page.getByRole('region', { name: 'Reference' });
  const set = async (region: ReturnType<Page['getByRole']>, group: string, date: string, time: string) => {
    await region.getByRole('group', { name: group }).getByRole('textbox', { name: 'Date' }).fill(date);
    await region.getByRole('group', { name: group }).getByRole('textbox', { name: 'Time' }).fill(time);
  };
  await set(investigate, 'From', '2026-10-17', '10:30:00.000');
  await set(investigate, 'To', '2026-10-17', '11:00:00.000');
  await set(reference, 'From', '2026-10-17', '09:30:00.000');
  await set(reference, 'To', '2026-10-17', '10:30:00.000');
  await run(page);
  const pulse = page.getByRole('region', { name: 'Pulse' });
  await expect(pulse.getByRole('button', { name: /^Requests 0 / })).toBeVisible();
  await expect(pulse.getByRole('button', { name: /^Completion — / })).toBeVisible();
  await expect(pulse.getByRole('button', { name: /^Observed P95 — / })).toBeVisible();
});

test('INV-15: the UTC / Local toggle changes labels only; no new analysis is requested', async ({ page }) => {
  const pulse = page.getByRole('region', { name: 'Pulse' });
  await expect(pulse.getByRole('button', { name: /^Requests / })).toBeVisible();
  const before = await pulse.getByRole('button').allInnerTexts();
  let analyses = 0;
  page.on('request', (r) => {
    if (r.url().includes('/api/analyze')) analyses++;
  });
  const group = page.getByRole('group', { name: 'Show times in' });
  await expect(group.getByRole('button', { name: 'UTC', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await group.getByRole('button', { name: /^Local/ }).click();
  await expect(group.getByRole('button', { name: /^Local/ })).toHaveAttribute('aria-pressed', 'true');
  expect(await pulse.getByRole('button').allInnerTexts()).toEqual(before);
  await group.getByRole('button', { name: 'UTC', exact: true }).click();
  await expect(group.getByRole('button', { name: 'UTC', exact: true })).toHaveAttribute('aria-pressed', 'true');
  expect(await pulse.getByRole('button').allInnerTexts()).toEqual(before);
  // Theme only recolours.
  const theme = page.getByRole('button', { name: /^Theme:/ });
  await theme.click();
  await theme.click();
  expect(await pulse.getByRole('button').allInnerTexts()).toEqual(before);
  expect(analyses, 'analysis requests caused by the toggles').toBe(0);
});

test('INV-16: ticking groups for Compare adds them to one chart and removing them takes them out', async ({ page }) => {
  await openWhere(page);
  const table = page.locator('details').filter({ has: page.locator('summary', { hasText: /All 8 API groups/ }) });
  await table.locator('summary').click();
  const boxes = table.getByRole('checkbox', { name: /^Compare timeline for / });
  await expect(boxes).toHaveCount(8);
  await expect(boxes.first()).toBeChecked();
  let calls = 0;
  page.on('request', (r) => {
    if (/\/api\/(analyze|breakdown|timeline)/.test(r.url())) calls++;
  });
  await boxes.nth(1).check();
  await boxes.nth(2).check();
  await expect(boxes.nth(1)).toBeChecked();
  await expect(boxes.nth(2)).toBeChecked();
  await boxes.nth(2).uncheck();
  await expect(boxes.nth(2)).not.toBeChecked();
  expect(calls, 'new data requests caused by ticking').toBe(0);
});
