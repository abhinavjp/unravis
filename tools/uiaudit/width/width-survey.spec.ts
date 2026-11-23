// Width survey (not part of the audit gate): opens every screen at five window widths and records how much of the
// window the content really uses, so a narrow centred column shows up as a number. Steps run as in the audit; the `after` steps of a screen are not run. Writes width-report/data.json and a picture per screen and width.
// Run (needs the same programs as the audit; see ../README.md for UNRAVIS_EXE and friends):
//   cd tools/uiaudit && WIDTH_OUT=/tmp/width-run UIAUDIT_ONLY=p1-case-home,p1-health WIDTH_LIST=1920,1440 npx playwright test -c width/playwright.width.config.ts --trace off
// Leave UIAUDIT_ONLY unset for every screen (135 screens x 5 widths, about 25 minutes). Not part of the audit gate.
import { test } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { runSteps, expandMacros, type Screen, type Env, type Step } from '../lib/steps';

const root = join(__dirname, '..');
const outDir = process.env.WIDTH_OUT || join(root, 'width-report');
mkdirSync(join(outDir, 'data'), { recursive: true });
mkdirSync(join(outDir, 'shots'), { recursive: true });
const base: Screen[] = JSON.parse(readFileSync(join(root, '..', 'screens', 'screens.json'), 'utf8')).screens;
const extraFile: { macros?: Record<string, Step[]>; screens: Screen[] } = JSON.parse(readFileSync(join(root, 'extra-screens.json'), 'utf8'));
const extra: Screen[] = extraFile.screens.map((s) => ({ ...s, steps: expandMacros(s.steps, extraFile.macros ?? {}), after: s.after && expandMacros(s.after, extraFile.macros ?? {}) }));
const only = (process.env.UIAUDIT_ONLY ?? '').split(',').map((s) => s.trim()).filter(Boolean);
const screens = [...base, ...extra].filter((s) => !only.length || only.includes(s.id));

// Union of the boxes of everything that shows something (text, picture, chart, control, table), inside the main area.
// Full-bleed backgrounds do not count, so a wide empty card does not hide a narrow column of text.
const measure = () => {
  const rootEl = (document.querySelector('#main-content, main, .stage') ?? document.body) as HTMLElement;
  const rr = rootEl.getBoundingClientRect();
  const vw = document.documentElement.clientWidth;
  let minX = Infinity, maxX = -Infinity, n = 0;
  const visible = (el: Element) => {
    const s = getComputedStyle(el);
    if (s.visibility === 'hidden' || s.display === 'none' || Number(s.opacity) === 0) return false;
    const r = el.getBoundingClientRect();
    return r.width > 2 && r.height > 2 && r.bottom > 0;
  };
  const shows = (el: Element) => {
    if (/^(IMG|CANVAS|SVG|TABLE|INPUT|SELECT|TEXTAREA|BUTTON|VIDEO)$/i.test(el.tagName)) return true;
    for (const c of Array.from(el.childNodes)) if (c.nodeType === 3 && (c.textContent ?? '').trim()) return true;
    return false;
  };
  rootEl.querySelectorAll('*').forEach((el) => {
    if (!visible(el) || !shows(el)) return;
    const r = el.getBoundingClientRect();
    // text boxes are as wide as their block; measure the text itself for those
    let l = r.left, rt = r.right;
    if (!/^(IMG|CANVAS|SVG|TABLE|INPUT|SELECT|TEXTAREA|BUTTON|VIDEO)$/i.test(el.tagName)) {
      const range = document.createRange();
      range.selectNodeContents(el);
      const rects = Array.from(range.getClientRects()).filter((x) => x.width > 0);
      if (rects.length) { l = Math.min(...rects.map((x) => x.left)); rt = Math.max(...rects.map((x) => x.right)); }
    }
    minX = Math.min(minX, l); maxX = Math.max(maxX, rt); n++;
  });
  // the narrowest column that holds most of the content: the widest block-level container direct under the stage
  let col = 0;
  const inner = rootEl.querySelector('.stage-inner') as HTMLElement | null;
  const first = (inner ?? rootEl).firstElementChild as HTMLElement | null;
  if (first) col = Math.round(first.getBoundingClientRect().width);
  return { vw, rootW: Math.round(rr.width), minX: Math.round(minX), maxX: Math.round(maxX), used: n ? Math.round(maxX - minX) : 0, n, firstChildW: col,
    hscroll: document.documentElement.scrollWidth > vw + 1 };
};

for (const screen of screens) {
  test(screen.id, async ({ page }, info) => {
    const width = Number(info.project.name.replace(/\D/g, ''));
    const env: Env = {
      base: process.env.UNRAVIS_BASE_URL!, token: process.env.UNRAVIS_TOKEN!,
      setupUrl: process.env.UNRAVIS_SETUP_URL ?? '', setupKey: process.env.UNRAVIS_SETUP_KEY ?? '',
      apps: JSON.parse(process.env.UNRAVIS_APPS ?? '{}'), demoNow: new Date(process.env.UNRAVIS_DEMO_NOW ?? '2026-10-20T09:00:00Z')
    };
    const key = screen.id + '__' + width;
    const write = (o: object) => writeFileSync(join(outDir, 'data', key + '.json'), JSON.stringify({ id: screen.id, width, ...o }));
    if (screen.retired) return write({ status: 'skipped' });
    if (screen.app === 'setup' && !env.setupUrl) return write({ status: 'unreachable' });
    if (screen.app && screen.app !== 'setup' && !env.apps[screen.app]) return write({ status: 'unreachable' });
    // One shared demo serves every screen and width: end a held setup run, and put the list of computers back after a screen that changes it (as audit.spec.ts does).
    if (screen.app === 'setup') {
      const h = { 'X-Setup-Key': env.setupKey };
      await fetch(env.setupUrl + '/api/cancel', { method: 'POST', headers: h }).catch(() => undefined);
      for (let i = 0; i < 50; i++) {
        const st = (await (await fetch(env.setupUrl + '/api/progress?since=0', { headers: h })).json().catch(() => ({ running: false }))) as { running: boolean };
        if (!st.running) break;
        await new Promise((done) => setTimeout(done, 100));
      }
    }
    const hosts = (method: string, body?: string) => fetch(env.base + '/api/hosts', { method, headers: { 'X-Audit-Token': env.token, 'Content-Type': 'application/json' }, body }).then((r) => (r.ok ? r.text() : null)).catch(() => null);
    const hostsBefore = screen.restoreHosts ? await hosts('GET') : null;
    await page.addInitScript(() => { try { localStorage.setItem('unravis.theme', 'dark'); } catch { /* ignore */ } });
    await page.clock.setFixedTime(env.demoNow);
    try {
      try { await runSteps(page, screen.steps, env, screen.app); } catch (e) { return write({ status: 'unreachable', reason: String((e as Error).message).slice(0, 200) }); }
      await page.evaluate(() => document.fonts.ready);
      await page.mouse.move(0, 0);
      await page.waitForTimeout(400);
      const m = await page.evaluate(measure);
      await page.screenshot({ path: join(outDir, 'shots', key + '.png') });
      write({ status: 'ok', ...m });
    } finally {
      if (hostsBefore !== null) await hosts('PUT', hostsBefore);
    }
  });
}
