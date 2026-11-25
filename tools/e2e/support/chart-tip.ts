// BL-323: ECharts tooltips are rich text, drawn on the chart canvas (a zrender Text), not as HTML in the page, so a spec cannot find them with a locator.
// `init` of web/src/lib/charts.ts leaves the chart on its container (`container.__chart`); the tooltip is a top-level Text of the chart's zrender.
// This reads what the tooltip says right now: the text that is drawn, with the rich-text tags removed and every run of white space made one space.
import type { Page } from '@playwright/test';

/** The text of the tooltip that is showing on the chart in `selector` (the element ECharts was started on, or one that holds it), or '' when none shows. */
export async function drawnTip(page: Page, selector: string): Promise<string> {
  return page.evaluate((sel) => {
    const root = document.querySelector(sel) as (HTMLElement & { __chart?: unknown }) | null;
    if (!root) return '';
    type Zr = { storage: { getRoots(): Array<{ type?: string; ignore?: boolean; style?: { text?: string } }> } };
    const holder = root.__chart ? root : ([...root.querySelectorAll('*')] as Array<HTMLElement & { __chart?: unknown }>).find((el) => el.__chart);
    const chart = holder?.__chart as { getZr(): Zr } | undefined;
    if (!chart) return '';
    const shown = chart.getZr().storage.getRoots().filter((el) => el.type === 'text' && !el.ignore && typeof el.style?.text === 'string' && el.style.text.length > 0);
    return shown.map((el) => el.style!.text!.replace(/\{[A-Za-z0-9_-]+\|([^}]*)\}/g, '$1').replace(/\s+/g, ' ').trim()).join(' | ');
  }, selector);
}

/** Moves the pointer over the plot of the chart in `selector` (from `from` px to `width - to` px in steps of `step`) until the tooltip text matches `wanted`; returns it, or fails with the last text seen. */
export async function sweepTip(page: Page, selector: string, wanted: RegExp, opts: { from?: number; to?: number; step?: number } = {}): Promise<string> {
  const { from = 56, to = 48, step = 3 } = opts;
  await page.locator(selector).scrollIntoViewIfNeeded();
  const box = (await page.locator(selector).boundingBox())!;
  let last = '';
  for (let x = from; x < box.width - to; x += step) {
    await page.mouse.move(box.x + x, box.y + box.height / 2);
    const text = await drawnTip(page, selector);
    if (text) last = text;
    if (wanted.test(text)) return text;
  }
  throw new Error('no tooltip matched ' + wanted + '; the last one was: ' + last);
}
