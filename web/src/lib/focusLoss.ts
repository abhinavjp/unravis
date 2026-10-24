/** True when focus sits inside `stage` but not on `message`: that focus is about to be lost when the chart in the stage is hidden. */
export function focusWouldBeLost(stage: Element | null, message: Element, active: Element | null, body: Element | null): boolean {
  return !!stage && !!active && active !== message && active !== body && stage.contains(active);
}
