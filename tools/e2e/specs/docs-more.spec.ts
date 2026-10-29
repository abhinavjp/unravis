// The user documents against the code they describe: the docs check, the page list, the install, upgrade and security pages,
// the "probe is not in release 1" sentences and the copy lint. These read files in the repository; the demo only gives the page for DOC-07a.
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, renameSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { test, expect } from '../support/fixtures';

const ROOT = resolve(__dirname, '..', '..', '..');
const DOCS = join(ROOT, 'docs', 'user');
const read = (...p: string[]) => readFileSync(join(ROOT, ...p), 'utf8');
const doc = (slug: string) => readFileSync(join(DOCS, slug + '.md'), 'utf8');
const mdFiles = () => readdirSync(DOCS).filter((f) => f.endsWith('.md')).sort();

/** The text of a page with fenced code, inline code and image lines taken out: only sentences are left. */
function prose(text: string): string {
  return text.replace(/```[\s\S]*?```/g, ' ').replace(/`[^`\n]*`/g, ' ').replace(/^!\[.*$/gm, ' ');
}

function runDocsCheck(args: string[], cwd: string) {
  const r = spawnSync(process.execPath, [join(ROOT, 'tools', 'screens', 'docs-check.mjs'), ...args], { cwd, encoding: 'utf8', timeout: 60000, windowsHide: true });
  return { code: r.status, out: (r.stdout ?? '') + (r.stderr ?? '') };
}

function pageOrder(): string[] {
  const m = read('web', 'src', 'lib', 'help', 'links.ts').match(/PAGE_ORDER: readonly string\[\] = \[([\s\S]*?)\]/);
  expect(m, 'PAGE_ORDER was not found in links.ts').toBeTruthy();
  return [...(m as RegExpMatchArray)[1].matchAll(/'([^']+)'/g)].map((x) => x[1]);
}

test('DOC-01: docs-check passes from the project folder and from any other folder', async () => {
  const here = runDocsCheck([], ROOT);
  expect(here.out).toMatch(/Docs are in step: \d+ pages, \d+ screens\./);
  expect(here.code).toBe(0);
  const elsewhere = runDocsCheck([], tmpdir());
  expect(elsewhere.code, 'it finds the docs from its own file location').toBe(0);
  expect(elsewhere.out).toBe(here.out);
});

test('DOC-01a: in a scratch copy a renamed picture, a dropped screen and a wrong folder each fail with a plain line', async ({ demo }) => {
  const copy = join(demo.scratch, 'docs-copy');
  mkdirSync(join(copy, 'tools', 'screens'), { recursive: true });
  mkdirSync(join(copy, 'web', 'src', 'lib', 'copy'), { recursive: true });
  cpSync(DOCS, join(copy, 'docs', 'user'), { recursive: true });
  cpSync(join(ROOT, 'tools', 'screens', 'screens.json'), join(copy, 'tools', 'screens', 'screens.json'));
  cpSync(join(ROOT, 'web', 'src', 'lib', 'copy', 'blackboxCopy.ts'), join(copy, 'web', 'src', 'lib', 'copy', 'blackboxCopy.ts'));
  const clean = runDocsCheck([copy], tmpdir());
  expect(clean.out, 'the scratch copy passes before it is broken').toMatch(/Docs are in step/);
  expect(clean.code).toBe(0);

  // 1. Rename one picture that a page links.
  const link = doc('blackbox').match(/!\[[^\]]*\]\((screenshots\/[^)]+\.png)\)/);
  expect(link).toBeTruthy();
  const rel = (link as RegExpMatchArray)[1];
  renameSync(join(copy, 'docs', 'user', rel), join(copy, 'docs', 'user', rel + '.gone'));
  const renamed = runDocsCheck([copy], tmpdir());
  expect(renamed.code).toBe(1);
  expect(renamed.out).toContain('blackbox.md');
  expect(renamed.out).toContain('does not exist');
  renameSync(join(copy, 'docs', 'user', rel + '.gone'), join(copy, 'docs', 'user', rel));

  // 2. Drop one screen from screens.json while its picture is still linked.
  const file = join(copy, 'tools', 'screens', 'screens.json');
  const json = JSON.parse(readFileSync(file, 'utf8'));
  const id = (rel.split('/').pop() as string).replace(/-(dark|light)\.png$/, '');
  json.screens = json.screens.filter((s: { id: string }) => s.id !== id);
  writeFileSync(file, JSON.stringify(json));
  const dropped = runDocsCheck([copy], tmpdir());
  expect(dropped.code).toBe(1);
  expect(dropped.out).toContain(id);
  expect(dropped.out).toContain('no longer captures');

  // 3. A folder with no project in it says what is wrong.
  const empty = join(demo.scratch, 'docs-nothing');
  mkdirSync(empty);
  const wrong = runDocsCheck([empty], tmpdir());
  expect(wrong.code).toBe(1);
  expect(wrong.out).toContain('screens.json could not be read');
});

test('DOC-04: the page list of the in-app guide and the files in docs/user agree', async () => {
  const order = pageOrder();
  expect(order).toHaveLength(11);
  const files = mdFiles();
  expect(files).toHaveLength(12);
  for (const slug of order) expect(files, `${slug}.md exists`).toContain(slug + '.md');
  expect(files.filter((f) => !order.includes(f.replace(/\.md$/, ''))), 'only screenshots.md is outside the guide').toEqual(['screenshots.md']);
  // The guide reads the same folder, so a new page would join it by name.
  expect(read('web', 'src', 'lib', 'help', 'pages.ts')).toContain("import.meta.glob('../../../../docs/user/*.md'");
});

test('DOC-04a: the index links every page and every relative link between pages points at a page and a heading that exist', async () => {
  const index = doc('index');
  for (const f of mdFiles()) if (f !== 'index.md') expect(index, `index.md links ${f}`).toContain(`(${f})`);
  const slugify = (t: string) => t.trim().toLowerCase().replace(/[^\p{L}\p{N} -]/gu, '').replace(/ /g, '-');
  const headings = (slug: string) => [...doc(slug).matchAll(/^#{1,6}\s+(.*)$/gm)].map((m) => slugify(m[1].replace(/[*`]/g, '').replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')));
  const problems: string[] = [];
  let seen = 0;
  for (const f of mdFiles()) {
    for (const m of prose(doc(f.replace(/\.md$/, ''))).matchAll(/\]\(([^)\s#]*)(#[^)\s]*)?\)/g)) {
      const [, target, anchor] = m;
      if (/^[a-z]+:/i.test(target)) { problems.push(`${f}: an outside link ${target}`); continue; }
      const slug = target === '' ? f.replace(/\.md$/, '') : target.replace(/\.md$/, '');
      if (target !== '' && !target.endsWith('.md')) continue;
      seen++;
      if (!existsSync(join(DOCS, slug + '.md'))) { problems.push(`${f}: ${target} does not exist`); continue; }
      if (anchor && !headings(slug).includes(anchor.slice(1))) problems.push(`${f}: ${target}${anchor} has no such heading`);
    }
  }
  expect(seen).toBeGreaterThan(10);
  expect(problems).toEqual([]);
});

test('DOC-06: the install page names the options, checks, flags, exit codes and log path the installer has', async () => {
  const install = doc('install');
  const app = read('web', 'setup', 'App.svelte');
  const labels: Array<[string, string]> = [
    ['Just for me', 'Just for me <span class="quiet">(no admin needed)</span>'],
    ['For everyone on this PC', 'For everyone on this PC <span class="quiet">(admin needed)</span>'],
    ['Start Unravis when I sign in', 'Start Unravis when I sign in'],
    ['Close Unravis for me', 'Close Unravis for me'],
    ['Also place a Blackbox recorder on this PC', 'Also place a Blackbox recorder on this PC'],
    ['Allow other PCs to read it (firewall rule)', 'Allow other PCs to read it (firewall rule)'],
  ];
  for (const [name, inApp] of labels) {
    expect(install, `install.md names "${name}"`).toContain(`**${name}**`);
    expect(app, `the installer shows "${name}"`).toContain(inApp);
  }
  expect(install, 'the page does not name a control the installer lacks').not.toContain('Open the Blackbox port to these PCs');

  // Every check in the table is a check the installer makes.
  const table = install.split('## Checks')[1].split('A **Failed** check')[0];
  const names = [...table.matchAll(/^\| ([^|]+) \|/gm)].map((m) => m[1].trim()).filter((n) => n !== 'Check' && !/^-+$/.test(n));
  expect(names.length).toBe(18);
  const preflight = read('internal', 'setup', 'preflight.go');
  const missing = names.filter((n) => !(preflight.includes(`"${n}"`) || (n === 'Blackbox port' && preflight.includes('"Blackbox port %d"'))));
  expect(missing, 'checks in the docs table that the code does not make').toEqual([]);

  // Flags and exit codes.
  const main = read('cmd', 'unravis-setup', 'main.go');
  for (const flag of ['silent', 'config', 'log', 'upgrade', 'repair', 'uninstall', 'allow-downgrade', 'no-browser', 'version']) {
    expect(main, `--${flag} exists`).toMatch(new RegExp(`fl\\.\\w+Var\\(&o\\.\\w+, "${flag}"`));
    expect(install, `install.md names --${flag}`).toContain('--' + flag);
  }
  const exit = read('internal', 'setup', 'runner.go').match(/func \(o Outcome\) ExitCode\(\) int \{([\s\S]*?)\n\}/) as RegExpMatchArray;
  const codes = [...exit[1].matchAll(/return (\d)/g)].map((m) => Number(m[1]));
  expect([...new Set(codes)].sort()).toEqual([0, 1, 2, 3, 4, 5]);
  const exitTable = install.split('### Exit codes')[1].split('## The log')[0];
  expect([...exitTable.matchAll(/^\| (\d) \|/gm)].map((m) => Number(m[1]))).toEqual([0, 1, 2, 3, 4, 5]);

  // The log path.
  expect(install).toContain('%LOCALAPPDATA%\\Unravis\\setup-logs\\setup-YYYYMMDD-HHMMSS.log');
  expect(read('internal', 'setup', 'log.go')).toMatch(/filepath\.Join\(localAppData, "Unravis", "setup-logs", name\+"\.log"\)/);
  expect(read('internal', 'setup', 'log.go')).toContain('setup-<yyyymmdd-hhmmss>.log');
});

test('DOC-06a: the silent install keys on the page are the keys the installer reads, and the progress steps are its steps', async () => {
  const install = doc('install');
  const silent = read('internal', 'setup', 'silent.go') + read('internal', 'setup', 'options.go');
  const block = install.match(/```json\r?\n([\s\S]*?)```/) as RegExpMatchArray;
  const sample = JSON.parse(block[1]);
  const keys = [...Object.keys(sample), ...Object.keys(sample.blackbox).map((k) => 'blackbox.' + k), ...Object.keys(sample.firewall).map((k) => 'firewall.' + k)];
  const tags = new Set([...silent.matchAll(/json:"([A-Za-z]+)/g)].map((m) => m[1]));
  const absent = keys.filter((k) => !tags.has(k.split('.').pop() as string));
  expect(absent, 'keys on the page that no struct tag reads').toEqual([]);
  expect(tags.has('openWhenDone')).toBe(true);
  expect(install).toContain('`openWhenDone` is accepted but ignored');

  const plan = read('internal', 'setup', 'plan.go');
  const section = install.split('The steps for an install are:')[1].split('Every change is written')[0];
  const docSteps = [...section.matchAll(/^\d+\. (.*)$/gm)].map((m) => m[1].replace(/ \(only .*\)$/, '').trim());
  expect(docSteps).toHaveLength(10);
  const wants: Array<[string, string]> = [
    ['Close Unravis', 'Close Unravis'], ['Make the program folder', 'Make the program folder'], ['Copy the program files', 'Copy the program files'],
    ['Make the shared data folder, record it, and set up the key keeper service', 'Make the shared data folder'],
    ['Set up the Blackbox recorder', 'Set up the Blackbox recorder'], ['Open the Blackbox port to the listed PCs', 'Open the Blackbox port to the listed PCs'],
    ['Add the Start menu shortcut', 'Add the Start menu shortcut'], ['Add Unravis to Installed apps', 'Add Unravis to Installed apps'],
    ['Save the setup choices', 'Save the setup choices'], ['Check that Unravis starts', 'Check that Unravis starts'],
  ];
  wants.forEach(([docText, codeTitle], i) => {
    expect(docSteps[i]).toBe(docText);
    expect(plan, `plan.go has the step "${codeTitle}"`).toContain(`Title: "${codeTitle}"`);
  });
});

test('DOC-07: the upgrade and uninstall page says what the update check, the installer and the update heading say', async () => {
  const page = doc('upgrade-and-uninstall');
  const check = read('web', 'src', 'lib', 'updateCheck.ts');
  const answers = [
    'You are on the latest version (v', ' is available. You have v',
    'Could not check for updates. Check your connection and try again later.',
    'This is a development build, so there is nothing to compare.',
  ];
  for (const a of answers) {
    expect(check, 'the app says: ' + a).toContain(a);
    expect(page, 'the page says: ' + a).toContain(a);
  }
  expect(page).toContain('**Open the release page**');
  const app = read('web', 'setup', 'App.svelte');
  for (const label of ['Also delete my cases and saved connections', 'Also delete the cases and saved connections of everyone on this PC', 'Also delete the Blackbox recordings']) {
    expect(page, 'the page names: ' + label).toContain(`**${label}`);
    expect(app, 'the installer shows: ' + label).toContain(label);
  }
  expect(read('web', 'setup', 'logic.ts')).toContain("CONFIRM_DELETE_WORD = 'DELETE'");
  expect(page).toContain('type `DELETE`');
  expect(read('web', 'setup', 'logic.ts')).toContain("'Upgrade Unravis ' + (state.installed?.version ?? '') + ' to ' + state.version");
  expect(page).toContain('**Upgrade Unravis 0.9.2 to 1.0.0**');
  expect(read('internal', 'setup', 'capture.go')).toContain('Version: "0.9.2"');
  expect(page, 'the installer notes on the page are honest about the missing VM run').toContain('> NOT RUN: needs a clean VM.');
  expect(doc('install')).toContain('> NOT RUN: needs a clean VM.');
  const setupSource = readdirSync(join(ROOT, "internal", "setup")).filter((f) => f.endsWith(".go") && !f.endsWith("_test.go")).map((f) => read("internal", "setup", f)).join('\n');
  for (const name of ["setup-state.json", ".setup-backup-", "blackbox.json.bak"]) expect(setupSource, name + " is a name the installer uses").toContain(name);
});

test('DOC-07a: the page says Unravis never checks by itself, and the app makes no update call and no outside call on its own', async ({ page }) => {
  const outside: string[] = [];
  const update: string[] = [];
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (!['127.0.0.1', 'localhost'].includes(u.hostname) && u.protocol.startsWith('http')) outside.push(r.url());
    if (/update-check/.test(u.pathname)) update.push(r.method() + ' ' + u.pathname);
  });
  expect(doc('upgrade-and-uninstall')).toContain('Unravis never checks for a new version by itself');
  await page.getByRole('button', { name: 'Glossary' }).click();
  const guide = page.getByRole('dialog');
  await expect(guide.getByRole('button', { name: 'Check for updates' })).toBeVisible();
  await page.waitForLoadState('networkidle');
  expect(update, 'opening the help panel does not check').toEqual([]);
  expect(outside).toEqual([]);
});

test('DOC-08: every number and name on the security page equals a constant in the keeper code', async () => {
  const sec = doc('security');
  const k = (f: string) => read('internal', 'keeper', f);
  const num = (src: string, re: RegExp) => Number((src.match(re) as RegExpMatchArray)[1]);
  // The audit log.
  expect(num(k('auditlog.go'), /DefaultAuditMaxBytes = (\d+) << 20/)).toBe(4);
  expect(num(k('auditlog.go'), /const AuditKeep = (\d+)/)).toBe(8);
  expect(sec).toContain('rotated at 4 MB, and eight older files are kept');
  // Limits per account per minute.
  const limits = k('ratelimit.go');
  expect(num(limits, /classBad:\s+(\d+)/)).toBe(30);
  expect(num(limits, /classAudit:\s+(\d+)/)).toBe(600);
  expect(num(limits, /classReach:\s+(\d+)/)).toBe(30);
  expect(sec).toContain('a limit of 30 a minute per account');
  expect(sec).toContain('30 connections to a server a minute');
  expect(sec).toContain('600 a minute per Windows account');
  // Seats and reads.
  expect(num(k('audit.go'), /DefaultSeatIdle = (\d+) \* time\.Hour/)).toBe(8);
  expect(num(k('audit.go'), /DefaultMaxSeats = (\d+)/)).toBe(16);
  expect(num(k('audit.go'), /DefaultSeatsPerPerson = (\d+)/)).toBe(4);
  expect(sec).toContain('in 8 hours are closed');
  expect(sec).toContain('At most 16 running Unravis programs, and 4 per Windows account');
  expect(num(k('server.go'), /defaultAuditConns = (\d+)/)).toBe(6);
  expect(num(k('server.go'), /defaultAuditRuns\s+= (\d+)/)).toBe(4);
  expect(sec).toContain('(6 at once, 4 running)');
  expect(num(k('server.go'), /auditReadCap = (\d+) \* time\.Second/)).toBe(90);
  expect(sec).toContain('at most 90 seconds');
  expect(num(k('auditrepo.go'), /MaxAuditAnswer = (\d+) << 20/)).toBe(64);
  expect(sec).toContain('larger than 64 MiB');
  expect(num(k('server.go'), /maxAuditOp = (\d+)/)).toBe(32);
  expect(sec).toContain('at most 32 characters');
  // Names.
  expect(read('internal', 'datadir', 'datadir.go')).toContain('DefaultPipe = `\\\\.\\pipe\\UnravisKeeper`');
  expect(sec).toContain('`\\\\.\\pipe\\UnravisKeeper`');
  expect(k('owner.go')).toContain('ServiceName = "UnravisKeeper"');
  expect(read('internal', 'datadir', 'trust_windows.go')).toContain('AnchorKey = `SOFTWARE\\Unravis`');
  expect(sec).toContain('`HKEY_LOCAL_MACHINE\\SOFTWARE\\Unravis`');
  // The master key wait.
  const mk = read('internal', 'server', 'masterkey.go');
  expect(num(mk, /throttleAfter\s+= (\d+)/)).toBe(5);
  expect(mk).toMatch(/throttleBaseWait = time\.Second/);
  expect(num(mk, /throttleMaxWait\s+= (\d+) \* time\.Second/)).toBe(60);
  expect(sec).toContain('After five wrong tries in a row, Unravis makes you wait: one second, then doubling, up to one minute.');
});

test('DOC-08a: the one-time link life and the refused Show and Copy on the security page are what the code does', async () => {
  const sec = doc('security');
  const main = read('cmd', 'audit-explorer', 'main.go');
  expect(main).toMatch(/launchCodeLifeBrowser\s+= 2 \* time\.Minute/);
  expect(main).toMatch(/launchCodeLifeManual\s+= 10 \* time\.Minute/);
  expect(sec).toContain('the link expires after 2 minutes');
  expect(sec).toContain('expires after 10 minutes');
  expect(sec).toContain('Show and Copy for a saved Blackbox read key are refused with a plain message');
  expect(read('internal', 'server', 'blackbox.go')).toContain('The key keeper holds this read key and never gives it out.');
  expect(sec).toContain('The one time it contacts the internet is when you press Check for updates');
});

test('DOC-09: the SQL probe is said to be outside release 1 wherever it is mentioned, and nothing promises it', async () => {
  const hits: string[] = [];
  for (const f of mdFiles()) {
    doc(f.replace(/\.md$/, '')).split(/\r?\n/).forEach((line, i) => { if (/probe/i.test(line)) hits.push(`${f}:${i + 1}: ${line}`); });
  }
  // The pages mention it only inside the sample manifest, with the switch off.
  for (const h of hits) expect(h, 'a docs mention of the probe').toMatch(/"probe":\{"enabled":false\}/);
  // A page may say it is not in release 1, but none may offer it.
  for (const f of mdFiles()) expect(prose(doc(f.replace(/\.md$/, ''))), `${f} does not offer a probe`).not.toMatch(/\bprobe\b.*\b(can|will|lets you|turn on|enable)\b/i);
  const copy = read('web', 'src', 'lib', 'copy', 'blackboxCopy.ts');
  expect(copy).toContain("probeNote: 'The SQL Server probe is not part of this release.'");
  expect(copy).toContain("reviewProbeOff: 'Off (not in this release)'");
  // The in-app glossary and help copy never mention it.
  for (const f of ['helpCopy.ts', 'wordings.ts']) expect(read('web', 'src', 'lib', 'copy', f), `${f} has no probe`).not.toMatch(/probe/i);
});

test('DOC-09a: the wizard says the probe is not in this release on Settings and shows it Off on Review', async ({ page }) => {
  await page.getByRole('button', { name: 'Blackbox', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Blackbox' });
  await dialog.getByRole('button', { name: 'Place a Blackbox' }).click();
  await dialog.getByLabel('Name', { exact: true }).fill('bb-doc-09');
  await dialog.getByRole('button', { name: 'Next: Where' }).click();
  await dialog.getByRole('button', { name: 'Next: Settings' }).click();
  await expect(dialog.getByText('The SQL Server probe is not part of this release.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Next: Review' }).click();
  await expect(dialog.getByRole('list', { name: 'Part 4 of 4' })).toBeVisible();
  const row = dialog.getByText('Database probe', { exact: true });
  await expect(row).toBeVisible();
  await expect(dialog.getByText('Off (not in this release)')).toBeVisible();
});

test('DOC-11: the docs have no emoji, no dash used as a stop, and none of the cause or proof words in a sentence about a measurement', async () => {
  const problems: string[] = [];
  for (const f of mdFiles()) {
    const text = doc(f.replace(/\.md$/, ''));
    if (/\p{Extended_Pictographic}/u.test(text)) problems.push(`${f}: an emoji`);
    if (/[—–]/.test(text)) problems.push(`${f}: an em or en dash`);
    prose(text).split(/(?<=[.!?])\s+|\n/).forEach((s) => {
      if (/\b(due to|waiting on|blocked by|led to|result in|resulted in|caused)\b/i.test(s)) problems.push(`${f}: "${s.trim().slice(0, 80)}"`);
      if (/\b(prove|proves|proved|proven|proof)\b/i.test(s) && !/\b(not|never|nor|cannot)\b|n['’]t/i.test(s)) problems.push(`${f}: proof word in "${s.trim().slice(0, 80)}"`);
      // Two sentences in blackbox.md name the recorder's own "cause" field of a manifest gap (the value is a code such as disk-low,
      // taken out above). That is a field name, not a claim about a measurement, so they are the one justified exception.
      const fieldName = f === 'blackbox.md' && /\b(gap with cause|a gap, with its cause when the recorder knows it)\b/i.test(s);
      if (/\bcause\b/i.test(s) && !fieldName) problems.push(`${f}: cause in "${s.trim().slice(0, 80)}"`);
    });
  }
  expect(problems).toEqual([]);
});

test('DOC-11a: the in-app guide shows the same Markdown, so the lint above covers the guide text too', async () => {
  const pages = read('web', 'src', 'lib', 'help', 'pages.ts');
  expect(pages).toContain("import.meta.glob('../../../../docs/user/*.md', { query: '?raw'");
  expect(pages).not.toMatch(/\.md['"`]\s*:\s*['"`]/);
  expect(runDocsCheck([], ROOT).code, 'the docs check also bans caused, due to and proof in the pages').toBe(0);
});
