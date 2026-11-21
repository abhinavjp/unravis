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

/** The address a found Mongo/SQL row opens its form with. The scan finds the service and its logs, not its port, so this is the usual local address; the owner can edit it. */
export function connectPrefill(kind: 'mongo' | 'sql'): string {
  return kind === 'mongo' ? 'mongodb://localhost:27017' : 'localhost';
}

/**
 * The one confirmation line after sources join the list, for every kind (Files, Mongo, SQL, Redis, Blackbox, found on this PC) and in both
 * the new-case and the add-to-a-case wizard. `before` and `after` are the source keys of the list; `labelOf` names one by key.
 * Returns '' when nothing was added (a removal or no change). The Blackbox suffix is there because that recorder is saved at once.
 */
export function addedLine(before: readonly string[], after: ReadonlyArray<{ key: string; kind: string; label: string }>, text: { one: (label: string) => string; many: (n: number) => string; recorder: string }): string {
  const known = new Set(before);
  const fresh = after.filter((a) => !known.has(a.key));
  if (fresh.length === 0) return '';
  const line = fresh.length === 1 ? text.one(fresh[0].label) : text.many(fresh.length);
  return fresh.some((f) => f.kind === 'blackbox') ? line + ' ' + text.recorder : line;
}
