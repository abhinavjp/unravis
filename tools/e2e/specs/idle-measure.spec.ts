// BL-429: numbers for the pull request. Skipped unless IDLE_MEASURE=1 (it takes about four minutes and prints, it does not judge).
//   IDLE-M1  Blackbox charts open (ECharts on screen), 60 s visible then 60 s hidden, real time: timers that fired, requests, CPU.
//   IDLE-M2  Ten virtual minutes on the page clock, update bar in "downloading" so the one-second poll is on: requests visible vs hidden.
import { test, expect, type Page } from '../support/fixtures';
import { cpu, cpuSession, idleState, installIdleProbe, installVisibility, setVisible, watchRequests } from '../support/idle';

test.skip(process.env.IDLE_MEASURE !== '1', 'set IDLE_MEASURE=1 to print the idle numbers');
test.use({ demoOptions: { cases: false } });
test.setTimeout(400_000);

async function openCharts(page: Page) {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const drawer = page.getByRole('dialog', { name: 'Blackbox' });
  await drawer.getByRole('button', { name: /^Open status: web-a/ }).first().click();
  await page.getByRole('button', { name: 'Open charts' }).first().click();
  await expect(page.locator('dialog.bbc[open] canvas').first()).toBeVisible();
}

test('IDLE-M1: charts open, a minute visible and a minute hidden', async ({ page, demo }) => {
  await installIdleProbe(page);
  await page.goto('about:blank');
  await page.goto(demo.base + '/#t=' + demo.token);
  await openCharts(page);
  const cdp = await cpuSession(page);
  const requests = watchRequests(page);
  const out: Record<string, unknown> = {};
  for (const visible of [true, false]) {
    await setVisible(page, visible);
    await page.mouse.move(300, 300);
    await page.waitForTimeout(1500); // let the switch settle
    requests.reset();
    const t0 = await idleState(page);
    const c0 = await cpu(cdp);
    await page.waitForTimeout(60_000);
    const t1 = await idleState(page);
    const c1 = await cpu(cdp);
    out[visible ? 'visible' : 'hidden'] = {
      timersFired: t1.fired - t0.fired,
      rafFrames: t1.raf - t0.raf,
      liveIntervalsMs: t1.intervals,
      liveTimeouts: t1.timeouts,
      requests: requests.total(),
      requestsBy: requests.by(),
      cpuTaskMs: Math.round(c1.taskMs - c0.taskMs),
      cpuScriptMs: Math.round(c1.scriptMs - c0.scriptMs)
    };
  }
  console.log('IDLE-M1 ' + JSON.stringify(out, null, 1));
});

for (const visible of [true, false]) {
  test('IDLE-M2: ten virtual minutes ' + (visible ? 'visible' : 'hidden') + ', download poll on', async ({ page, demo }) => {
    await installVisibility(page);
    await page.addInitScript(() => { try { sessionStorage.setItem('unravis.updatePreview', 'downloading'); } catch { /* none */ } });
    await page.clock.install();
    await page.goto('about:blank');
    await page.goto(demo.base + '/#t=' + demo.token);
    await page.clock.runFor(3_000);
    await page.mouse.move(200, 200);
    await setVisible(page, visible);
    await page.clock.runFor(2_000);
    const requests = watchRequests(page);
    for (let minute = 0; minute < 10; minute++) {
      await page.mouse.move(200 + minute, 200);
      await page.clock.runFor(60_000);
      await page.waitForTimeout(400); // let the answers come back before the next virtual minute
    }
    console.log('IDLE-M2 ' + (visible ? 'visible' : 'hidden') + ' ' + JSON.stringify({ requests: requests.total(), by: requests.by() }));
  });
}
