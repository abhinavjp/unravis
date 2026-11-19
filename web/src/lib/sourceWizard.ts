// Pure helpers of the one source wizard. No state, no imports of components, so they can be tested alone.

export interface RailSource { kind: string; ref: string; label: string }
export interface RailGroup<T extends RailSource> { title: string; items: T[] }

/** The sources grouped by type for the left rail, in the order each type first appears. `titleOf` names the type of one source. */
export function railGroups<T extends RailSource>(sources: readonly T[], titleOf: (source: T) => string): RailGroup<T>[] {
  const out: RailGroup<T>[] = [];
  for (const source of sources) {
    const title = titleOf(source);
    const group = out.find((g) => g.title === title);
    if (group) group.items.push(source);
    else out.push({ title, items: [source] });
  }
  return out;
}

/** Whether the last button may be pressed: something to add, and nothing already running. */
export const canFinish = (adding: boolean, count: number, busy: boolean, closed: boolean): boolean => !busy && !closed && (adding ? count > 0 : true);
