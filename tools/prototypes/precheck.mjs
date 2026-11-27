#!/usr/bin/env node
// Deterministic, cheap pre-check for standalone HTML UI prototypes. Run before any model reviewer.
// Usage: node tools/prototypes/precheck.mjs <file.html> [--out <dir>]
// Exit: 0 = no FAIL, 1 = at least one FAIL, 2 = tooling problem (no Playwright / no browser / bad args).
// Playwright lookup (no network, no repo dependency): env PRECHECK_PLAYWRIGHT (module path), then playwright,
// playwright-core, @playwright/test from this dir, tools/uiaudit|e2e|screens, cwd, then PRECHECK_NODE_MODULES.
// On this PC: PRECHECK_NODE_MODULES=D:\Projects\api-audit-explorer\tools\e2e\node_modules. PW_CHROMIUM = browser exe.
// Output files in --out (default os.tmpdir()/precheck-<basename>): first-screen-1440.png, first-screen-390.png, visible-text.txt.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';

const BUDGET = { firstScreenWords: 120, labelWords: 8, sentenceWords: 20 };
const CLICK_CAP = 80;
const DESKTOP = { width: 1440, height: 900 };
const PHONE = { width: 390, height: 844 };

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..', '..');
const tooling = (msg) => { console.error(`ERROR precheck - ${msg}`); process.exit(2); };

const args = process.argv.slice(2);
const oi = args.indexOf('--out');
const outArg = oi >= 0 ? args.splice(oi, 2)[1] : null;
if (!args[0] || (oi >= 0 && !outArg)) tooling('usage: node precheck.mjs <file.html> [--out <dir>]');
const file = path.resolve(args[0]);
if (!fs.existsSync(file)) tooling(`file not found: ${file}`);
const outDir = path.resolve(outArg || path.join(os.tmpdir(), `precheck-${path.basename(file, path.extname(file))}`));
fs.mkdirSync(outDir, { recursive: true });

// ---- module resolution -------------------------------------------------------------------------
const nm = process.env.PRECHECK_NODE_MODULES;
const bases = [here, ...['uiaudit', 'e2e', 'screens'].map((d) => path.join(root, 'tools', d)), process.cwd()];
if (nm) bases.push(path.basename(nm) === 'node_modules' ? path.dirname(nm) : nm);
const reqFrom = (b) => createRequire(path.join(b, 'noop.js'));
function load(names, how) {
  for (const b of bases) for (const n of names) { try { const m = how(reqFrom(b), n); if (m) return m; } catch { /* next */ } }
  return null;
}
let pw = null;
if (process.env.PRECHECK_PLAYWRIGHT) { try { pw = reqFrom(here)(process.env.PRECHECK_PLAYWRIGHT); } catch { /* fall through */ } }
pw ??= load(['playwright', 'playwright-core', '@playwright/test'], (r, n) => { const m = r(n); return m.chromium ? m : null; });
if (!pw) tooling('Playwright not found (set PRECHECK_PLAYWRIGHT or PRECHECK_NODE_MODULES to a node_modules containing playwright-core)');
const axePath = load(['axe-core/axe.min.js', 'axe-core'], (r, n) => r.resolve(n));

// ---- reporting ---------------------------------------------------------------------------------
const trunc = (s, n = 80) => { s = String(s).replace(/\s+/g, ' ').trim(); return s.length > n ? `${s.slice(0, n - 3)}...` : s; };
const counts = { PASS: 0, WARN: 0, FAIL: 0 };
const say = (lvl, check, detail) => { counts[lvl]++; console.log(`${lvl} ${check} - ${detail}`); };

// ---- in-page helpers (installed before every document; exposed as window.__pc) -------------------
function pageHelpers() {
  const SKIP = /^(SCRIPT|STYLE|NOSCRIPT|TEMPLATE|HEAD|TITLE|META|LINK)$/;
  const CHIP = /(^|[\s_-])(chip|badge|pill|tag)s?($|[\s_-])/i;
  const SEL = 'button,a,[role=button],[role=tab],summary,[onclick]';
  const S = { n: 0, nodes: [], own: false, el: null, sx: 0, sy: 0, href: '' };
  let cache = new WeakMap();
  const fresh = () => { cache = new WeakMap(); };
  const words = (s) => s.split(/\s+/).filter((w) => /[\p{L}\p{N}]/u.test(w));
  const inVP = (r) => r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth;
  const txt = (el) => (el.innerText || el.getAttribute('aria-label') || el.title || el.value || '').replace(/\s+/g, ' ').trim();
  function isVis(el) {
    if (cache.has(el)) return cache.get(el);
    let v = !SKIP.test(el.tagName) && (el.checkVisibility ? el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }) : true);
    if (v) {
      const r = el.getBoundingClientRect();
      if (getComputedStyle(el).overflow !== 'visible' && (r.width <= 1 || r.height <= 1)) v = false; // sr-only style clip
    }
    if (v && el.parentElement && el.parentElement !== document.documentElement) v = isVis(el.parentElement);
    cache.set(el, v);
    return v;
  }
  const blockOf = (el) => { while (el.parentElement && /^(inline|contents)$/.test(getComputedStyle(el).display)) el = el.parentElement; return el; };
  function scan() {
    fresh();
    const tw = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const blocks = new Map(); const view = new Map(); const range = document.createRange(); let inView = 0;
    for (let n; (n = tw.nextNode());) {
      const el = n.parentElement; const t = n.nodeValue;
      if (!el || !isVis(el) || !words(t).length) continue;
      range.selectNodeContents(n);
      const rects = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
      if (!rects.length) continue;
      const b = blockOf(el);
      blocks.set(b, (blocks.get(b) || '') + (n.previousSibling?.nodeName === 'BR' ? '\n' : ' ') + t);
      const hit = rects.filter(inVP).length; let c = 0;
      if (hit === rects.length) c = words(t).length;
      else if (hit) for (const m of t.matchAll(/\S+/g)) { range.setStart(n, m.index); range.setEnd(n, m.index + m[0].length); if (words(m[0]).length && inVP(range.getBoundingClientRect())) c++; }
      if (c) { inView += c; view.set(b, (view.get(b) || 0) + c); }
    }
    const top = [...view.entries()].sort((a, b) => b[1] - a[1])[0];
    return { inView, top: top ? top[0].innerText || '' : '', blocks: [...blocks.values()] };
  }
  function labels() {
    fresh();
    const out = [];
    for (const el of document.querySelectorAll('button,a,[role=button],[role=tab],h1,h2,h3,h4,h5,h6,[class]')) {
      const core = el.matches('button,a,[role=button],[role=tab],h1,h2,h3,h4,h5,h6');
      const cls = el.getAttribute('class') || '';
      const chip = !core && CHIP.test(cls) && ![...el.querySelectorAll('[class]')].some((d) => CHIP.test(d.getAttribute('class')));
      if (!(core || chip) || !isVis(el) || el.getBoundingClientRect().width <= 0) continue;
      const t = txt(el); const n = words(t).length;
      if (n) out.push({ kind: core ? el.tagName.toLowerCase() : 'chip', text: t, n });
    }
    return out;
  }
  const dompath = (el) => { const p = []; for (; el && el !== document.body && el.parentElement; el = el.parentElement) p.push([...el.parentElement.children].indexOf(el)); return p.join('.'); };
  function cand(tested) {
    fresh();
    for (const el of document.querySelectorAll(SEL)) {
      if (el.disabled || !isVis(el)) continue;
      const r = el.getBoundingClientRect();
      if (r.width <= 0 || r.height <= 0) continue;
      const text = txt(el).slice(0, 60); const href = el.getAttribute('href');
      const sig = [el.tagName, text, el.id, href, dompath(el)].join('|');
      if (tested.includes(sig)) continue;
      S.el = el;
      return { sig, text, tag: el.tagName.toLowerCase(), inert: !!el.closest('[data-proto-inert]'), ext: /^(mailto|tel):/i.test(href || '') };
    }
    return null;
  }
  function proc(recs) {
    for (const r of recs) {
      S.n++;
      if (r.type === 'attributes' && r.target === S.el) S.own = true;
      for (const x of r.type === 'childList' ? [r.target, ...r.addedNodes] : [r.target]) if (S.nodes.length < 300) S.nodes.push(x);
    }
  }
  function observe() {
    S.mo = new MutationObserver(proc);
    S.mo.observe(document, { childList: true, attributes: true, characterData: true, subtree: true });
  }
  function reset() { S.mo.takeRecords(); S.n = 0; S.nodes = []; S.own = false; S.sx = scrollX; S.sy = scrollY; S.href = location.href; }
  function result() {
    proc(S.mo.takeRecords()); fresh();
    const visible = S.own || S.nodes.some((x) => { const e = x.nodeType === 1 ? x : x.parentElement; return e && e.isConnected && isVis(e) && inVP(e.getBoundingClientRect()); });
    return { n: S.n, scrolled: scrollX !== S.sx || scrollY !== S.sy, hrefChanged: location.href !== S.href, visible };
  }
  document.addEventListener('fullscreenchange', () => { S.n++; S.own = true; });
  window.__pc = { S, scan, labels, cand, observe, reset, result, overflow: () => ({ sw: document.documentElement.scrollWidth, iw: innerWidth }) };
}

// ---- run ---------------------------------------------------------------------------------------
const url = pathToFileURL(file).href;
let browser;
try {
  browser = await pw.chromium.launch({ headless: true, executablePath: process.env.PW_CHROMIUM || undefined });
} catch (e) {
  tooling(`cannot launch Chromium (${trunc(String(e.message).split('\n')[0], 160)}); set PW_CHROMIUM to a chromium executable`);
}
let exitCode = 0;
try {
  const ctx = await browser.newContext({ viewport: DESKTOP, permissions: ['clipboard-read', 'clipboard-write'] });
  await ctx.addInitScript(pageHelpers);
  await ctx.addInitScript(() => { window.open = () => null; });
  let blockedNav = false;
  await ctx.route(/^https?:/, (r) => { if (r.request().isNavigationRequest()) { blockedNav = true; return r.abort(); } return r.continue(); });
  const page = await ctx.newPage();
  let newPage = false; let dialogSeen = false; let downloaded = false; let where = 'load';
  ctx.on('page', (p) => { if (p !== page) { newPage = true; p.close().catch(() => {}); } });
  page.on('download', () => { downloaded = true; });
  page.on('dialog', (d) => { dialogSeen = true; d.dismiss().catch(() => {}); });
  const errors = new Map();
  const addErr = (t) => { const k = `${where}|${t}`; if (!errors.has(k)) errors.set(k, { where, t }); };
  page.on('console', (m) => { if (m.type() === 'error') addErr(`console.error: ${m.text()}`); });
  page.on('pageerror', (e) => addErr(`uncaught: ${e.message}`));
  const open = async () => { await page.goto(url, { waitUntil: 'load' }); await page.waitForTimeout(300); await page.evaluate(() => window.__pc.observe()); };
  await open();
  const baseUrl = page.url();

  // (1) first-screen words, (2) labels, (3) sentences, artefacts
  const sc = await page.evaluate(() => window.__pc.scan());
  const first = sc.inView;
  say(first > BUDGET.firstScreenWords ? 'FAIL' : 'PASS', 'first-screen-words', `${first} words at ${DESKTOP.width}x${DESKTOP.height} (budget ${BUDGET.firstScreenWords}); densest block: "${trunc(sc.top)}"`);
  const bad = (await page.evaluate(() => window.__pc.labels())).filter((l) => l.n > BUDGET.labelWords);
  if (!bad.length) say('PASS', 'label-length', `all labels within ${BUDGET.labelWords} words`);
  for (const l of bad) say('FAIL', 'label-length', `${l.kind} ${l.n} words (max ${BUDGET.labelWords}): "${trunc(l.text)}"`);
  const sents = sc.blocks.flatMap((b) => b.split(/(?<=[.!?])\s+|\n+/)).map((s) => ({ s: s.trim(), n: s.split(/\s+/).filter((w) => /[\p{L}\p{N}]/u.test(w)).length }))
    .filter((x) => x.n > BUDGET.sentenceWords).sort((a, b) => b.n - a.n);
  if (!sents.length) say('PASS', 'sentence-length', `no sentence over ${BUDGET.sentenceWords} words`);
  for (const x of sents.slice(0, 10)) say('FAIL', 'sentence-length', `${x.n} words (max ${BUDGET.sentenceWords}): "${trunc(x.s)}"`);
  if (sents.length > 10) say('FAIL', 'sentence-length', `... and ${sents.length - 10} more long sentences`);
  fs.writeFileSync(path.join(outDir, 'visible-text.txt'), await page.innerText('body'));
  await page.screenshot({ path: path.join(outDir, 'first-screen-1440.png') });

  // (5) horizontal overflow at 390 and 1440
  const overflow = async (vp, name) => {
    await page.setViewportSize(vp); await page.waitForTimeout(200);
    const o = await page.evaluate(() => window.__pc.overflow());
    say(o.sw > o.iw ? 'FAIL' : 'PASS', `overflow-${vp.width}`, `scrollWidth ${o.sw} vs innerWidth ${o.iw}`);
    if (name) await page.screenshot({ path: path.join(outDir, name) });
  };
  await overflow(PHONE, 'first-screen-390.png');
  await overflow(DESKTOP);

  // (7) contrast (before clicks, so the DOM is pristine)
  if (!axePath) say('WARN', 'contrast', 'skipped (axe-core not installed)');
  else {
    await page.evaluate(fs.readFileSync(axePath, 'utf8'));
    const res = await page.evaluate(() => window.axe.run(document, { runOnly: { type: 'rule', values: ['color-contrast'] } }));
    const nodes = res.violations.flatMap((v) => v.nodes);
    if (!nodes.length) say('PASS', 'contrast', 'no color-contrast violations');
    for (const n of nodes.slice(0, 10)) say('WARN', 'contrast', `ratio ${n.any[0]?.data?.contrastRatio ?? '?'} (needs ${n.any[0]?.data?.expectedContrastRatio ?? '?'}): ${trunc(n.html)}`);
    if (nodes.length > 10) say('WARN', 'contrast', `... and ${nodes.length - 10} more`);
  }

  // (6) click every interactive element
  await page.evaluate(() => window.__pc.reset()); await page.waitForTimeout(400);
  if ((await page.evaluate(() => window.__pc.result())).n > 0) say('WARN', 'click-noise', 'page mutates on its own (timers/animation); dead-click detection may miss dead elements');
  const tested = []; let clicked = 0; let changed = 0; let inert = 0; let dead = 0;
  while (clicked < CLICK_CAP) {
    const c = await page.evaluate((t) => window.__pc.cand(t), tested);
    if (!c) break;
    tested.push(c.sig); clicked++;
    const label = `${c.tag} "${trunc(c.text || '(no text)', 60)}"`;
    where = `click ${label}`;
    const h = await page.evaluateHandle(() => window.__pc.S.el);
    await h.evaluate((e) => e.scrollIntoView({ block: 'center', inline: 'center' }));
    await page.waitForTimeout(60);
    await page.evaluate(() => window.__pc.reset());
    newPage = false; dialogSeen = false; blockedNav = false; downloaded = false;
    let clickErr = null;
    try { await h.click({ timeout: 1500 }); } catch (e) { clickErr = e; }
    await page.waitForTimeout(250);
    if (clickErr) say('WARN', 'click-failed', `${label}: ${trunc(String(clickErr.message).split('\n')[0], 70)}`);
    const r = await page.evaluate(() => window.__pc.result()).catch(() => null);
    const navigated = !r || page.url() !== baseUrl || r.hrefChanged;
    const did = navigated || dialogSeen || downloaded || newPage || blockedNav || c.ext || (r && (r.n > 0 || r.scrolled));
    if (clickErr) { /* reported above; not judged */ } else if (!did) {
      if (c.inert) inert++; else { dead++; say('FAIL', 'click-dead', `nothing changed after clicking ${label}`); }
    } else {
      changed++;
      if (r && !navigated && !dialogSeen && !downloaded && !newPage && !blockedNav && !c.ext && !r.scrolled && !r.visible) say('WARN', 'click-hidden-state', `change not visible in viewport after clicking ${label}`);
    }
    if (navigated) { await page.goto('about:blank'); await open(); }
  }
  where = 'load';
  if (!dead) say('PASS', 'click-dead', `${clicked} clicked, ${changed} changed, ${inert} inert (data-proto-inert)`);
  else say('PASS', 'click-summary', `${clicked} clicked, ${changed} changed, ${inert} inert, ${dead} dead`);

  // (4) console / page errors (load + clicks)
  if (!errors.size) say('PASS', 'console-errors', 'none during load and clicks');
  for (const e of errors.values()) say('FAIL', 'console-errors', `[${trunc(e.where, 40)}] ${trunc(e.t)}`);

  console.log(`SUMMARY first-screen words: ${first} (budget ${BUDGET.firstScreenWords}) | clicked ${clicked} | FAIL ${counts.FAIL} | WARN ${counts.WARN} | out: ${outDir}`);
  exitCode = counts.FAIL ? 1 : 0;
} catch (e) {
  console.error(`ERROR precheck - ${e.stack || e}`);
  exitCode = 2;
} finally {
  await browser.close().catch(() => {});
}
process.exit(exitCode);
