// Blackbox actions that change the demo list or a case. The demo keeps its state for the whole
// file, so the order matters: the tests that need all nine rows come first, Forget and Remove last.
import { test, expect, type Page, type Locator } from '../support/fixtures';

const CASE = 'Checkout slowdown, 17 October';
const CLOSED_CASE = 'Night batch overrun, 10 October';

async function openCase(page: Page, title: string) {
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + title }).click();
  await expect(page.locator('#home-sources')).toBeVisible();
}

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

/** Set the four datetime fields of the Step 9 block (UTC, "YYYY-MM-DDTHH:MM") and read. */
async function readHost(region: Locator, incident: [string, string], reference?: [string, string]) {
  const details = region.locator('details', { hasText: 'Read for a case window' });
  if ((await details.getAttribute('open')) === null) await details.locator('summary').click();
  const inc = details.getByRole('group', { name: 'Incident window (UTC)' });
  await inc.getByRole('textbox', { name: 'Start' }).fill(incident[0]);
  await inc.getByRole('textbox', { name: 'End' }).fill(incident[1]);
  if (reference) {
    const ref = details.getByRole('group', { name: 'Reference window (UTC)' });
    await ref.getByRole('textbox', { name: 'Start' }).fill(reference[0]);
    await ref.getByRole('textbox', { name: 'End' }).fill(reference[1]);
  }
  await details.getByRole('button', { name: 'Read the host for these windows' }).click();
}

const WINDOW_PROBLEM = 'Each window needs an end after its start, and no window may be longer than 7 days.';

test('BB-11: Use in this case shows Stop using it, survives closing the drawer, and Stop puts it back', async ({ page }) => {
  await openCase(page, CASE);
  const { dialog, region } = await openStatus(page, 'web-a');
  await region.getByRole('button', { name: 'Use in this case' }).click();
  await expect(region.getByText('This case uses this Blackbox for Step 9.')).toBeVisible();
  await expect(region.getByRole('button', { name: 'Stop using it in this case' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  const again = await openStatus(page, 'web-a');
  await expect(again.region.getByText('This case uses this Blackbox for Step 9.')).toBeVisible();
  await again.region.getByRole('button', { name: 'Stop using it in this case' }).click();
  await expect(again.region.getByText('Removed from this case. The Blackbox and its recordings are as they were.')).toBeVisible();
  await expect(again.region.getByRole('button', { name: 'Use in this case' })).toBeEnabled();
});

test('BB-11a: a case reads one Blackbox, so another one cannot be added until the first is stopped', async ({ page }) => {
  await openCase(page, CASE);
  const a = await openStatus(page, 'web-a');
  await a.region.getByRole('button', { name: 'Use in this case' }).click();
  await expect(a.region.getByRole('button', { name: 'Stop using it in this case' })).toBeVisible();
  await a.region.getByRole('button', { name: 'Back to the list' }).click();
  await a.dialog.getByRole('button', { name: 'Open status: web-b' }).click();
  const b = a.dialog.getByRole('region', { name: 'web-b' });
  await expect(b.getByText('This case already uses web-a. A case reads one Blackbox. Stop using that one first.')).toBeVisible();
  await expect(b.getByRole('button', { name: 'Use in this case' })).toBeDisabled();
  // leave the demo as it was found
  await b.getByRole('button', { name: 'Back to the list' }).click();
  await a.dialog.getByRole('button', { name: 'Open status: web-a' }).click();
  await a.dialog.getByRole('button', { name: 'Stop using it in this case' }).click();
  await expect(a.dialog.getByRole('button', { name: 'Use in this case' })).toBeEnabled();
});

test('BB-11b: a double press on Use and on Stop ends in one clear state', async ({ page }) => {
  await openCase(page, CASE);
  const { region } = await openStatus(page, 'web-a');
  await region.getByRole('button', { name: 'Use in this case' }).dblclick();
  await expect(region.getByRole('button', { name: 'Stop using it in this case' })).toBeVisible();
  await expect(region.getByText('This case uses this Blackbox for Step 9.')).toHaveCount(1);
  await region.getByRole('button', { name: 'Stop using it in this case' }).dblclick();
  await expect(region.getByRole('button', { name: 'Use in this case' })).toBeEnabled();
});

test('BB-11c: on a closed case the button is off up front and the case is said to be closed', async ({ page }) => {
  await openCase(page, CLOSED_CASE);
  const closed = await openStatus(page, 'web-a');
  await expect(closed.region.getByRole('button', { name: 'Use in this case' })).toBeDisabled();
  await expect(closed.region.getByText('This case is closed. Its Blackbox use cannot be changed. Reopen the case to change it.')).toBeVisible();
});

test('BB-11d: with no case open the text says to open one first and the button is off', async ({ page }) => {
  const none = await openStatus(page, 'web-a');
  await expect(none.region.getByText('Open a case first. Then this Blackbox can be used in it.')).toBeVisible();
  await expect(none.region.getByRole('button', { name: 'Use in this case' })).toBeDisabled();
});

test('BB-12: the Step 9 host read names the first move and the busiest process, and never says Ready', async ({ page }) => {
  await openCase(page, CASE);
  const { region } = await openStatus(page, 'web-a');
  await expect(region.getByRole('heading', { name: 'Step 9: Host and network' })).toBeVisible();
  await readHost(region, ['2026-10-20T07:30', '2026-10-20T08:10'], ['2026-10-20T06:30', '2026-10-20T07:20']);
  await expect(region.getByText('The host first moved at 2026-10-20 07:40 UTC')).toBeVisible();
  await expect(region.getByText(/w3wp/)).toBeVisible();
  await expect(region.getByText('Partly ready').first()).toBeVisible();
  await expect(region.getByText('Ready', { exact: true }), 'step 9 is never Ready in release 1').toHaveCount(1); // only the status line above it
});

test('BB-12a: a window with its end before its start is refused in words, with no request sent and no old answer left beside it', async ({ page }) => {
  await openCase(page, CASE);
  const { region } = await openStatus(page, 'web-a');
  await readHost(region, ['2026-10-20T07:30', '2026-10-20T08:10']);
  await expect(region.getByText('The host first moved at 2026-10-20 07:40 UTC')).toBeVisible();
  const posts: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().includes('host-reading')) posts.push(r.url());
  });
  await readHost(region, ['2026-10-20T08:10', '2026-10-20T07:30']);
  await expect(region.getByText(WINDOW_PROBLEM)).toBeVisible();
  await expect(region.getByText('The host first moved at')).toHaveCount(0);
  expect(posts).toEqual([]);
});

test('BB-12b: empty windows and a window longer than seven days are refused the same way', async ({ page }) => {
  await openCase(page, CASE);
  const { region } = await openStatus(page, 'web-a');
  await readHost(region, ['2026-10-20T07:30', '2026-10-20T08:10']);
  await expect(region.getByText('The host first moved at 2026-10-20 07:40 UTC')).toBeVisible();
  const details = region.locator('details', { hasText: 'Read for a case window' });
  await readHost(region, ['2026-10-01T00:00', '2026-10-20T08:10']);
  await expect(region.getByText(WINDOW_PROBLEM)).toBeVisible();
  const inc = details.getByRole('group', { name: 'Incident window (UTC)' });
  await inc.getByRole('textbox', { name: 'Start' }).fill('');
  await details.getByRole('button', { name: 'Read the host for these windows' }).click();
  await expect(region.getByText(WINDOW_PROBLEM)).toBeVisible();
});

test('BB-12c: a window over the 90 second gap names the gap and only warns', async ({ page }) => {
  await openCase(page, CASE);
  const { region } = await openStatus(page, 'web-a');
  await readHost(region, ['2026-10-20T05:30', '2026-10-20T06:30']);
  await expect(region.getByText('Some minutes were not recorded')).toBeVisible();
  await expect(region.getByText(/gaps of 2 minutes or less/)).toBeVisible();
  await expect(region.getByText('Missing minutes are never counted as zero', { exact: false }).first()).toBeVisible();
  await expect(region.getByText('Partly ready').first()).toBeVisible();
});

test('BB-13a: the 3 minute gap on web-b in the incident window is a long gap and the step cannot be tested', async ({ page }) => {
  await openCase(page, CASE);
  const { region } = await openStatus(page, 'web-b');
  await readHost(region, ['2026-10-20T06:30', '2026-10-20T07:30'], ['2026-10-20T05:00', '2026-10-20T06:00']);
  await expect(region.getByText('Cannot test yet').first()).toBeVisible();
  await expect(region.getByText(/gaps longer than 2 minutes/)).toBeVisible();
});

test('BB-20a: no other server view has a write button, and the Blackbox list has no case-less write either', async ({ page }) => {
  const dialog = await openDrawer(page);
  const writes = /^(Start|Stop|Apply settings|Snapshot now|Remove|Install.*|Delete.*|Clean.*)$/;
  for (const name of ['web-old', 'web-g', 'old-box']) {
    await dialog.getByRole('button', { name: 'Open status: ' + name }).click();
    const region = dialog.getByRole('region', { name });
    await expect(region).toBeVisible();
    const buttons = await region.getByRole('button').evaluateAll((els) => els.map((e) => (e.textContent ?? '').trim()));
    for (const b of buttons) expect(b, name + ' offers "' + b + '"').not.toMatch(writes);
    await region.getByRole('button', { name: 'Back to the list' }).click();
  }
});

test('BB-10: Cancel keeps web-d; Forget this place removes only it, says nothing on the server was touched, and eight rows remain', async ({ page }) => {
  const { dialog, region } = await openStatus(page, 'web-d');
  await region.getByRole('button', { name: 'Forget this place' }).click();
  await expect(dialog.getByRole('heading', { name: 'Remove web-d' })).toBeVisible();
  await expect(dialog.getByText('Forget removes the saved place and its sealed read key from this PC. The Blackbox and its recordings are not touched.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog.getByRole('region', { name: 'web-d' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(9);

  await dialog.getByRole('button', { name: 'Open status: web-d' }).click();
  await dialog.getByRole('button', { name: 'Forget this place' }).click();
  await dialog.getByRole('button', { name: 'Forget this place' }).click();
  await expect(dialog.getByText('Removed from the list. The Blackbox and its recordings were not touched.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(8);
  await expect(dialog.getByRole('button', { name: 'Open status: web-d' })).toHaveCount(0);
});

test('BB-10a: a double press on the second Forget removes the row once without an error', async ({ page }) => {
  const { dialog, region } = await openStatus(page, 'web-f');
  await region.getByRole('button', { name: 'Forget this place' }).click();
  const deletes: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'DELETE') deletes.push(r.url());
  });
  await dialog.getByRole('button', { name: 'Forget this place' }).dblclick();
  await expect(dialog.getByText('Removed from the list. The Blackbox and its recordings were not touched.')).toBeVisible();
  expect(deletes).toHaveLength(1);
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await expect(dialog.getByRole('button', { name: /^Open status: / })).toHaveCount(7);
});

test('BB-18a: Remove on this PC defaults to keeping the recordings, and Esc or Cancel remove nothing', async ({ page }) => {
  const { dialog, region } = await openStatus(page, 'web-a');
  const posts: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && r.url().includes('/remove')) posts.push(r.url());
  });
  await region.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(dialog.getByRole('heading', { name: 'Remove web-a' })).toBeVisible();
  await expect(dialog.getByRole('radio', { name: /Keep the recordings/ })).toBeChecked();
  await expect(dialog.getByRole('radio', { name: /Delete the recordings too/ })).not.toBeChecked();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog.getByRole('region', { name: 'web-a' })).toBeVisible();
  await dialog.getByRole('region', { name: 'web-a' }).getByRole('button', { name: 'Remove', exact: true }).click();
  await page.keyboard.press('Escape');
  const again = await openDrawer(page);
  await expect(again.getByRole('button', { name: 'Open status: web-a' })).toBeVisible();
  expect(posts).toEqual([]);
});

test('BB-18: deleting the recordings needs the exact name, case and all, and removing frees the this-PC row', async ({ page }) => {
  const { dialog, region } = await openStatus(page, 'web-a');
  await region.getByRole('button', { name: 'Remove', exact: true }).click();
  await dialog.getByRole('radio', { name: /Delete the recordings too/ }).check();
  await expect(dialog.getByText('Type the name web-a to turn the button on')).toBeVisible();
  const confirm = dialog.getByRole('button', { name: 'Remove Blackbox' });
  const name = dialog.getByLabel('Name', { exact: true });
  await expect(confirm).toBeDisabled();
  for (const wrong of ['web-', 'WEB-A', 'web-A', 'web-aa', 'web-b', ' web-a']) {
    await name.fill(wrong);
    await expect(confirm, '"' + wrong + '"').toBeDisabled();
  }
  await name.fill('web-a');
  await expect(confirm).toBeEnabled();
  await name.fill('web-x');
  await expect(confirm).toBeDisabled();
  await name.fill('web-a');
  await confirm.click();
  await expect(dialog.getByText(/The service was removed and \d+ files? (was|were) deleted\./)).toBeVisible();
  await dialog.getByRole('button', { name: 'Back to the list' }).click();
  await expect(dialog.getByRole('button', { name: 'Open status: web-a' })).toHaveCount(0);
});
