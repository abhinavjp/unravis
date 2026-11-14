// The Slow call screen as a full page on the demo: opening it from a red diamond, the three ways back to the
// timeline, a reload, the lane fit at two window sizes, the one window control driving both lists, and every
// "Pretend the database is" state showing its own sentence. Demo only; the same checks on live data are P-46.
//   SCALL-01  A diamond opens the screen with three lanes, the call's facts, the database source and the overview strip; it is a page, not a dialog.
//   SCALL-02  Back button, Esc and the browser Back all return to the timeline with the same window and no new tracing read; Forward shows the screen again.
//   SCALL-03  A reload on the screen comes back to the same call; Back then shows the timeline.
//   SCALL-04  The three lanes fit without an inner scroll at 1440x800 and 1100x760, and the pinned timeline stays in view after a scroll.
//   SCALL-05  The window presets and Earlier/Later drive the hint and the window line; the All sources tab opens; dragging the strip box reads only on release.
//   SCALL-06  Each pretend state shows its own sentence; the off/denied/events/notread states draw no database shape, and never claim Query Store is on.
import { test, expect, type Page } from '../support/fixtures';
import { goHome, openCase } from '../support/ui';
import { join } from 'node:path';
import { mkdirSync, writeFileSync } from 'node:fs';

const CASE_TITLE = 'Orders API stall, 19 October';
const REPORT_DIR = join(__dirname, '..', 'report');

async function openTimeline(page: Page): Promise<void> {
  await goHome(page);
  await openCase(page, CASE_TITLE);
  await page.getByRole('button', { name: 'Open the Investigation Timeline' }).click();
  await expect(page.getByRole('heading', { name: 'Investigation Timeline' })).toBeVisible();
}

/** Clicks the first red diamond of the API lane at its centre. */
async function clickFirstDiamond(page: Page, n = 0): Promise<void> {
  const diamond = page.locator('.panel[data-lane=audit] .mark.flagged').nth(n);
  await expect(diamond).toBeVisible();
  await diamond.scrollIntoViewIfNeeded();
  const box = await diamond.boundingBox();
  if (!box) throw new Error('the diamond has no box');
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
}

/** Opens the Slow call screen from the first red diamond and waits until the call's facts are drawn. */
async function openSlowCall(page: Page): Promise<void> {
  await clickFirstDiamond(page);
  await expect(page.getByTestId('slow-call-screen')).toBeVisible();
  await expect(page.getByTestId('scall-thiscall')).toBeVisible();
  await expect(page.getByTestId('scall-lanes')).toBeVisible();
}

/** Counts every tracing read the page starts (the timeline and the Slow call screen share this endpoint). */
function countTracing(page: Page): () => number {
  let n = 0;
  page.on('request', (request) => {
    if (/^\/api\/cases\/[^/]+\/tracing$/.test(new URL(request.url()).pathname)) n++;
  });
  return () => n;
}

test('SCALL-01: a diamond opens the Slow call screen with three lanes and the call\'s facts', async ({ page }) => {
  await openTimeline(page);
  await openSlowCall(page);
  const screen = page.getByTestId('slow-call-screen');
  await expect(screen).toBeVisible();
  // The three lanes are all drawn.
  await expect(page.getByTestId('scall-lane-audit')).toBeVisible();
  await expect(page.getByTestId('scall-lane-iis')).toBeVisible();
  await expect(page.getByTestId('scall-lane-sql')).toBeVisible();
  // The call's facts and the database source line are filled in (never blank).
  const thisCall = ((await page.getByTestId('scall-thiscall').innerText()) ?? '').trim();
  expect(thisCall.length, 'the "This call" facts have text').toBeGreaterThan(0);
  const dbSource = ((await page.getByTestId('scall-db-source').innerText()) ?? '').trim();
  expect(dbSource.length, 'the database lane names its source').toBeGreaterThan(0);
  // The overview strip is present, either as the map or as the note that says the whole strip is already shown.
  const mapOrHidden = await page.getByTestId('scall-map').count() + await page.getByTestId('scall-map-hidden').count();
  expect(mapOrHidden, 'the overview strip or its hidden note is present').toBe(1);
  // It is a full page, not a modal: no dialog is open.
  await expect(page.getByRole('dialog'), 'the screen is a page, not a dialog').toHaveCount(0);
});

test('SCALL-02: Back button, Esc and the browser Back all return to the timeline with the same window', async ({ page }) => {
  const tracing = countTracing(page);
  await openTimeline(page);
  const paper = page.locator('.tracing-paper');
  const screen = page.getByTestId('slow-call-screen');
  const metaline = page.getByTestId('tp-metaline');
  await expect(metaline).toBeVisible();
  const windowBefore = (await metaline.innerText()).trim();
  expect(windowBefore.length, 'the timeline window text was read before opening').toBeGreaterThan(0);

  // Each way of leaving must land on the timeline, keep the window, and start no new tracing read.
  const leaveWays: { name: string; leave: () => Promise<void> }[] = [
    { name: 'the Back button', leave: async () => { await page.getByTestId('scall-back-btn').click(); } },
    { name: 'Esc', leave: async () => { await page.keyboard.press('Escape'); } },
    { name: 'the browser Back', leave: async () => { await page.goBack(); } }
  ];
  for (const way of leaveWays) {
    await openSlowCall(page);
    await expect(paper).toBeHidden();
    const before = tracing();
    expect(before, 'the opening read was observed').toBeGreaterThan(0);
    await way.leave();
    // The packet asks for a 500 ms watch for any new tracing read after leaving.
    await page.waitForTimeout(500);
    expect(tracing(), `${way.name} does not read the timeline again`).toBe(before);
    await expect(screen, `${way.name} closes the screen`).toHaveCount(0);
    await expect(paper, `${way.name} shows the timeline`).toBeVisible();
    await expect(metaline).toBeVisible();
    const windowAfter = (await metaline.innerText()).trim();
    expect(windowAfter, `${way.name} keeps the same timeline window`).toBe(windowBefore);
  }

  // The browser Forward shows the Slow call screen again.
  await page.goForward();
  await expect(screen, 'browser Forward shows the Slow call screen again').toBeVisible();
  await expect(page.getByTestId('scall-thiscall')).toBeVisible();
});

test('SCALL-03: a reload on the screen comes back to the same call', async ({ page }) => {
  await openTimeline(page);
  await openSlowCall(page);
  const screen = page.getByTestId('slow-call-screen');
  const factsBefore = (await page.getByTestId('scall-thiscall').innerText()).trim();
  expect(factsBefore.length, 'the call facts were read before the reload').toBeGreaterThan(0);

  await page.reload();
  await expect(screen, 'the reload comes back to the Slow call screen').toBeVisible();
  await expect(page.getByTestId('scall-thiscall')).toBeVisible();
  const factsAfter = (await page.getByTestId('scall-thiscall').innerText()).trim();
  expect(factsAfter, 'the reload shows the same call').toBe(factsBefore);

  // Back then shows the timeline.
  await page.getByTestId('scall-back-btn').click();
  await expect(screen).toHaveCount(0);
  await expect(page.locator('.tracing-paper'), 'Back after the reload shows the timeline').toBeVisible();
});

test('SCALL-04: the three lanes fit without an inner scroll at 1440x800 and 1100x760', async ({ page }, testInfo) => {
  mkdirSync(REPORT_DIR, { recursive: true });
  await openTimeline(page);
  for (const size of [{ width: 1440, height: 800 }, { width: 1100, height: 760 }]) {
    await page.setViewportSize({ width: size.width, height: size.height });
    // A fresh open at this size: go back to the timeline first if the screen is already up.
    if (await page.getByTestId('slow-call-screen').count() > 0) {
      await page.getByTestId('scall-back-btn').click();
      await expect(page.locator('.tracing-paper')).toBeVisible();
    }
    await openSlowCall(page);
    await expect(page.getByTestId('scall-lane-sql')).toBeVisible();

    const measured = await page.evaluate(() => {
      const lanes = Array.from(document.querySelectorAll('[data-testid^="scall-lane-"]')).map((el) => {
        const r = el.getBoundingClientRect();
        return { id: el.getAttribute('data-testid'), top: r.top, bottom: r.bottom, height: r.height };
      });
      const root = document.querySelector('[data-testid="slow-call-screen"]');
      const chain: { id: string; scrollHeight: number; clientHeight: number; overflowY: string }[] = [];
      let el = document.querySelector('[data-testid="scall-pin"]') as HTMLElement | null;
      while (el) {
        const cs = getComputedStyle(el);
        chain.push({ id: el.getAttribute('data-testid') || el.className || el.tagName, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, overflowY: cs.overflowY });
        if (el === root) break;
        el = el.parentElement;
      }
      return { lanes, chain, innerHeight: window.innerHeight };
    });

    // Every lane sits inside the viewport: nothing is cut off at the top or the bottom.
    for (const lane of measured.lanes) {
      expect(lane.top, `${size.width}x${size.height}: ${lane.id} starts at or below the top of the viewport`).toBeGreaterThanOrEqual(0);
      expect(lane.bottom, `${size.width}x${size.height}: ${lane.id} ends at or above the bottom of the viewport`).toBeLessThanOrEqual(measured.innerHeight);
    }
    expect(measured.lanes.length, 'all three lanes were measured').toBe(3);
    // The pinned timeline and every ancestor up to the screen has no inner scroll (or lets overflow show).
    for (const box of measured.chain) {
      const noInnerScroll = box.scrollHeight <= box.clientHeight + 1 || box.overflowY === 'visible';
      expect(noInnerScroll, `${size.width}x${size.height}: ${box.id} has no inner scroll (scrollHeight ${box.scrollHeight}, clientHeight ${box.clientHeight}, overflow-y ${box.overflowY})`).toBe(true);
    }

    // A screenshot per size, attached to the report and saved as a PNG so the path can be quoted.
    const shot = await page.screenshot();
    const png = join(REPORT_DIR, `scall-04-${size.width}x${size.height}.png`);
    writeFileSync(png, shot);
    await testInfo.attach(`scall-04-${size.width}x${size.height}`, { body: shot, contentType: 'image/png' });
    // The measurements are kept with the report so the lane heights and any inner scroll can be read back.
    await testInfo.attach(`scall-04-${size.width}x${size.height}-measure`, { body: Buffer.from(JSON.stringify(measured, null, 2)), contentType: 'application/json' });

    // After scrolling down, the pinned timeline is still in view (it sticks to the top).
    await page.evaluate(() => window.scrollTo(0, 400));
    const pinTop = await page.evaluate(() => (document.querySelector('[data-testid="scall-pin"]') as HTMLElement).getBoundingClientRect().top);
    expect(pinTop, `${size.width}x${size.height}: the pinned timeline stays in view after a scroll`).toBeGreaterThanOrEqual(0);
    expect(pinTop, `${size.width}x${size.height}: the pinned timeline is still within the viewport`).toBeLessThan(measured.innerHeight);
    await page.evaluate(() => window.scrollTo(0, 0));
  }
});

test('SCALL-05: the window drives the lanes and both lists', async ({ page }) => {
  const tracing = countTracing(page);
  await openTimeline(page);
  await openSlowCall(page);
  const hint = page.getByTestId('scall-nearby-hint');
  const line = page.getByTestId('scall-window-line');
  await expect(hint).toBeVisible();
  await expect(line).toBeVisible();

  // The screen opens at +/-15 min, so a preset only shows a change from a different window. Narrow to +/-5 first,
  // then press +/-15: the hint and the window line must both change, proving the one control drives the lists.
  await page.getByTestId('scall-preset-5').click();
  await expect(line).toContainText('5 min before, 5 min after');
  const hint5 = (await hint.innerText()).trim();
  const line5 = (await line.innerText()).trim();

  await page.getByTestId('scall-preset-15').click();
  await expect(line).toContainText('15 min before, 15 min after');
  const hint15 = (await hint.innerText()).trim();
  const line15 = (await line.innerText()).trim();
  expect(line15, 'the +/-15 preset changes the window line').not.toBe(line5);
  expect(hint15, 'the +/-15 preset changes the nearby-calls hint').not.toBe(hint5);

  // Later moves the window away from the call: the line says so and "Back to this call" appears, enabled.
  await page.getByTestId('scall-later').click();
  await expect(line).toContainText('moved away from this call');
  const toCall = page.getByTestId('scall-tocall');
  await expect(toCall).toBeVisible();
  await expect(toCall).toBeEnabled();
  const movedLine = (await line.innerText()).trim();
  await toCall.click();
  await expect(line, 'Back to this call returns the window to the call').toContainText('15 min before, 15 min after');
  expect((await line.innerText()).trim(), 'the window line is back from the moved wording').not.toBe(movedLine);

  // The All sources tab opens its list.
  await page.getByTestId('scall-tab-all').click();
  await expect(page.getByTestId('scall-all-heading')).toBeVisible();

  // Drag the strip box 60 px to the right. While dragging nothing is read; on release one visible-window read happens.
  const map = page.getByTestId('scall-map');
  await expect(map, 'the overview strip is shown so its box can be dragged').toBeVisible();
  const box = page.getByTestId('scall-map-box');
  await expect(box).toBeVisible();
  const rect = await box.boundingBox();
  if (!rect) throw new Error('the strip box has no box');
  const startX = rect.x + rect.width / 2;
  const startY = rect.y + rect.height / 2;
  await page.mouse.move(startX, startY);
  const beforeDrag = tracing();
  await page.mouse.down();
  await page.mouse.move(startX + 30, startY, { steps: 5 });
  await page.mouse.move(startX + 60, startY, { steps: 5 });
  expect(tracing(), 'no tracing read starts while the box is being dragged').toBe(beforeDrag);
  await page.mouse.up();
  await expect.poll(() => tracing() - beforeDrag, { message: 'one visible-window read happens after the drag', timeout: 5000 }).toBeGreaterThanOrEqual(1);
  expect(tracing() - beforeDrag, 'the drag ends with at most two visible-window reads').toBeLessThanOrEqual(2);
});

// The exact sentences from web/src/lib/drill/drillCopy.ts for the four states where Query Store is not read.
// They are quoted here so the test checks the real wording, never a guess.
const DB_SENTENCE = {
  off: 'Query Store is switched off on this database, so it saved no history. Switching it on needs the database owner (the script is in Setup). Unravis never switches it on itself.',
  denied: 'The saved database login is not allowed to read Query Store. It needs the VIEW DATABASE STATE permission. Ask the database owner, or add a login that has it.',
  notread: 'The database was not read for this time. Go back to the timeline and read it for a window that covers this call.',
  events: "Only SQL Server's own event files are available. They show moments such as deadlocks and long blocking, but not how busy the database was. A marker shows when something happened, not why."
} as const;
const DB_SHORT = {
  off: 'Not recorded: Query Store is off',
  denied: 'Not recorded: no permission',
  notread: 'not recorded'
} as const;
// The affirmative "Query Store is being read" wording (dbMsgLive). A not-read state must never show it.
const QS_ON_PHRASE = 'Read from Query Store';

test('SCALL-06: each pretend state shows its own sentence and never the words Query Store is on when it is not', async ({ page }, testInfo) => {
  await openTimeline(page);
  await openSlowCall(page);
  const state = page.getByTestId('scall-db-state');
  const sqlLane = page.getByTestId('scall-lane-sql');
  await expect(state).toBeVisible();
  await expect(page.getByTestId('scall-pretend')).toBeVisible();

  // The real sentence and whether the demo database lane actually draws a shape, before any pretend switch.
  const realText = (await state.innerText()).trim();
  const realState = await state.getAttribute('data-state');
  expect(realText.length, 'the real database sentence is filled in').toBeGreaterThan(0);
  const realHasShape = (await sqlLane.locator('.shape').count()) > 0;

  // Each pretend key must show its own sentence, different from the previous key's. The real state is the baseline
  // the "Real data" button returns to; it is not one of the pretend keys (the demo's real state may equal one of them).
  const keys = ['qs', 'qsfile', 'api', 'off', 'ro', 'late', 'denied', 'events', 'notread'] as const;
  const seen = new Set<string>();
  let previous: string | null = null;
  const evidence: { key: string; shapeCount: number; noteCount: number }[] = [];
  for (const key of keys) {
    await page.getByTestId(`scall-pretend-${key}`).click();
    await expect(state, `the ${key} state is selected`).toHaveAttribute('data-state', key);
    const text = (await state.innerText()).trim();
    expect(text.length, `the ${key} state has a sentence`).toBeGreaterThan(0);
    if (previous !== null) expect(text, `the ${key} sentence differs from the previous one`).not.toBe(previous);
    expect(seen.has(text), `the ${key} sentence is its own, not a repeat`).toBe(false);
    seen.add(text);
    previous = text;

    const shapeCount = await sqlLane.locator('.shape').count();
    const noteCount = await sqlLane.locator('.scall-note').count();
    evidence.push({ key, shapeCount, noteCount });
    if (key === 'off' || key === 'denied' || key === 'events' || key === 'notread') {
      // No "running at once" shape is drawn when the database was not read (or only events are available).
      expect(shapeCount, `the ${key} state draws no database shape`).toBe(0);
      if (key === 'events') {
        expect(noteCount, 'the events state has no "not recorded" note over the lane').toBe(0);
      } else {
        expect(noteCount, `the ${key} state shows the short note over the lane`).toBe(1);
        const note = (await sqlLane.locator('.scall-note').innerText()).trim();
        expect(note, `the ${key} note is the short wording`).toBe(DB_SHORT[key]);
      }
      // The exact sentence, and never the affirmative "Query Store is on" wording.
      expect(text, `the ${key} state shows its own sentence`).toBe(DB_SENTENCE[key]);
      expect(text, `the ${key} state never says Query Store is on`).not.toContain(QS_ON_PHRASE);
    } else {
      // blocks / apiseen: no "not recorded" note, and the shape is drawn when the demo lane has one.
      expect(noteCount, `the ${key} state has no "not recorded" note`).toBe(0);
      if (realHasShape) {
        expect(shapeCount, `the ${key} state draws the database shape (the demo lane has one)`).toBeGreaterThan(0);
      }
    }
  }

  // "Real data" restores the first sentence and the real state.
  await page.getByTestId('scall-pretend-real').click();
  await expect(state).toHaveAttribute('data-state', realState ?? '');
  expect((await state.innerText()).trim(), 'Real data restores the first sentence').toBe(realText);

  // Keep what the demo actually drew, so the shape discriminator can be read back from the report.
  await testInfo.attach('scall-06-database-states', {
    body: Buffer.from(JSON.stringify({ realState, realHasShape, realText, evidence }, null, 2)),
    contentType: 'application/json'
  });
});