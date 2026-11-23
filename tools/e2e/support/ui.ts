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
 * Wait until the page has drawn the frames that follow a click, a resize or a theme change, and has no finite animation or transition left (BL-539).
 * Under reduced motion (the setting every spec runs with, and a real user's) the app's rule `transition-duration: .01 ms` still starts a
 * transition on every property that changes, `order` and `flex-basis` of a box that wraps onto its own row among them, and a layout read in the
 * frame the change was made returns the boxes where they came from: the filter panel was 8.84 px higher for about 30 ms after More filters opened.
 * What this does: waits for the web fonts, lets at least four frames pass (the .01 ms transitions are over after the first), and then until no
 * finite animation or transition has been running for two frames in a row; one check before the frames means a long or delayed one is named
 * if the wait runs out (5 s by default, with the animations named in the error). What it does NOT do: it does not see layout moved by script
 * (a ResizeObserver, a Svelte update after a fetch, a late notice) or an animation that only starts in a later frame. A spec whose page can
 * change that way has to wait on its own marker. Other layout specs that read a position right after a click or a resize may reuse it.
 */
export async function layoutSettled(page: Page, timeoutMs = 5_000): Promise<void> {
  await page.evaluate(async (limit) => {
    await document.fonts.ready;
    const frame = () => new Promise<void>((done) => requestAnimationFrame(() => done()));
    const running = () => document.getAnimations().filter((a) => a.playState !== 'finished' && a.playState !== 'idle' && Number.isFinite(a.effect?.getComputedTiming().endTime ?? 0));
    const names = (list: Animation[]) => list.slice(0, 5).map((a) => ((a as CSSTransition).transitionProperty ?? (a as CSSAnimation).animationName ?? a.constructor.name) + ' on ' + ((a.effect as KeyframeEffect | null)?.target as HTMLElement | null)?.tagName?.toLowerCase()).join(', ');
    // In the task of the change itself the .01 ms transitions are still listed; keep their names for the error message.
    let seen = names(running());
    // The first frame applies the change and starts its transitions; the frames after it have drawn the final places.
    await frame();
    await frame();
    const t0 = performance.now();
    let calm = 0;
    while (calm < 2) {
      await frame();
      const left = running();
      calm = left.length === 0 ? calm + 1 : 0;
      if (left.length) seen = names(left);
      if (calm < 2 && performance.now() - t0 > limit) throw new Error('the page is still animating after ' + limit + ' ms: ' + seen);
    }
  }, timeoutMs);
}
