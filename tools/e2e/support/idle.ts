// A probe for "what does the page do while nobody is looking" (BL-429).
//   installIdleProbe(page)   before the page loads: counts the timers that fire, keeps the list of live intervals, and lets the test
//                            say the tab is hidden or visible (the real visibilityState cannot be changed in headless Chromium, so the
//                            page is told the same way the browser tells it: document.visibilityState, document.hidden and a
//                            visibilitychange event).
//   setVisible(page, bool)   hides or shows the tab.
//   watchRequests(page)      counts the requests the page makes, by path.
//   cpu(page)                task and script time from the browser (CDP Performance metrics), in milliseconds.
import type { CDPSession, Page } from '@playwright/test';

export interface IdleState {
  fired: number;
  raf: number;
  intervals: number[];
  timeouts: number;
  visible: boolean;
}

const PROBE = () => {
  const w = window as any;
  const state = { fired: 0, raf: 0, intervals: new Map<number, number>(), timeouts: new Set<number>(), visible: true };
  w.__idle = state;
  const realSetInterval = w.setInterval.bind(w);
  const realSetTimeout = w.setTimeout.bind(w);
  const realClearInterval = w.clearInterval.bind(w);
  const realClearTimeout = w.clearTimeout.bind(w);
  const realRaf = w.requestAnimationFrame.bind(w);
  w.setInterval = (fn: any, ms?: number, ...rest: any[]) => {
    const id = realSetInterval(() => { state.fired++; if (typeof fn === 'function') fn(...rest); }, ms);
    state.intervals.set(id, Number(ms) || 0);
    return id;
  };
  w.clearInterval = (id: number) => { state.intervals.delete(id); realClearInterval(id); };
  w.setTimeout = (fn: any, ms?: number, ...rest: any[]) => {
    const id = realSetTimeout(() => { state.timeouts.delete(id); state.fired++; if (typeof fn === 'function') fn(...rest); }, ms);
    state.timeouts.add(id);
    return id;
  };
  w.clearTimeout = (id: number) => { state.timeouts.delete(id); realClearTimeout(id); };
  w.requestAnimationFrame = (fn: FrameRequestCallback) => realRaf((t: number) => { state.raf++; fn(t); });
};

const VISIBILITY = () => {
  const w = window as any;
  const state = w.__idle ?? { visible: true };
  let shown = 'visible';
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => shown });
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => shown !== 'visible' });
  w.__setVisible = (visible: boolean) => {
    shown = visible ? 'visible' : 'hidden';
    state.visible = visible;
    document.dispatchEvent(new Event('visibilitychange'));
  };
};

/** Only the visibility switch (no timer counting): for tests that also drive the page clock. */
export async function installVisibility(page: Page): Promise<void> {
  await page.addInitScript(VISIBILITY);
}

/** Timer counters and the visibility switch. */
export async function installIdleProbe(page: Page): Promise<void> {
  await page.addInitScript(PROBE);
  await page.addInitScript(VISIBILITY);
}

export async function setVisible(page: Page, visible: boolean): Promise<void> {
  await page.evaluate((v) => (window as any).__setVisible(v), visible);
}

export async function idleState(page: Page): Promise<IdleState> {
  return page.evaluate(() => {
    const s = (window as any).__idle;
    return { fired: s.fired, raf: s.raf, intervals: [...s.intervals.values()].sort((a: number, b: number) => a - b), timeouts: s.timeouts.size, visible: s.visible };
  });
}

/** Counts the page's own requests, by method and path (the query string is dropped). */
export function watchRequests(page: Page) {
  const counts = new Map<string, number>();
  const on = (r: { url(): string; method(): string }) => {
    let path = r.url();
    try { path = new URL(r.url()).pathname; } catch { /* keep the raw text */ }
    if (!path.startsWith('/api/')) return;
    const key = r.method() + ' ' + path;
    counts.set(key, (counts.get(key) ?? 0) + 1);
  };
  page.on('request', on);
  return {
    reset: () => counts.clear(),
    total: () => [...counts.values()].reduce((a, b) => a + b, 0),
    count: (key: string) => counts.get(key) ?? 0,
    by: () => Object.fromEntries([...counts.entries()].sort()),
    stop: () => page.off('request', on)
  };
}

export async function cpuSession(page: Page): Promise<CDPSession> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Performance.enable');
  return cdp;
}

export async function cpu(cdp: CDPSession): Promise<{ taskMs: number; scriptMs: number }> {
  const { metrics } = await cdp.send('Performance.getMetrics');
  const pick = (name: string) => metrics.find((m) => m.name === name)?.value ?? 0;
  return { taskMs: pick('TaskDuration') * 1000, scriptMs: pick('ScriptDuration') * 1000 };
}
