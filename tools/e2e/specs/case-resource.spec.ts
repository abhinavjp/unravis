// BL-495j: the Resource counters panel (ResourcePanel, planned in docs/superpowers/plans/2026-10-25-importer-wave2-plan.md U1) is on the case page again.
// It had been mounted in the case tools of the old import page; BL-190 removed that page and left the panel with no place (sweep BLACKBOX-29). The data
// is real: a Windows Performance Monitor CSV goes through the real importer into a demo case, the panel reads /api/cases/{id}/resource and /series.
//   RESCASE-01  a case with a counters file shows "Resource counters" with the host, its summary and its coverage; "Show lines" draws the chart from /series
//   RESCASE-02  a case with no counter file shows no panel and no empty block
import { test, expect } from '../support/fixtures';
import { createCase, ensureHost, importFile, HOST } from '../support/api';
import { goHome, openCase } from '../support/ui';
import { perfmonCsv } from '../support/perfmon';

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

  await goHome(page);
  await openCase(page, title);
  const panel = page.locator('section.tools[aria-labelledby="resource-heading"]');
  await expect(panel.getByRole('heading', { name: 'Resource counters', level: 3 })).toBeVisible();
  await expect(panel.getByRole('heading', { name: HOST, level: 4 })).toBeVisible();
  // The summary line and the coverage dots come from the server's answer.
  await expect(panel.locator('.host .line').first()).not.toHaveText('');
  await expect(panel.getByText('No counters were read for this host in this window.')).toHaveCount(0);

  const series = page.waitForResponse((r) => r.url().includes(`/api/cases/${id}/series`) && r.status() === 200);
  await panel.getByRole('button', { name: 'Show the resource lines for ' + HOST }).click();
  await series;
  await expect(panel.locator('canvas').first()).toBeVisible();
  await expect(panel.getByRole('button', { name: 'Hide the resource lines for ' + HOST })).toHaveAttribute('aria-expanded', 'true');
});

test('RESCASE-02: a case with no counters file shows no panel and no empty block', async ({ page, api }) => {
  const title = 'QA Resource None';
  await createCase(api, title);
  await goHome(page);
  await openCase(page, title);
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  // Let the panel's first read finish; then it must have left nothing behind.
  await page.waitForLoadState('networkidle');
  await expect(page.locator('section.tools[aria-labelledby="resource-heading"]')).toHaveCount(0);
  await expect(page.locator('.resource-slot')).toBeHidden();
});
