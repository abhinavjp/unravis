import { blackboxApi, BlackboxError } from './blackbox';
import type { ThisPc } from './blackbox';

/**
 * Gives back the name of the list entry that stands for this PC's recorder, adding it only when no entry does.
 * The Blackbox panel may already list the same recorder by its address, so adding it again by folder would show it twice.
 */
export async function recorderEntryName(pc: Pick<ThisPc, 'listed' | 'listId'>, folder: string, name: string): Promise<string> {
  if (pc.listed && pc.listId) {
    try { const list = await blackboxApi.list(); const hit = list.items.find((i) => i.id === pc.listId); if (hit) return hit.name; } catch { /* fall through to a plain add */ }
  }
  try { return (await blackboxApi.add({ name, location: folder, key: '' })).name; }
  catch (e) { if (e instanceof BlackboxError && e.refusal.existing) return e.refusal.existing.name; throw e; }
}
