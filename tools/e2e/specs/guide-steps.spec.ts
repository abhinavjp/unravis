// Guided analysis, step by step (steps 0 to 10) on the demo day. The guided state lives in the
// page only, so nothing here changes the case on the server and the tests can run in any order.
import { test, expect, type Page } from '../support/fixtures';
import { INC, REF, openGuided, setWindows, runSteps, gotoStep, stepRail } from '../support/ui';

const NO_CALLS_INC: [string, string] = ['2026-10-17T10:30', '2026-10-17T11:00'];
const NO_CALLS_REF: [string, string] = ['2026-10-17T09:30', '2026-10-17T10:30'];

/** 320 px wide, then wait until the layout has followed. */
async function narrow(page: Page) {
  await page.setViewportSize({ width: 320, height: 700 });
  await expect.poll(() => page.evaluate(() => window.innerWidth)).toBe(320);
  await page.evaluate(() => new Promise<void>((r) => requestAnimationFrame(() => requestAnimationFrame(() => r()))));
}

async function addChange(page: Page, when: string, what: string) {
  await page.getByRole('button', { name: '+ Add a change' }).click();
  await page.getByLabel('When (UTC)').last().fill(when);
  await page.getByLabel('What changed').last().fill(what);
}

test('GUIDE-01: the frame has the intro, eleven waiting steps, five tabs and one primary button', async ({ page }) => {
  await openGuided(page);
  await expect(page.getByRole('navigation', { name: 'Guided analysis steps' })).toBeVisible();
  await expect(page.getByText(/Eleven steps, cheapest first\. Each step reads the data you already have and says what it shows and what it could not test\./)).toBeVisible();
  const rail = stepRail(page);
  await expect(rail.getByRole('button')).toHaveCount(11);
  for (let n = 0; n <= 10; n++) {
    await expect(rail.getByRole('button', { name: new RegExp('^' + n + ' ') })).toHaveAccessibleName(/Waiting$/);
  }
  // A Waiting sticker is hatched, never a flat green.
  const bg = await rail.getByText('Waiting', { exact: true }).first().evaluate((el) => getComputedStyle(el).backgroundImage);
  expect(bg).toContain('repeating-linear-gradient');
  for (const tab of ['Steps', 'Checklist', 'After the fix', 'Nearby activity', 'Export']) {
    await expect(page.getByRole('tab', { name: tab, exact: true })).toBeVisible();
  }
  await expect(page.locator('button.primary-button')).toHaveCount(1);
  await expect(page.getByRole('button', { name: 'Run the steps' })).toHaveClass(/primary-button/);
  await expect(page.getByText('Fix the windows to run the steps. The incident window needs a start and an end, in UTC.')).toBeVisible();
  await setWindows(page);
  await expect(page.getByText('Nothing has run yet. Choose the windows in Step 0, then run the steps.')).toBeVisible();
});

test('GUIDE-01a: at 320 px the rail and the tabs stay usable', async ({ page }) => {
  await openGuided(page);
  await narrow(page);
  await expect(stepRail(page)).toBeVisible();
  await page.getByRole('tab', { name: 'Checklist' }).click();
  await expect(page.getByRole('heading', { name: 'Checklist' })).toBeVisible();
  await page.getByRole('tab', { name: 'Export' }).click();
  await expect(page.getByRole('button', { name: 'Download report (HTML)' })).toBeVisible();
  await page.getByRole('tab', { name: 'Steps' }).click();
  await stepRail(page).getByRole('button', { name: /^2 / }).click();
  await expect(page.locator('#step-title-2')).toBeVisible();
});

test('GUIDE-01b: at 320 px the page does not scroll sideways', async ({ page }) => {
  await openGuided(page);
  await narrow(page);
  await expect(stepRail(page)).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(1);
});

test('GUIDE-03: focus, pool limit and a listed change reach the steps that use them', async ({ page }) => {
  await openGuided(page);
  await setWindows(page);
  await page.getByLabel('Focus on one API (optional)').fill('Payroll.Run');
  await page.getByLabel('SqlClient pool limit (optional)').fill('10');
  await addChange(page, '2026-10-20T08:40', 'Release 4.2');
  await page.getByLabel('I have listed every change I know of').check();
  await page.getByRole('button', { name: 'Run the steps' }).click();
  await expect(stepRail(page).getByRole('button', { name: /Waiting|Running/ })).toHaveCount(0);
  const s0 = await gotoStep(page, 0);
  await expect(s0.getByText(/Scope: 500 calls\./)).toBeVisible();
  const s2 = await gotoStep(page, 2);
  await expect(s2.getByRole('row', { name: /Employees\.List/ })).toBeVisible();
  const s4 = await gotoStep(page, 4);
  await expect(s4.getByText(/against a limit of 10\./)).toBeVisible();
  const s5 = await gotoStep(page, 5);
  await expect(s5.getByRole('row', { name: /Release 4\.2/ })).toContainText('just before the start');
  await expect(s5.getByRole('row', { name: /Release 4\.2/ })).toContainText('20 Oct 08:40 UTC');
});

test('GUIDE-03a: the three quick picks set a window and a normal period of the same length just before it', async ({ page }) => {
  await openGuided(page);
  const incident = page.getByRole('group', { name: 'Incident window' });
  const reference = page.getByRole('group', { name: 'Reference window (normal)' });
  const ms = async (g: typeof incident, label: string) => new Date((await g.getByLabel(label).inputValue()) + 'Z').getTime();
  for (const pick of ['Last hour', 'Today', 'Yesterday']) {
    await page.getByRole('button', { name: pick, exact: true }).click();
    await expect(incident.getByLabel('From')).not.toHaveValue('');
    const [iFrom, iTo, rFrom, rTo] = [await ms(incident, 'From'), await ms(incident, 'To'), await ms(reference, 'From'), await ms(reference, 'To')];
    expect(iTo).toBeGreaterThan(iFrom);
    expect(rTo).toBe(iFrom);
    expect(rTo - rFrom).toBe(iTo - iFrom);
  }
});

test('GUIDE-03b: an empty change, a 51st change and a bad pool limit are refused', async ({ page }) => {
  await openGuided(page);
  await setWindows(page);
  const run = page.getByRole('button', { name: 'Run the steps' });
  await page.getByRole('button', { name: '+ Add a change' }).click();
  await expect(run).toBeDisabled();
  await expect(page.getByText('Each change needs a time, in UTC.', { exact: true })).toBeVisible();
  await expect(page.getByText('Each change needs a name.', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Remove' }).click();
  await expect(run).toBeEnabled();
  const pool = page.getByLabel('SqlClient pool limit (optional)');
  for (const bad of ['-5', '0', 'abc']) {
    await pool.fill(bad);
    await expect(run).toBeDisabled();
    await expect(page.getByText('The pool limit is a whole number from 1 to 100000, or empty.').first()).toBeVisible();
  }
  await pool.fill('');
  await expect(run).toBeEnabled();
  for (let i = 0; i < 50; i++) await page.getByRole('button', { name: '+ Add a change' }).click();
  await expect(page.getByLabel('When (UTC)')).toHaveCount(50);
  await expect(page.getByRole('button', { name: '+ Add a change' })).toBeDisabled();
  await expect(page.getByText('The change list holds at most 50 changes.')).toBeVisible();
});

test('GUIDE-05: step 1 is partly ready, labelled in a nutshell, and lists the sources with their state', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const s1 = await gotoStep(page, 1);
  await expect(s1.getByText('Partly ready', { exact: true }).first()).toBeVisible();
  await expect(s1.getByText(/in a nutshell/i)).toBeVisible();
  await expect(s1.getByText('Before reading any number we check that the data can be trusted: clocks agree, no call is cut off, and nothing is missing.')).toBeVisible();
  await expect(s1.getByText('Calls in the incident window')).toBeVisible();
  await expect(s1.getByText('Calls in the reference window')).toBeVisible();
  await expect(s1.getByText('No minute is missing.').first()).toBeVisible();
  const sources = s1.getByRole('table');
  await expect(sources.getByRole('columnheader', { name: 'Source' })).toBeVisible();
  await expect(sources.getByRole('columnheader', { name: 'State' })).toBeVisible();
  await expect(sources.getByRole('row', { name: /^audit\s+Ready/ })).toBeVisible();
  for (const name of ['IIS logs', 'SQL Server data', 'MongoDB data', 'host counters', 'change list']) {
    await expect(sources.getByRole('row', { name: new RegExp('^' + name + '\\s+Cannot test yet') })).toBeVisible();
  }
  await expect(s1.getByText(/This run read 1,514 audit calls and 0 IIS rows\./)).toBeVisible();
  await expect(s1.getByText(/14 not completed/).first()).toBeVisible();
});

test('GUIDE-05a: jumping to step 1 before a run says to run first', async ({ page }) => {
  await openGuided(page);
  await setWindows(page);
  await gotoStep(page, 1);
  await expect(page.getByText('This step shows the last run. Run the steps first to fill it in.')).toBeVisible();
});

test('GUIDE-05b: a window with no calls ends in Not enough data, and Change the window goes back to step 0', async ({ page }) => {
  await openGuided(page);
  await runSteps(page, NO_CALLS_INC, NO_CALLS_REF);
  const s1 = await gotoStep(page, 1);
  await expect(s1.getByText('Not enough data to continue')).toBeVisible();
  await s1.getByRole('button', { name: 'Change the window' }).click();
  await expect(page.locator('#step-title-0')).toBeVisible();
});

test('GUIDE-06: step 2 puts Payroll.Run first with both marks passed and states the rule', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const s2 = await gotoStep(page, 2);
  await expect(s2.getByText('By API. 1 of 3 moved.')).toBeVisible();
  const rows = s2.getByRole('table').first().getByRole('row');
  await expect(rows.nth(1)).toContainText('Payroll.Run');
  await expect(rows.nth(1)).toContainText('260 ms');
  await expect(rows.nth(1)).toContainText('52 ms');
  await expect(rows.nth(1)).toContainText('Slower');
  await expect(s2.getByRole('row', { name: /Employees\.List/ })).toContainText('No change');
  await expect(s2.getByRole('row', { name: /Reports\.Generate/ })).toContainText('No change');
  await expect(s2.getByText('The rule: a change counts when it is at least 30 % and at least 100 ms.')).toBeVisible();
  await expect(s2.getByText(/Calls sent again by the same client within seconds: 375 in the incident, 375 in the reference\./)).toBeVisible();
  await expect(s2.getByText('The change starts at 20 Oct 2026 08:45 UTC.')).toBeVisible();
});

test('GUIDE-06a: two windows before the slowdown say that nothing moved', async ({ page }) => {
  await openGuided(page);
  await runSteps(page, ['2026-10-20T08:38', '2026-10-20T08:45'], ['2026-10-20T08:30', '2026-10-20T08:38']);
  const s2 = await gotoStep(page, 2);
  await expect(s2.getByText('Nothing moved: no API changed by 30 % and 100 ms. The slow end looks the same as normal, as far as these calls show.')).toBeVisible();
});

test('GUIDE-07: step 3 puts the extra time in C, leaves O untested and not drawn, and lists the slowest calls', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const s3 = await gotoStep(page, 3);
  const parts = s3.getByRole('table').first();
  for (const h of ['Part', 'Normal', 'Incident', 'Share of the extra time']) {
    await expect(parts.getByRole('columnheader', { name: h })).toBeVisible();
  }
  await expect(parts.getByRole('row', { name: /Action time \(C\)/ })).toContainText('73 %');
  await expect(parts.getByRole('row', { name: /Audit set-up \(A\)/ })).toContainText('18 %');
  await expect(parts.getByRole('row', { name: /Payload record \(P\)/ })).toContainText('9 %');
  const o = parts.getByRole('row', { name: /Outside time \(O\)/ });
  await expect(o).toContainText('Cannot test yet');
  await expect(o).not.toContainText(/\b0 ms\b/);
  await expect(s3.getByText('Bars of the average time of a call, on one scale.')).toBeVisible();
  await expect(s3.getByText(/Not drawn: parts that could not be tested\./)).toBeVisible();
  await expect(s3.getByText('IIS logs show the time before a call reaches the app (outside time, O).')).toBeVisible();
  await expect(s3.getByRole('button', { name: 'Add the IIS logs' })).toBeVisible();
  await s3.getByRole('button', { name: 'Show the slowest calls' }).click();
  const slow = s3.getByRole('table').nth(1);
  await expect(slow.getByRole('columnheader')).toHaveText(['API', 'Started (UTC)', 'Time']);
  await expect(slow.getByRole('row')).toHaveCount(11);
  await expect(slow.getByRole('row').nth(1)).toContainText('Payroll.Run');
});

test('GUIDE-08: step 4 shows the four measures and the planning load, and a pool limit gives a sentence', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  let s4 = await gotoStep(page, 4);
  const measures = s4.getByRole('table').first();
  for (const m of ['Calls arriving per second', 'Time a call spends in the system', 'Calls in flight (average)', 'Calls in flight (highest minute)']) {
    await expect(measures.getByRole('row', { name: new RegExp(m.replace(/[()]/g, '\\$&')) })).toBeVisible();
  }
  await expect(s4.getByText('Planning load is 5.0 requests in flight at 40 requests per second.')).toBeVisible();
  await expect(s4.getByText('No pool limit was entered, so load cannot be set against a limit.').first()).toBeVisible();
  await expect(s4.getByText(/against a limit of/)).toHaveCount(0);
  await gotoStep(page, 0);
  await page.getByLabel('SqlClient pool limit (optional)').fill('10');
  await page.getByRole('button', { name: /^Run again|^Run the steps/ }).click();
  await expect(stepRail(page).getByRole('button', { name: /Out of date|Running/ })).toHaveCount(0);
  s4 = await gotoStep(page, 4);
  await expect(s4.getByText('Planned load is 5.0 requests in flight against a limit of 10.')).toBeVisible();
  await expect(s4.getByText('No pool limit was entered, so load cannot be set against a limit.')).toHaveCount(0);
});

test('GUIDE-09: step 5 reads Cannot test yet with no list, a deliberate No change listed when ticked, and both changes as typed', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  let s5 = await gotoStep(page, 5);
  await expect(s5.getByText('Cannot test yet', { exact: true }).first()).toBeVisible();
  await expect(s5.getByText('No change listed. Add the releases or changes you know of in Step 0.')).toBeVisible();
  await gotoStep(page, 0);
  await page.getByLabel('I have listed every change I know of').check();
  await page.getByRole('button', { name: /^Run again|^Run the steps/ }).click();
  await expect(stepRail(page).getByRole('button', { name: /Out of date|Running/ })).toHaveCount(0);
  s5 = await gotoStep(page, 5);
  await expect(s5.getByText('Ready', { exact: true }).first()).toBeVisible();
  await expect(s5.getByText('No change listed.', { exact: true }).first()).toBeVisible();
  await gotoStep(page, 0);
  await addChange(page, '2026-10-20T08:40', 'Config push');
  await addChange(page, '2026-10-20T06:00', 'Old release');
  await page.getByRole('button', { name: /^Run again|^Run the steps/ }).click();
  await expect(stepRail(page).getByRole('button', { name: /Out of date|Running/ })).toHaveCount(0);
  s5 = await gotoStep(page, 5);
  const table = s5.getByRole('table');
  await expect(table.getByRole('row', { name: /Config push/ })).toContainText('just before the start');
  await expect(table.getByRole('row', { name: /Config push/ })).toContainText('20 Oct 08:40 UTC');
  await expect(table.getByRole('row', { name: /Old release/ })).toContainText('long before the start');
  await expect(table.getByRole('row', { name: /Old release/ })).toContainText('20 Oct 06:00 UTC');
  await expect(s5.getByText('The release list is typed by you, so a release that was not entered cannot be ruled out.').first()).toBeVisible();
});

test('GUIDE-09a: a change entered after the run marks the steps out of date', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  await gotoStep(page, 0);
  await addChange(page, '2026-10-20T09:30', 'Late release');
  await expect(stepRail(page).getByRole('button', { name: /Out of date, rerun/ })).toHaveCount(10);
});

test('GUIDE-10: steps 6 to 9 name what could not be tested, and no source step turns green', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const rail = stepRail(page);
  await expect(rail.getByRole('button', { name: /^6 .*Partly ready/ })).toBeVisible();
  for (const n of [7, 8, 9]) await expect(rail.getByRole('button', { name: new RegExp('^' + n + ' .*Cannot test yet') })).toBeVisible();
  const s6 = await gotoStep(page, 6);
  await expect(s6.getByText(/S4, S6 and S7 need data the audit does not hold/)).toBeVisible();
  await expect(s6.getByText('Tested from the audit and IIS: S1, S2, S3, S5. Held for other sources: S4, S6, S7.')).toBeVisible();
  await expect(s6.getByText('Ruled out by the data we have')).toHaveCount(1);
  const s7 = await gotoStep(page, 7);
  await expect(s7.getByText('No SQL Server data is imported for this case.').first()).toBeVisible();
  await expect(s7.getByText('Suspects with a reading: 0 of 15.')).toBeVisible();
  await expect(s7.getByRole('button', { name: 'Add a source' })).toBeVisible();
  await expect(s7.getByText('Ruled out by the data we have')).toHaveCount(0);
  await expect(s7.getByText('Not sure', { exact: true })).toHaveCount(15);
  const s8 = await gotoStep(page, 8);
  await expect(s8.getByText('Suspects with a reading: 0 of 8.')).toBeVisible();
  await expect(s8.getByRole('button', { name: 'Add a source' })).toBeVisible();
  await expect(s8.getByText('Ruled out by the data we have')).toHaveCount(0);
  await expect(s8.getByText('Not sure', { exact: true })).toHaveCount(8);
  const s9 = await gotoStep(page, 9);
  await expect(s9.getByText('Suspects with a reading: 0 of 6.')).toBeVisible();
  await expect(s9.getByRole('button', { name: 'Place a Blackbox' })).toBeVisible();
  await expect(s9.getByText('Ruled out by the data we have')).toHaveCount(0);
  await expect(s9.getByText('Not sure', { exact: true })).toHaveCount(6);
});

test('GUIDE-10a: the Add a source hand-off opens Import files and closing it returns to the same step', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const s7 = await gotoStep(page, 7);
  await s7.getByRole('button', { name: 'Add a source' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText('Import files').first()).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('#step-title-7')).toBeVisible();
});

test('GUIDE-11: step 10 names no culprit, explains the colours and keeps confidence at None with no dated tier', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const s10 = await gotoStep(page, 10);
  await expect(s10.getByText('Unravis does not name a culprit.')).toBeVisible();
  await expect(s10.getByText('Not enough data to date any tier. Add the sources listed under How sure are we.')).toBeVisible();
  await expect(s10.getByText('Red: most likely suspect')).toBeVisible();
  await expect(s10.getByText('Orange: likely suspect')).toBeVisible();
  await expect(s10.getByText('Green: ruled out by the data we have')).toBeVisible();
  await expect(s10.getByText('Grey: not sure. It is not a pass.')).toBeVisible();
  await expect(s10.getByText(/Confidence: None\./)).toBeVisible();
  await expect(s10.getByText('A suspect can be listed as Likely while no tier is dated. The data fits it, but Confidence stays None and no origin is named.')).toBeVisible();
  await expect(s10.getByText('Could not be tested (grey, never a pass)')).toBeVisible();
  await expect(s10.getByText('Set aside: 3 green suspects')).toBeVisible();
  await expect(s10.getByRole('row', { name: /^Sources used\s+audit$/ })).toBeVisible();
  await expect(s10.getByRole('row', { name: /^Sources missing\s+IIS logs, SQL Server data, MongoDB data, host counters, change list$/ })).toBeVisible();
  for (const id of ['origin-moved', 'origin-colours', 'origin-suspects', 'origin-cheapest', 'origin-sure']) {
    await expect(page.locator('#' + id)).toHaveCount(1);
  }
});

test('GUIDE-11a: the one likely suspect card shows why, the evidence, what would overturn it and the cheapest check', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const s10 = await gotoStep(page, 10);
  const card = s10.locator('article, li, div').filter({ hasText: /^Likely suspect\s*L2\. Mix shift/ }).last();
  await expect(card).toContainText('Consistent');
  await expect(card).toContainText('Observed');
  await expect(card).toContainText('Why: Every reading it needs is present.');
  await expect(card).toContainText('Evidence');
  await expect(card).toContainText('What would overturn it: The rise is spread across routes.');
  await expect(card).toContainText('Cheapest check: Group the slow calls by route, client and body size.');
});

test('GUIDE-11b: a window with no calls gives confidence None and no origin', async ({ page }) => {
  await openGuided(page);
  await runSteps(page, NO_CALLS_INC, NO_CALLS_REF);
  const s10 = await gotoStep(page, 10);
  await expect(s10.getByText(/Confidence: None\./)).toBeVisible();
  await expect(s10.getByText(/Not enough data to date any tier/).first()).toBeVisible();
});

test('GUIDE-11c: at 320 px the colour legend keeps its words', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  await narrow(page);
  const s10 = await gotoStep(page, 10);
  for (const t of ['Red: most likely suspect', 'Orange: likely suspect', 'Green: ruled out by the data we have', 'Grey: not sure. It is not a pass.']) {
    await expect(s10.getByText(t)).toBeVisible();
  }
});

test('GUIDE-16: the After the fix tab refuses a missing day, says no data for an empty day and refuses an overlapping one', async ({ page }) => {
  await openGuided(page);
  await page.getByRole('tab', { name: 'After the fix' }).click();
  await expect(page.getByText('Run the steps first. The comparison needs the incident day.')).toBeVisible();
  await page.getByRole('tab', { name: 'Steps' }).click();
  await runSteps(page);
  await page.getByRole('tab', { name: 'After the fix' }).click();
  await expect(page.getByText('Pick hours on a day after the fix. Unravis compares them with the incident day.')).toBeVisible();
  await page.getByRole('button', { name: 'Compare', exact: true }).click();
  await expect(page.getByText('Pick the later day first.')).toBeVisible();
  await page.getByLabel('The later day (UTC)').fill('2026-10-21');
  await page.getByRole('button', { name: 'Compare', exact: true }).click();
  await expect(page.getByText('No data for that day').first()).toBeVisible();
  await expect(page.getByText('Unravis found no calls on that day, in those hours. Nothing is compared against zero.')).toBeVisible();
  await expect(page.getByText(/Fixed/)).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Record result' })).toHaveCount(0);
  await page.getByLabel('The later day (UTC)').fill('2026-10-20');
  await page.getByRole('button', { name: 'Compare', exact: true }).click();
  await expect(page.getByText('The later day must come after the incident day. Pick a later date.')).toBeVisible();
});

test('GUIDE-17: nearby activity shows the audit lane, three untested lanes and a refused span', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  await page.getByRole('tab', { name: 'Nearby activity' }).click();
  await page.getByLabel('Around (UTC)').fill('2026-10-20T08:52');
  await page.getByLabel('Minutes before').fill('10');
  await page.getByLabel('Minutes after').fill('10');
  await page.getByRole('button', { name: 'Show activity' }).click();
  await expect(page.getByText('Bar colours: red striped over 2 seconds, orange 1 to 2 seconds, green under 1 second. These are not the suspect colours.')).toBeVisible();
  await expect(page.getByText('Cannot test yet: this case has no source of this kind.')).toHaveCount(3);
  await expect(page.getByText('Nothing was recorded in this window.')).toHaveCount(0);
  await expect(page.getByText('Showing the first 100 calls of the window. Narrow the window to see the rest.')).toBeVisible();
  const show = page.getByRole('button', { name: 'Show activity' });
  for (const [before, after] of [['121', '10'], ['-5', '10'], ['0', '0']]) {
    await page.getByLabel('Minutes before').fill(before);
    await page.getByLabel('Minutes after').fill(after);
    await expect(show).toBeDisabled();
    await expect(page.getByText('Pick a moment in UTC and a span of 0 to 120 minutes.')).toBeVisible();
  }
  await page.getByLabel('Around (UTC)').fill('');
  await page.getByLabel('Minutes before').fill('5');
  await expect(show).toBeDisabled();
});

test('GUIDE-17a: a moment with no calls says nothing was recorded', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  await page.getByRole('tab', { name: 'Nearby activity' }).click();
  await page.getByLabel('Around (UTC)').fill('2026-10-21T08:52');
  await page.getByRole('button', { name: 'Show activity' }).click();
  await expect(page.getByText('Nothing was recorded in this window.')).toBeVisible();
});

test('GUIDE-18: changing a window marks steps 1 to 10 out of date, keeps the old numbers, and Run again clears it', async ({ page }) => {
  await openGuided(page);
  await runSteps(page);
  const reference = page.getByRole('group', { name: 'Reference window (normal)' });
  await reference.getByLabel('From').fill('2026-10-20T08:25');
  const rail = stepRail(page);
  await expect(rail.getByRole('button', { name: /Out of date, rerun/ })).toHaveCount(10);
  await expect(page.getByText('The windows, sources or lists changed after this ran. The numbers below are from the earlier run.')).toBeVisible();
  const s2 = await gotoStep(page, 2);
  await expect(s2.getByRole('row', { name: /Payroll\.Run/ })).toContainText('260 ms');
  await page.getByRole('button', { name: 'Run again' }).click();
  await expect(rail.getByRole('button', { name: /Out of date|Running|Waiting/ })).toHaveCount(0);
  for (let n = 0; n <= 10; n++) {
    await expect(rail.getByRole('button', { name: new RegExp('^' + n + ' ') })).toHaveAccessibleName(/Ready|Partly ready|Cannot test yet|Failed/);
  }
});
