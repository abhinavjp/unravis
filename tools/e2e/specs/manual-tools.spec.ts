// Manual tools: the thirteen cards, the tool frame, the two filters, notes and saved views.
// This file has its own demo. The read-only tests come first; the ones that add a note or a saved view
// remove it again before they finish so the next test starts from an empty list.
import { test, expect, type Page } from '../support/fixtures';
import { INC, REF, openGuided, setWindows, stepRail } from '../support/ui';

const CLOSED_CASE = 'Night batch overrun, 10 October';
const CARDS: Array<[string, string]> = [
  ['Response time over time', 'Waiting'],
  ['Slowest APIs', 'Waiting'],
  ['Slowest calls', 'Ready'],
  ['Time split A C P O', 'Waiting'],
  ['Calls in flight', 'Waiting'],
  ['Nearby activity', 'Ready'],
  ['Changes timeline', 'Waiting'],
  ['SQL blocking and waits', 'Cannot test yet'],
  ['MongoDB health', 'Cannot test yet'],
  ['Host CPU, memory and disk', 'Cannot test yet'],
  ['SQL host disk, memory and network', 'Cannot test yet'],
  ['After the fix', 'Waiting'],
  ['Suspect summary', 'Waiting']
];

async function toManual(page: Page, caseTitle?: string) {
  await openGuided(page, caseTitle);
  await page.getByText('Case actions').click();
  await page.getByRole('button', { name: 'Switch to Manual tools' }).click();
  await expect(page.getByText('You are using Manual tools.')).toBeVisible();
  await expect(page.locator('#tools-title')).toHaveText('Manual tools');
}

async function toGuidedMode(page: Page) {
  await page.getByText('Case actions').click();
  await page.getByRole('button', { name: 'Switch to Guided analysis' }).click();
  await expect(page.getByText('You are using Guided analysis.')).toBeVisible();
}

async function openTool(page: Page, name: string) {
  await page.getByRole('button', { name: 'Open: ' + name, exact: true }).click();
  await expect(page.locator('#tools-title')).toHaveText(name);
}

async function runInTool(page: Page) {
  await setWindows(page, INC, REF);
  await page.getByRole('button', { name: 'Run the steps' }).click();
  await expect(page.getByRole('button', { name: 'Run again' })).toBeVisible();
}

const card = (page: Page, name: string) => page.locator('li.card').filter({ has: page.getByRole('heading', { name, exact: true }) });

test('MAN-01: Manual tools shows the lead and the thirteen cards in order, each with a sticker and one Open button', async ({ page }) => {
  await toManual(page);
  await expect(page.getByText('The same facts as the steps, one tool at a time, in any order.')).toBeVisible();
  await expect(page.locator('li.card h4')).toHaveText(CARDS.map(([n]) => n));
  for (const [name, word] of CARDS) {
    const c = card(page, name);
    await expect(c).toContainText(word);
    await expect(c.getByRole('button')).toHaveCount(1);
    await expect(c.getByRole('button', { name: 'Open: ' + name, exact: true })).toHaveClass(/secondary-button/);
  }
  // Waiting cards say what to do, and the missing-source cards say they show nothing instead of a guess.
  await expect(card(page, 'Slowest APIs')).toContainText('Press Run the steps first. This tool shows what that run found.');
  await expect(card(page, 'MongoDB health')).toContainText('Nothing is shown rather than a guess.');
  await expect(card(page, 'Slowest calls')).toContainText('Sources: Audit');
  await expect(page.getByRole('heading', { name: 'Saved views (0)' })).toBeVisible();
  await expect(page.getByText('No view is saved yet.')).toBeVisible();
  await expect(page.locator('button.primary-button')).toHaveCount(0);
});

test('MAN-01a: the case actions menu says the work is kept, and ten switches back and forth end where they should', async ({ page }) => {
  await toManual(page);
  await page.getByText('Case actions').click();
  await expect(page.getByText('Your notes, checklist and saved views are kept.')).toBeVisible();
  await page.getByText('Case actions').click();
  for (let i = 0; i < 10; i++) {
    await toGuidedMode(page);
    await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
    await page.getByText('Case actions').click();
    await page.getByRole('button', { name: 'Switch to Manual tools' }).click();
    await expect(page.locator('#tools-title')).toHaveText('Manual tools');
  }
  await expect(page.locator('li.card')).toHaveCount(13);
});

test('MAN-01b: the mode is remembered for the case in this browser after a reload', async ({ page }) => {
  await toManual(page);
  await page.reload();
  await openGuidedAgain(page);
  await expect(page.locator('#tools-title')).toHaveText('Manual tools');
  await expect(page.locator('li.card')).toHaveCount(13);
});

async function openGuidedAgain(page: Page, caseTitle = 'Checkout slowdown, 17 October') {
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case ' + caseTitle }).click();
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
}

test('MAN-01c: a closed case can switch mode both ways and says the checklist and views are read-only', async ({ page }) => {
  await toManual(page, CLOSED_CASE);
  await expect(page.locator('li.card')).toHaveCount(13);
  await expect(page.getByText('This case is closed. You can open views but not change them.')).toBeVisible();
  await expect(page.getByText('This case is closed. You can read the checklist but not change it.').first()).toBeVisible();
  await toGuidedMode(page);
  await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
});

test('MAN-02: switching to Manual tools and back keeps the run, the checklist item and the saved view', async ({ page }) => {
  await openGuided(page);
  await setWindows(page);
  await page.getByRole('button', { name: 'Run the steps' }).click();
  await expect(stepRail(page).getByRole('button', { name: /Waiting|Running/ })).toHaveCount(0);
  const before = await stepRail(page).getByRole('button').allInnerTexts();
  // A checklist item and a saved view, made through the manual tools.
  await page.getByText('Case actions').click();
  await page.getByRole('button', { name: 'Switch to Manual tools' }).click();
  await openTool(page, 'Slowest APIs');
  await expect(page.getByRole('cell', { name: 'Payroll.Run' })).toBeVisible();
  await page.getByLabel('Add a note to the checklist').fill('Check Payroll.Run batch job');
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(page.getByText('Added to the checklist.')).toBeVisible();
  await page.getByRole('button', { name: 'Save this view' }).click();
  await page.getByLabel('Name').fill('M2 view');
  await page.getByRole('button', { name: 'Save the view' }).click();
  await expect(page.getByRole('heading', { name: 'Saved views (1)' })).toBeVisible();

  await page.getByRole('button', { name: 'Run Guided analysis instead' }).click();
  await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
  expect(await stepRail(page).getByRole('button').allInnerTexts()).toEqual(before);
  await expect(stepRail(page).getByRole('button', { name: /Failed/ })).toHaveCount(0);
  await page.getByRole('tab', { name: 'Checklist', exact: true }).click();
  await expect(page.locator('section.checklist').getByText('Check Payroll.Run batch job')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'What we learned (1)' })).toBeVisible();
  await page.getByRole('tab', { name: 'Steps', exact: true }).click();
  await page.getByText('Case actions').click();
  await page.getByRole('button', { name: 'Switch to Manual tools' }).click();
  await expect(page.getByRole('heading', { name: 'Saved views (1)' })).toBeVisible();
  await expect(page.getByRole('cell', { name: 'M2 view' })).toBeVisible();

  // Clean up so the next test starts from an empty list.
  await page.getByRole('row', { name: /M2 view/ }).getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByText('No view is saved yet.')).toBeVisible();
  await page.getByRole('tab', { name: 'Checklist', exact: true }).click();
  await page.locator('section.checklist').getByRole('button', { name: 'Delete' }).click();
  await expect(page.locator('section.checklist .count')).toHaveText('Done 0 · Open 0');
});

test('MAN-03: Slowest APIs ranks Payroll.Run first and both filters narrow the table', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Slowest APIs');
  await expect(page.getByText('This step shows the last run. Run the steps first to fill it in.')).toBeVisible();
  await runInTool(page);
  const rows = page.getByRole('table').first().getByRole('row');
  await expect(rows.nth(1)).toContainText('Payroll.Run');
  await expect(rows.nth(1)).toContainText('260 ms');
  await expect(rows.nth(1)).toContainText('52 ms');
  await expect(rows.nth(1)).toContainText('Slower');
  await expect(rows).toHaveCount(4);

  const minCalls = page.getByLabel('Minimum calls');
  await minCalls.fill('250');
  await expect(rows).toHaveCount(4);
  await minCalls.fill('251');
  await expect(page.getByText('No API had 251 or more calls. Lower Minimum calls to see more.')).toBeVisible();
  await minCalls.fill('100000');
  await expect(page.getByText('No API had 100,000 or more calls. Lower Minimum calls to see more.')).toBeVisible();
  await expect(page.getByRole('table')).toHaveCount(0);
  await minCalls.fill('20');

  await page.getByLabel('Only APIs matching').fill('Payroll');
  await expect(rows).toHaveCount(2);
  await page.getByLabel('Only APIs matching').fill('zzz');
  await expect(page.getByText('No API matches "zzz".')).toBeVisible();
  await expect(page.getByText(/No API had .* or more calls/)).toHaveCount(0);
  await page.getByRole('button', { name: 'Back to tools' }).click();
  await expect(page.locator('#tools-title')).toHaveText('Manual tools');
});

test('MAN-03a: a minimum of 0 or a negative number still shows the table and nothing breaks', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Slowest APIs');
  await runInTool(page);
  const minCalls = page.getByLabel('Minimum calls');
  for (const v of ['0', '-1']) {
    await minCalls.fill(v);
    await expect(page.getByRole('table').first().getByRole('row')).toHaveCount(4);
  }
  for (let i = 0; i < 5; i++) await page.getByRole('button', { name: 'Run again' }).click();
  await expect(page.getByRole('table').first().getByRole('row')).toHaveCount(4);
});

test('MAN-05: the four tools without a source say so, name the missing source and draw nothing', async ({ page }) => {
  await toManual(page);
  const need: Array<[string, string]> = [
    ['SQL blocking and waits', 'a SQL Server source'],
    ['MongoDB health', 'a MongoDB source'],
    ['Host CPU, memory and disk', 'a Blackbox source'],
    ['SQL host disk, memory and network', 'a Blackbox source']
  ];
  for (const [name, word] of need) {
    await openTool(page, name);
    await expect(page.getByRole('heading', { name: 'This tool cannot run yet' })).toBeVisible();
    await expect(page.getByText(`This tool needs ${word}, and this case has none. Nothing is shown rather than a guess.`)).toBeVisible();
    await expect(page.locator('section.tools canvas, section.tools svg[role="img"]')).toHaveCount(0);
    await page.getByRole('button', { name: 'Back to tools' }).click();
  }
});

test('MAN-05a: Add by location opens the import dialog and Escape closes it; twice does not stack two', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'SQL blocking and waits');
  const add = page.getByRole('button', { name: 'Add by location' });
  await add.click();
  const dialog = page.getByRole('dialog', { name: 'Import files' });
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await add.click();
  await expect(page.getByRole('dialog')).toHaveCount(1);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('MAN-05b: on a closed case a tool without a source says so and Add by location is off', async ({ page }) => {
  await toManual(page, CLOSED_CASE);
  await openTool(page, 'SQL blocking and waits');
  await expect(page.getByRole('heading', { name: 'This tool cannot run yet' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add by location' })).toBeDisabled();
  await expect(page.getByText('This case is closed. You can open views but not change them.')).toBeVisible();
});

test('MAN-05c: Time split runs on audit alone and says the outside time cannot be tested', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Time split A C P O');
  await runInTool(page);
  await expect(page.locator('section.tools').getByText('Partly ready').first()).toBeVisible();
  await expect(page.locator('section.tools')).toContainText('outside time cannot be tested');
});

test('MAN-06: a tool has the frame in order, takes the status word of its step from the run and keeps the run in Guided analysis', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Response time over time');
  const frame = page.locator('section.tools');
  await expect(frame.getByRole('button', { name: 'Back to tools' })).toBeVisible();
  await expect(frame.getByText('Incident window and Reference window (normal)')).toBeVisible();
  await expect(frame.getByRole('button', { name: 'Run the steps' })).toBeVisible();
  await expect(frame.getByRole('heading', { name: 'Saved views (0)' })).toBeVisible();
  await expect(frame.getByRole('button', { name: 'Run Guided analysis instead' })).toBeVisible();
  await expect(frame.getByLabel('Add a note to the checklist')).toBeVisible();
  // Before the run the tool reads Waiting; after it, the word of Step 2.
  await expect(frame.locator('header.head')).toContainText('Waiting');
  await runInTool(page);
  await expect(frame.locator('header.head')).toContainText('Ready');
  await page.getByRole('button', { name: 'Run Guided analysis instead' }).click();
  await expect(page.getByRole('heading', { name: 'Guided analysis', level: 2 })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run again' })).toBeVisible();
  await expect(stepRail(page).getByRole('button', { name: /Waiting|Running/ })).toHaveCount(0);
});

test('MAN-06a: the windows fold shut when a tool is opened with the windows already set, and open again on a click', async ({ page }) => {
  await openGuided(page);
  await setWindows(page);
  await page.getByText('Case actions').click();
  await page.getByRole('button', { name: 'Switch to Manual tools' }).click();
  await openTool(page, 'Slowest APIs');
  const windows = page.locator('details.windows');
  await expect(windows).not.toHaveJSProperty('open', true);
  await windows.locator('summary').click();
  await expect(windows).toHaveJSProperty('open', true);
  await expect(page.getByRole('group', { name: 'Incident window' }).getByLabel('From')).toHaveValue(INC[0]);
});

test('MAN-06b: the run button is off with empty windows and with overlapping windows, and says why', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Response time over time');
  const run = page.getByRole('button', { name: 'Run the steps' });
  await expect(run).toBeDisabled();
  await expect(page.getByText('Fix the windows to run the steps. The incident window needs a start and an end, in UTC.')).toBeVisible();
  await setWindows(page, INC, ['2026-10-20T08:50', '2026-10-20T09:00']);
  await expect(run).toBeDisabled();
  await expect(page.getByText('The two windows overlap. Pick a normal period that does not share any time with the incident.').first()).toBeVisible();
  await setWindows(page, INC, REF);
  await expect(run).toBeEnabled();
});

test('MAN-07: a note goes under What we learned, an empty note adds nothing and HTML is shown as plain text', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Slowest APIs');
  const note = page.getByLabel('Add a note to the checklist');
  const add = page.locator('form.note-form').getByRole('button', { name: 'Add', exact: true });
  await expect(add).toBeDisabled();
  await note.fill('Payroll.Run is 5x slower');
  await add.click();
  await expect(page.getByText('Added to the checklist.')).toBeVisible();
  await expect(note).toHaveValue('');
  const html = '<b>bold</b> <img src=x onerror=alert(1)> <script>window.__x=1</script>';
  await note.fill(html);
  await add.click();
  await page.getByRole('tab', { name: 'Checklist', exact: true }).click();
  const section = page.locator('section.checklist');
  await expect(section.getByRole('heading', { name: 'What we learned (2)' })).toBeVisible();
  await expect(section.getByText('Payroll.Run is 5x slower')).toBeVisible();
  await expect(section.getByText(html, { exact: true })).toBeVisible();
  await expect(section.locator('b, img, script')).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __x?: number }).__x)).toBeUndefined();
  for (const left of [1, 0]) {
    await section.getByRole('button', { name: 'Delete' }).first().click();
    await expect(section.getByRole('listitem')).toHaveCount(left);
  }
});

test('MAN-07a: a note of 301 characters is refused with the same sentence as the checklist, 300 is accepted', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Slowest APIs');
  const note = page.getByLabel('Add a note to the checklist');
  const add = page.locator('form.note-form').getByRole('button', { name: 'Add', exact: true });
  await note.fill('a'.repeat(301));
  await expect(page.getByText('An item holds up to 300 characters.')).toBeVisible();
  await expect(add).toBeDisabled();
  await note.fill('a'.repeat(300));
  await expect(page.getByText('An item holds up to 300 characters.')).toHaveCount(0);
  await expect(add).toBeEnabled();
});

test('MAN-07b: on a closed case there is no note box and the page says notes cannot be added', async ({ page }) => {
  await toManual(page, CLOSED_CASE);
  await openTool(page, 'Slowest APIs');
  await expect(page.getByLabel('Add a note to the checklist')).toHaveCount(0);
  await expect(page.getByText('This case is closed, so notes and checks cannot be added here. You can still read everything.')).toBeVisible();
});

async function saveView(page: Page, name: string) {
  await page.getByRole('button', { name: 'Save this view' }).click();
  await page.getByLabel('Name', { exact: true }).fill(name);
  await page.getByRole('button', { name: 'Save the view' }).click();
}

test('MAN-08: save a view of Slowest APIs, change everything, open it to get the windows and filters back, then delete it', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Slowest APIs');
  await runInTool(page);
  await page.getByLabel('Minimum calls').fill('100');
  await page.getByLabel('Only APIs matching').fill('Payroll');
  await page.getByRole('button', { name: 'Save this view' }).click();
  await expect(page.getByText('Up to 60 characters. Not a secret: it is shown in lists and in exports.')).toBeVisible();
  await expect(page.getByText('Saves: the windows, the API and the tool filters. Not saved: results.')).toBeVisible();
  await page.getByLabel('Name', { exact: true }).fill('Payroll slow');
  await page.getByRole('button', { name: 'Save the view' }).click();
  await expect(page.getByRole('heading', { name: 'Saved views (1)' })).toBeVisible();
  const table = page.locator('section.views table');
  await expect(table.getByRole('columnheader')).toHaveText(['Name', 'Tool', 'Windows (UTC)', 'Saved', 'Actions']);
  const row = table.getByRole('row', { name: /Payroll slow/ });
  await expect(row).toContainText('Slowest APIs');
  await expect(row).toContainText('2026-10-20');

  // Change the windows and the filters, then bring the view back.
  await page.getByLabel('Minimum calls').fill('5');
  await page.getByLabel('Only APIs matching').fill('Employees');
  await setWindows(page, ['2026-10-20T08:40', '2026-10-20T09:00'], ['2026-10-20T08:20', '2026-10-20T08:40']);
  await row.getByRole('button', { name: 'Open' }).click();
  await expect(page.locator('#tools-title')).toHaveText('Slowest APIs');
  await expect(page.getByLabel('Minimum calls')).toHaveValue('100');
  await expect(page.getByLabel('Only APIs matching')).toHaveValue('Payroll');
  const incident = page.getByRole('group', { name: 'Incident window' });
  await expect(incident.getByLabel('From')).toHaveValue(INC[0]);
  await expect(incident.getByLabel('To')).toHaveValue(INC[1]);
  const reference = page.getByRole('group', { name: 'Reference window (normal)' });
  await expect(reference.getByLabel('From')).toHaveValue(REF[0]);

  await row.getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByRole('heading', { name: 'Saved views (0)' })).toBeVisible();
  await expect(page.getByText('No view is saved yet.')).toBeVisible();
});

test('MAN-08a: an empty or blank name is refused with the sentence, and a name over 60 characters cannot be saved', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Slowest APIs');
  await runInTool(page);
  await page.getByRole('button', { name: 'Save this view' }).click();
  await page.getByRole('button', { name: 'Save the view' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Give the view a name.' })).toBeVisible();
  await page.getByLabel('Name', { exact: true }).fill('   ');
  await page.getByRole('button', { name: 'Save the view' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Give the view a name.' })).toBeVisible();
  await page.getByLabel('Name', { exact: true }).fill('n'.repeat(61));
  await expect(page.getByText('A view name holds up to 60 characters. Shorten it to save.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save the view' })).toBeDisabled();
  await page.getByLabel('Name', { exact: true }).fill('n'.repeat(60));
  await expect(page.getByRole('button', { name: 'Save the view' })).toBeEnabled();
  await expect(page.getByRole('heading', { name: 'Saved views (0)' })).toBeVisible();
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(page.getByLabel('Name', { exact: true })).toHaveCount(0);
});

test('MAN-08b: opening a saved view runs nothing', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Slowest APIs');
  await runInTool(page);
  await saveView(page, 'No run on open');
  await expect(page.getByRole('heading', { name: 'Saved views (1)' })).toBeVisible();
  await page.reload();
  await page.getByRole('button', { name: 'Home', exact: true }).click();
  await page.getByRole('button', { name: 'Open case Checkout slowdown, 17 October' }).click();
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
  await expect(page.locator('#tools-title')).toHaveText('Manual tools');
  await expect(page.getByRole('heading', { name: 'Saved views (1)' })).toBeVisible();
  await page.getByRole('row', { name: /No run on open/ }).getByRole('button', { name: 'Open' }).click();
  await expect(page.locator('#tools-title')).toHaveText('Slowest APIs');
  await expect(page.getByText('This step shows the last run. Run the steps first to fill it in.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run the steps' })).toBeVisible();
  await page.getByRole('row', { name: /No run on open/ }).getByRole('button', { name: 'Delete' }).click();
  await expect(page.getByText('No view is saved yet.')).toBeVisible();
});

test('MAN-08c: on a closed case there is no Save this view button and the page says views cannot be changed', async ({ page }) => {
  await toManual(page, CLOSED_CASE);
  await expect(page.getByRole('button', { name: 'Save this view' })).toHaveCount(0);
  await expect(page.getByText('This case is closed. You can open views but not change them.')).toBeVisible();
  await openTool(page, 'Slowest APIs');
  await expect(page.getByRole('button', { name: 'Save this view' })).toHaveCount(0);
});

async function stepText(page: Page, n: number): Promise<string> {
  const heading = page.locator('#step-title-' + n);
  await expect(heading).toBeVisible();
  return heading.evaluate((h) => {
    const clone = (h.closest('section') as HTMLElement).cloneNode(true) as HTMLElement;
    clone.querySelectorAll('button').forEach((b) => b.remove());
    return (clone.textContent ?? '').replace(/\s+/g, ' ').trim();
  });
}

test('MAN-10: Time split, Calls in flight, Changes timeline and Suspect summary read the same as steps 3, 4, 5 and 10', async ({ page }) => {
  await openGuided(page);
  await setWindows(page);
  await page.getByRole('button', { name: 'Run the steps' }).click();
  await expect(stepRail(page).getByRole('button', { name: /Waiting|Running/ })).toHaveCount(0);
  const steps = [3, 4, 5, 10];
  const words: Record<number, string> = {};
  const guided: Record<number, string> = {};
  for (const n of steps) {
    const button = stepRail(page).getByRole('button', { name: new RegExp('^' + n + ' ') });
    words[n] = ((await button.innerText()).split('\n').pop() ?? '').trim();
    await button.click();
    await expect(page.locator('#step-title-' + n)).toBeFocused();
    guided[n] = await stepText(page, n);
  }
  await page.getByText('Case actions').click();
  await page.getByRole('button', { name: 'Switch to Manual tools' }).click();
  const tools: Record<number, string> = { 3: 'Time split A C P O', 4: 'Calls in flight', 5: 'Changes timeline', 10: 'Suspect summary' };
  for (const n of steps) {
    await expect(card(page, tools[n])).toContainText(words[n]);
    await openTool(page, tools[n]);
    expect(await stepText(page, n)).toBe(guided[n]);
    await page.getByRole('button', { name: 'Back to tools' }).click();
  }
});

test('MAN-10a: after the windows change every tool card and the open tool say Out of date', async ({ page }) => {
  await toManual(page);
  await openTool(page, 'Time split A C P O');
  await runInTool(page);
  await expect(page.getByText('Out of date, rerun')).toHaveCount(0);
  await setWindows(page, ['2026-10-20T08:40', '2026-10-20T09:00'], ['2026-10-20T08:20', '2026-10-20T08:40']);
  await expect(page.getByText('The windows, sources or lists changed after this ran. The numbers below are from the earlier run.')).toBeVisible();
  await expect(page.locator('section.tools header.head')).toContainText('Out of date');
  await page.getByRole('button', { name: 'Back to tools' }).click();
  await expect(card(page, 'Time split A C P O')).toContainText('Out of date');
  await expect(card(page, 'Calls in flight')).toContainText('Out of date');
});

test('MAN-10b: a tool opened before any run says to run the steps first and shows no figure', async ({ page }) => {
  await toManual(page);
  for (const name of ['Time split A C P O', 'Calls in flight', 'Changes timeline', 'Suspect summary']) {
    await openTool(page, name);
    await expect(page.getByText('This step shows the last run. Run the steps first to fill it in.')).toBeVisible();
    await page.getByRole('button', { name: 'Back to tools' }).click();
  }
});
