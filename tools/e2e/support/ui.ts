// Small helpers for the page: how to reach the start screen and a case.
import { expect, type Page } from '@playwright/test';

/** Go to the start screen (the explorer opens first; Home is in the header). */
export async function goHome(page: Page): Promise<void> {
  // A fresh window can already be on the start screen, where "All cases" does not exist.
  if (!(await page.locator('#start-title').isVisible())) {
    await page.getByRole('button', { name: /^All cases/ }).first().click();
  }
  await expect(page.locator('#start-title')).toBeVisible();
}

/** Open a case from the start screen by its title. */
export async function openCase(page: Page, title: string): Promise<void> {
  await page.getByRole('button', { name: 'Open case ' + title }).click();
  await expect(page.getByRole('button', { name: /^All cases/ })).toBeVisible();
}

/** The "Files" tile of the source wizard (name is "Files IIS, app logs, exports"). */
export const filesTile = (page: Page) => page.getByRole('button', { name: /^Files\b/ });

/**
 * Wait until the page has finished laying itself out after a click, a resize or a theme change (BL-539).
 * Under reduced motion (the setting every spec runs with, and a real user's) the app's rule `transition-duration: .01 ms` still starts a
 * transition on every property that changes, `order` and `flex-basis` of a box that wraps onto its own row among them, and a read taken in the
 * frame the change was made sees the box where it came from: the filter panel 8.84 px higher for about 30 ms after More filters opened, a row
 * of the sub bar one frame wider than the window after a resize. This waits for the web fonts, lets the frame that starts the transitions
 * pass, and then waits until no finite animation or transition is running for two frames in a row. It never gives up quietly: a page that
 * keeps animating fails here, with the animations named.
 */
export async function layoutSettled(page: Page, timeoutMs = 5_000): Promise<void> {
  await page.evaluate(async (limit) => {
    await document.fonts.ready;
    const frame = () => new Promise<void>((done) => requestAnimationFrame(() => done()));
    const running = () => document.getAnimations().filter((a) => a.playState !== 'finished' && a.playState !== 'idle' && Number.isFinite(a.effect?.getComputedTiming().endTime ?? 0));
    // The first frame applies the change and starts its transitions; only the frames after it can show them running.
    await frame();
    await frame();
    const t0 = performance.now();
    let calm = 0;
    while (calm < 2) {
      await frame();
      const left = running();
      calm = left.length === 0 ? calm + 1 : 0;
      if (performance.now() - t0 > limit) {
        throw new Error('the page is still animating after ' + limit + ' ms: ' + left.slice(0, 5).map((a) => ((a as CSSTransition).transitionProperty ?? (a as CSSAnimation).animationName ?? a.constructor.name) + ' on ' + ((a.effect as KeyframeEffect | null)?.target as HTMLElement | null)?.tagName?.toLowerCase()).join(', '));
      }
    }
  }, timeoutMs);
}
