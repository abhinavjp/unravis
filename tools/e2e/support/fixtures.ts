// The test fixture every spec uses.
//   demo     one fresh demo app per spec FILE (its own empty data folder, its own port)
//   api      calls the demo's HTTP API with the session token
//   page     already on <base>/#t=<token>
//   allow    lets this one test see an error it provokes on purpose
// An auto fixture also (1) marks a test as expected-to-fail when its scenario id is
// in known-defects.json, and (2) fails the test on any console error, page error or
// failed request that is not allowed.
import { test as base, expect, type Page, type TestInfo } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { makeApi, type ApiFn } from './api';
import { ALLOWED } from './allow-list';
import { startDemo, type Demo, type StartOptions } from './launcher';

const KNOWN: Record<string, string> = JSON.parse(readFileSync(join(__dirname, '..', 'known-defects.json'), 'utf8'));

/** The scenario id a test title starts with, for example "CASE-10". */
export function scenarioOf(title: string): string | undefined {
  return /^([A-Z0-9]+-\d+[a-z]?)\b/.exec(title)?.[1];
}

// One demo per spec file. Tests run one at a time, in file order, so a module-level
// slot is enough; a new file (or new options) replaces it.
let slot: { key: string; demo: Promise<Demo> } | null = null;

async function stopSlot(): Promise<void> {
  const s = slot;
  slot = null;
  if (s) await (await s.demo.catch(() => null))?.stop();
}

async function demoFor(testInfo: TestInfo, options: StartOptions): Promise<Demo> {
  const key = testInfo.file + '|' + JSON.stringify(options);
  if (!slot || slot.key !== key) {
    await stopSlot();
    slot = { key, demo: startDemo(options) };
  }
  return slot.demo;
}

interface Issue {
  kind: string;
  text: string;
}

export interface Fixtures {
  demo: Demo;
  api: ApiFn;
  allow: (match: RegExp, why: string) => void;
  issues: void;
  demoOptions: StartOptions;
}

export const test = base.extend<Fixtures, { reaper: void }>({
  demoOptions: [{}, { option: true }],

  reaper: [
    async ({}, use) => {
      await use();
      await stopSlot();
    },
    { scope: 'worker', auto: true }
  ],

  demo: async ({ demoOptions }, use, testInfo) => {
    await use(await demoFor(testInfo, demoOptions));
  },

  api: async ({ demo }, use) => {
    await use(makeApi(demo.base, demo.token));
  },

  page: async ({ page, demo }, use, testInfo) => {
    // Windows now and then answers a local connection with ERR_NO_BUFFER_SPACE (a full socket buffer on the test PC, not an app
    // fault). Only that one error is tried again, and only before the test has done anything.
    for (let attempt = 1; ; attempt++) {
      try {
        await page.goto(demo.base + '/#t=' + demo.token);
        break;
      } catch (err) {
        if (attempt >= 3 || !/ERR_NO_BUFFER_SPACE/.test(String(err))) throw err;
        testInfo.annotations.push({ type: 'nav-retry', description: 'ERR_NO_BUFFER_SPACE on the first page load, loaded again' });
      }
    }
    await use(page);
  },

  allow: async ({}, use, testInfo) => {
    const extra: RegExp[] = [];
    (testInfo as any)._allowed = extra;
    await use((match: RegExp, _why: string) => {
      extra.push(match);
    });
  },

  issues: [
    async ({ context, allow }, use, testInfo) => {
      void allow;
      const id = scenarioOf(testInfo.title);
      if (id && KNOWN[id]) testInfo.fail(true, KNOWN[id]);

      const seen: Issue[] = [];
      const pathOf = (u: string) => {
        try {
          const x = new URL(u);
          return x.pathname;
        } catch {
          return u;
        }
      };
      context.on('console', (m) => {
        if (m.type() === 'error') seen.push({ kind: 'console', text: m.text() + ' | ' + pathOf(m.location().url) });
      });
      context.on('weberror', (e) => seen.push({ kind: 'pageerror', text: String(e.error().message) + ' | ' }));
      context.on('requestfailed', (r) => {
        const why = r.failure()?.errorText ?? '';
        // A request the page itself cancelled (navigation, reload, closing a view) is not a failure.
        if (/ERR_ABORTED/.test(why)) return;
        // The one page load that was tried again (see the page fixture) is not an app failure either.
        if (/ERR_NO_BUFFER_SPACE/.test(why) && testInfo.annotations.some((a) => a.type === 'nav-retry')) return;
        seen.push({ kind: 'requestfailed', text: why + ' | ' + pathOf(r.url()) });
      });

      await use();

      const extra: RegExp[] = (testInfo as any)._allowed ?? [];
      const bad = seen.filter((i) => {
        const line = i.kind + ': ' + i.text;
        return !ALLOWED.some((a) => a.match.test(line)) && !extra.some((re) => re.test(line));
      });
      expect(bad.map((b) => b.kind + ': ' + b.text), 'unexpected browser errors').toEqual([]);
    },
    { auto: true }
  ]
});

export { expect };
export type { Page };
