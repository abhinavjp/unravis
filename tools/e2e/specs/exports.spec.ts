// Reports and exports: the CSV and HTML report, the shared export, and what must never be in them.
import { test, expect, type Page } from '../support/fixtures';
import { createCase, demoId, type ApiFn } from '../support/api';
import { goHome, openCase } from '../support/ui';

const WIN = {
  sourceIds: ['demo-west', 'demo-east'],
  incident: { start: '2026-10-20T08:45:00Z', end: '2026-10-20T09:00:00Z' },
  reference: { start: '2026-10-20T08:30:00Z', end: '2026-10-20T08:45:00Z' }
};
const NEWLINE = String.fromCharCode(10);
const BOM = String.fromCharCode(0xfeff);

/** A small RFC 4180 reader: rows of cells, quotes doubled, line breaks allowed inside quotes. */
function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quoted) {
      if (ch === '"') {
        if (text[i + 1] === '"') {
          cell += '"';
          i++;
        } else quoted = false;
      } else cell += ch;
    } else if (ch === '"') quoted = true;
    else if (ch === ',') {
      row.push(cell);
      cell = '';
    } else if (ch === NEWLINE) {
      row.push(cell);
      rows.push(row);
      row = [];
      cell = '';
    } else cell += ch;
  }
  if (cell !== '' || row.length) {
    row.push(cell);
    rows.push(row);
  }
  return rows;
}

async function report(api: ApiFn, id: string, format: 'csv' | 'html', analysed = true) {
  const r = await api('POST', `/api/cases/${id}/report`, analysed ? { format, request: WIN } : { format });
  expect(r.status, 'report ' + format).toBe(200);
  return r.json as { filename: string; mime: string; sha256: string; bytes: number; content: string };
}

/** The export file explains what it leaves out in its own words; those statements are not data. */
function withoutMaskingNote(text: string): string {
  const doc = JSON.parse(text);
  delete doc.manifest.masking;
  return JSON.stringify(doc);
}

async function sha256(text: string): Promise<string> {
  const { createHash } = await import('node:crypto');
  return createHash('sha256').update(Buffer.from(text, 'utf8')).digest('hex');
}

test('EXP-03a: the CSV starts with a byte order mark, has six columns on every row and says dates are UTC', async ({ api }) => {
  const id = await createCase(api, 'QA Csv Shape');
  const csv = await report(api, id, 'csv');
  expect(csv.mime).toContain('text/csv');
  expect(csv.filename).toMatch(/^unravis-report-.*-2026-10-20.csv$/);
  expect(csv.content.startsWith(BOM)).toBe(true);
  const rows = parseCsv(csv.content.slice(1)).filter((r) => r.length > 1 || r[0] !== '');
  expect(rows[0]).toEqual(['section', 'item', 'status', 'normal stretch', 'slow stretch', 'text']);
  for (const r of rows) expect(r, r.join('|').slice(0, 80)).toHaveLength(6);
  expect(csv.content).toContain('UTC');
  expect(await sha256(csv.content)).toBe(csv.sha256);
  expect(Buffer.byteLength(csv.content, 'utf8')).toBe(csv.bytes);
});

test('EXP-03b: item text with a comma and a quote is written as one safe cell, and a line break is refused', async ({ api }) => {
  const id = await createCase(api, 'QA Csv Quotes');
  const text = 'he said "hi", then a second line';
  expect((await api('POST', `/api/cases/${id}/checklist`, { list: 'todo', text })).status).toBe(200);
  // A line break inside an item is not accepted at all, so it can never reach the file.
  expect((await api('POST', `/api/cases/${id}/checklist`, { list: 'todo', text: 'a' + NEWLINE + 'b' })).status).toBe(400);
  const csv = await report(api, id, 'csv');
  const rows = parseCsv(csv.content.slice(1));
  const mine = rows.filter((r) => r[0] === 'checklist');
  expect(mine).toHaveLength(1);
  expect(mine[0]).toHaveLength(6);
  expect(mine[0][5]).toContain('he said "hi", then');
  expect(mine[0][5]).not.toContain(NEWLINE);
});

test('EXP-03: every CSV cell that starts with = + - or @ is written as text', async ({ api }) => {
  const id = await createCase(api, 'QA Csv Formula');
  for (const text of ['=SUM(A1:A9)', "+cmd|' /C calc'!A0", '-2+3', '@SUM(1)']) {
    expect((await api('POST', `/api/cases/${id}/checklist`, { list: 'todo', text })).status).toBe(200);
  }
  const csv = await report(api, id, 'csv');
  const cells = parseCsv(csv.content.slice(1)).flat();
  const unsafe = cells.filter((c) => /^[=+@-]/.test(c));
  expect(unsafe, 'cells that a spreadsheet may run or read as a formula').toEqual([]);
  expect(cells).toContain("'=SUM(A1:A9)");
});

test('EXP-04: a report before any run says nothing was analysed and invents no figure', async ({ api }) => {
  const id = await createCase(api, 'QA Empty Report');
  const csv = await report(api, id, 'csv', false);
  expect(csv.content).toContain('Nothing has been analysed in this case yet.');
  expect(csv.content).not.toContain('figure,');
  const html = await report(api, id, 'html', false);
  expect(html.mime).toContain('text/html');
  expect(html.content).toContain('Nothing has been analysed in this case yet.');
});

async function openExportTab(page: Page) {
  await goHome(page);
  await openCase(page, 'Checkout slowdown, 17 October');
  await page.getByRole('button', { name: 'Open guided analysis' }).click();
  await page.getByRole('tab', { name: 'Export' }).click();
}

test('EXP-04a: the Export tab before any run says so in one sentence', async ({ page }) => {
  await openExportTab(page);
  await expect(page.getByText('Nothing has been analysed yet. The report will say so and list the case sources only.')).toBeVisible();
});

test('EXP-06: a bad case id, a bad format and a wrong method are refused plainly with no report body', async ({ api }) => {
  const id = await createCase(api, 'QA Report Refusals');
  const noCase = await api('POST', '/api/cases/nope/report', { format: 'csv' });
  expect(noCase.status).toBe(400);
  expect(noCase.json.reason).toBe('bad-case-id');
  const missing = await api('POST', '/api/cases/' + 'zzzzzzzzzzzzzzzzzzzzzzzzzz' + '/report', { format: 'csv' });
  expect(missing.status).toBeGreaterThanOrEqual(400);
  expect(missing.status).toBeLessThan(500);
  const format = await api('POST', `/api/cases/${id}/report`, { format: 'pdf' });
  expect(format.status).toBe(400);
  const get = await api('GET', `/api/cases/${id}/report`);
  expect(get.status).toBe(405);
  for (const r of [noCase, missing, format, get]) {
    expect(r.text).not.toMatch(/goroutine|panic|\.go:\d+/);
    expect(r.text).not.toContain('section,item');
  }
});

test('EXP-06a: an analysed report on a closed case still builds, and a short window is refused', async ({ api }) => {
  const closed = await report(api, demoId('democlos'), 'csv');
  expect(closed.content).toContain('Night batch overrun');
  const backwards = await api('POST', `/api/cases/${demoId('democlos')}/report`, {
    format: 'csv',
    request: { ...WIN, incident: { start: WIN.incident.end, end: WIN.incident.start } }
  });
  expect(backwards.status).toBe(400);
});

test('EXP-08a: a hypothesis is not in the shared export unless Include my ledger is ticked', async ({ api }) => {
  const id = demoId('demoopen');
  const win = { sourceIds: WIN.sourceIds, start: '2026-10-20T08:00:00Z', end: '2026-10-20T09:00:00Z' };
  const first = await api('POST', '/api/export/preview', { ...win, includeLedger: true });
  expect(first.status).toBe(200);
  const records = first.json.export.body.records as { evidenceId: string }[];
  expect(records.length).toBeGreaterThan(0);
  const made = await api('POST', `/api/cases/${id}/ledger`, { text: 'Retries at the gateway were slow' });
  expect(made.status).toBe(201);
  const set = await api('PUT', `/api/cases/${id}/ledger/${made.json.id}`, {
    text: 'Retries at the gateway were slow',
    state: 'open',
    evidenceIds: [records[0].evidenceId],
    notes: 'private note'
  });
  expect(set.status).toBe(200);
  // A hypothesis that cites nothing in this export never appears.
  await api('POST', `/api/cases/${id}/ledger`, { text: 'Unrelated idea' });
  const shared = await api('POST', '/api/export/preview', { ...win, includeLedger: false });
  expect(JSON.stringify(shared.json.export.body)).not.toContain('Retries at the gateway');
});

test('EXP-08: Include my ledger puts the case hypothesis that cites a record into the export', async ({ api }) => {
  const id = await createCase(api, 'QA Ledger Share');
  const win = { sourceIds: WIN.sourceIds, start: '2026-10-20T08:00:00Z', end: '2026-10-20T09:00:00Z' };
  const first = await api('POST', '/api/export/preview', { ...win, includeLedger: true });
  const evidenceId = (first.json.export.body.records as { evidenceId: string }[])[0].evidenceId;
  const made = await api('POST', `/api/cases/${id}/ledger`, { text: 'Retries at the gateway were slow' });
  const set = await api('PUT', `/api/cases/${id}/ledger/${made.json.id}`, {
    text: 'Retries at the gateway were slow',
    state: 'open',
    evidenceIds: [evidenceId],
    notes: 'private note'
  });
  expect(set.status).toBe(200);
  const withLedger = await api('POST', '/api/export/preview', { ...win, includeLedger: true });
  const body = JSON.stringify(withLedger.json.export.body);
  expect(body, 'the ledger section of the export').toContain('Retries at the gateway were slow');
  expect(body).not.toContain('private note');
});

const FORBIDDEN = ['payload', '1003', 'password', 'Password', 'unravis_ro', 'demo-sql.example.invalid', 'demo-mongo.example.invalid', 'mongodb://', 'X-Audit-Token'];

test('EXP-09a: no payload, user id, secret, host name or token is in the CSV, the HTML or the shared export', async ({ api, demo }) => {
  const id = await createCase(api, 'QA Privacy Audit');
  const csv = await report(api, id, 'csv');
  const html = await report(api, id, 'html');
  const shared = await api('POST', '/api/export/preview', { sourceIds: WIN.sourceIds, start: '2026-10-20T08:00:00Z', end: '2026-10-20T09:00:00Z' });
  const confirm = await api('POST', '/api/export/confirm', { previewId: shared.json.previewId });
  expect(confirm.status).toBe(200);
  const launchCode = new URL(demo.link).pathname.split('/').pop() ?? '';
  // Two fixed phrases name the word: the footer claim and the suspect title M8. They are the product's own words, not a leak.
  const bare = (t: string) => t.replace(/No request payloads[^.]*[.]/g, '').split('Large payloads or document growth').join('');
  for (const [name, text] of [['csv', bare(csv.content)], ['html', bare(html.content)], ['export', bare(withoutMaskingNote(confirm.json.content as string))]] as const) {
    for (const bad of [...FORBIDDEN, demo.token, launchCode]) {
      expect(text.includes(bad), `${name} holds "${bad.slice(0, 12)}"`).toBe(false);
    }
  }
  expect(html.content).toContain('Dates in UTC');
});

test('EXP-09: typed text that looks like a password or a connection string is redacted or warned about before it is exported', async ({ api }) => {
  const id = await createCase(api, 'QA Typed Secret');
  const text = 'Check password=abc123 on mongodb://unravis_ro:Sup3r@demo-mongo.example.invalid/db';
  const added = await api('POST', `/api/cases/${id}/checklist`, { list: 'todo', text });
  const csv = await report(api, id, 'csv');
  const html = await report(api, id, 'html');
  const refused = added.status >= 400;
  const redacted = !csv.content.includes('abc123') && !csv.content.includes('mongodb://') && !html.content.includes('abc123') && !html.content.includes('mongodb://');
  expect(refused || redacted, 'the secret text is refused or kept out of both reports').toBe(true);
});

test('EXP-03c: the CSV of a new case is byte for byte the checked-in snapshot', async ({ api }) => {
  const { readFileSync } = await import('node:fs');
  const { join } = await import('node:path');
  const id = await createCase(api, 'Snapshot Case');
  const csv = await report(api, id, 'csv', false);
  const expected = readFileSync(join(__dirname, '..', 'fixtures', 'files', 'report-empty.csv'), 'utf8').replace('{CASE}', id);
  expect(csv.content).toBe(expected);
});
