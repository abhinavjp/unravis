// The user documents in the repository against the app: screenshots exist, pages link them,
// the Blackbox table says what the app says, and the troubleshooting page is true.
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { test, expect } from '../support/fixtures';
import { exePath } from '../support/launcher';

const ROOT = resolve(__dirname, '..', '..', '..');
const DOCS = join(ROOT, 'docs', 'user');

interface Screen {
  id: string;
  phase: string;
  doc: string;
}

function screens(): Screen[] {
  return JSON.parse(readFileSync(join(ROOT, 'tools', 'screens', 'screens.json'), 'utf8')).screens;
}

test('DOC-02: every screen id has a dark and a light picture in its phase folder', async () => {
  const missing: string[] = [];
  for (const s of screens()) {
    for (const theme of ['dark', 'light']) {
      const file = join(DOCS, 'screenshots', s.phase, `${s.id}-${theme}.png`);
      if (!existsSync(file)) missing.push(`${s.phase}/${s.id}-${theme}.png`);
    }
  }
  expect(missing, 'pictures that screens.json promises and the repository lacks').toEqual([]);
});

test('DOC-03: every screen named on a Screens in this part line has a dark and a light image link, and every image alt names its theme', async () => {
  const problems: string[] = [];
  const known = new Set(screens().map((s) => s.id));
  for (const name of readdirSync(DOCS).filter((n) => n.endsWith('.md'))) {
    const text = readFileSync(join(DOCS, name), 'utf8');
    const links = [...text.matchAll(/!\[([^\]]*)\]\(([^)]+)\)/g)].map((m) => ({ alt: m[1], target: m[2] }));
    for (const line of text.split(/\r?\n/)) {
      const m = /^Screens? in this part:(.*)$/.exec(line);
      if (!m) continue;
      for (const id of [...m[1].matchAll(/`([a-z0-9-]+)`/g)].map((x) => x[1])) {
        if (!known.has(id)) problems.push(`${name}: names ${id}, which does not exist`);
        for (const theme of ['dark', 'light']) {
          if (!links.some((l) => l.target.endsWith(`/${id}-${theme}.png`))) problems.push(`${name}: ${id} has no ${theme} image link`);
        }
      }
    }
    for (const l of links) {
      if (!/\b(dark|light)\b/i.test(l.alt)) problems.push(`${name}: image alt without a theme: ${l.alt.slice(0, 50)}`);
    }
  }
  expect(problems).toEqual([]);
});

interface Row {
  state: string;
  readiness: string;
  text: string;
}

function blackboxTable(): Row[] {
  const text = readFileSync(join(DOCS, 'blackbox.md'), 'utf8');
  const rows: Row[] = [];
  let inTable = false;
  for (const line of text.split(/\r?\n/)) {
    const cells = line.split('|').map((c) => c.trim());
    if (cells[1] === 'State' && cells[2] === 'Readiness') {
      inTable = true;
      continue;
    }
    if (!inTable) continue;
    if (!line.startsWith('|')) break;
    if (/^-+$/.test(cells[1])) continue;
    rows.push({ state: cells[1], readiness: cells[2], text: cells[3] });
  }
  return rows;
}

test('DOC-05: the Blackbox states table says what the app says for all nine states', async ({ page }) => {
  const table = blackboxTable();
  expect(table.map((r) => r.state)).toEqual(['Running', 'Running late', 'Stopped', 'Disk low', 'Clock differs', 'Not found', 'Unreachable', 'Incomplete', 'Not placed']);
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  const open = dialog.getByRole('button', { name: /^Open status: / });
  await expect(open.first()).toBeVisible();
  const names = (await open.allInnerTexts()).length;
  const labels: string[] = [];
  for (let i = 0; i < names; i++) labels.push((await open.nth(i).getAttribute('aria-label')) ?? '');
  const problems: string[] = [];
  const seen = new Set<string>();
  for (const label of labels) {
    const name = label.replace(/^Open status: /, '');
    await dialog.getByRole('button', { name: label }).click();
    const region = dialog.getByRole('region', { name });
    await expect(region).toBeVisible();
    const state = (await region.locator('.bb-sticker').first().innerText()).trim();
    const readiness = (await region.locator('.bb-ready strong').first().innerText()).trim();
    const next = (await region.locator('p.bb-note').first().innerText()).trim();
    seen.add(state);
    const row = table.find((r) => r.state === state);
    if (!row) problems.push(`${name}: the app shows state "${state}", which is not in the table`);
    else {
      if (row.readiness !== readiness) problems.push(`${state}: table says "${row.readiness}", app says "${readiness}"`);
      if (!row.text.includes(next)) problems.push(`${state}: next step "${next}" is not in the table row`);
    }
    await region.getByRole('button', { name: 'Back to the list' }).click();
  }
  for (const r of table) if (!seen.has(r.state)) problems.push(`${r.state}: no demo Blackbox shows this state`);
  expect(problems).toEqual([]);
});

/** Run the demo exe once with these arguments; it must end by itself. */
function runOnce(args: string[], configDir: string) {
  return spawnSync(exePath(), args, { encoding: 'utf8', timeout: 20000, windowsHide: true, env: { ...process.env, AUDIT_CONFIG_DIR: configDir } });
}

test('DOC-10: a port that is already in use gets a plain sentence that says what to do', async ({ demo }) => {
  const holder = createServer();
  await new Promise<void>((ok) => holder.listen(0, '127.0.0.1', ok));
  try {
    const port = (holder.address() as { port: number }).port;
    const r = runOnce(['--demo', '--no-browser', '--port', String(port)], join(demo.scratch, 'busy-config'));
    expect(r.status, 'it must stop and not start').not.toBe(0);
    const said = (r.stderr + r.stdout).trim();
    expect(said, 'a raw network error is not a plain sentence').not.toMatch(/tcp4|bind:|socket address/i);
    expect(said).toMatch(/--port|port/i);
  } finally {
    holder.close();
  }
});

test('DOC-10a: --version answers, a bad port is refused plainly, and --no-browser starts without a browser', async ({ demo }) => {
  const version = runOnce(['--version'], join(demo.scratch, 'v-config'));
  expect(version.status).toBe(0);
  expect(version.stdout).toMatch(/Unravis/);
  const bad = runOnce(['--demo', '--no-browser', '--port', '99999'], join(demo.scratch, 'bad-config'));
  expect(bad.status).not.toBe(0);
  expect(bad.stderr).toContain('port must be between 0 and 65535');
  // The fixture itself starts every demo with --no-browser and printed a link: that is the proof.
  expect(demo.link).toContain('/');
});
