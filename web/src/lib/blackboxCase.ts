import type { SourceTagInput } from './caseTagsApi';

// Using a Blackbox in a case. The case keeps one source tag of kind "blackbox" whose label is the Blackbox name and nothing else:
// no address, no folder, no read key. Step 9 finds the Blackbox in the saved list by that name and reads it on this PC.

export const BLACKBOX_TAG_KIND = 'blackbox';

export interface CaseTag { id: number; kind: string; label: string }

/** The tag a case holds for a Blackbox: the name as the label, every other field empty. */
export function blackboxTag(name: string): SourceTagInput {
  return { importId: 0, kind: BLACKBOX_TAG_KIND, label: name.trim(), env: '', portal: '', api: '', classification: '' };
}

/** What the case already says about this Blackbox: not used, used (with the tag to remove), or another Blackbox is used instead. */
export type CaseUse = { state: 'none' } | { state: 'this'; tagId: number } | { state: 'other'; name: string };

const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();

export function caseUse(tags: readonly CaseTag[], name: string): CaseUse {
  const boxes = tags.filter((t) => t.kind === BLACKBOX_TAG_KIND);
  const mine = boxes.find((t) => same(t.label, name));
  if (mine) return { state: 'this', tagId: mine.id };
  if (boxes.length > 0) return { state: 'other', name: boxes[0].label };
  return { state: 'none' };
}

/** The three calls the control makes. The real ones are in blackboxCaseApi.ts; tests give fakes. */
export interface CaseUseApi {
  listTags(caseId: string): Promise<CaseTag[]>;
  postTag(caseId: string, tag: SourceTagInput): Promise<CaseTag>;
  removeTag(caseId: string, tagId: number): Promise<void>;
}

/** Adds the Blackbox to the case. A case reads one Blackbox, so another one already in use is refused. Used twice does nothing twice. */
export async function useInCase(api: CaseUseApi, caseId: string, name: string): Promise<CaseUse> {
  const now = caseUse(await api.listTags(caseId), name);
  if (now.state !== 'none') return now;
  const added = await api.postTag(caseId, blackboxTag(name));
  return { state: 'this', tagId: added.id };
}

/** Takes the Blackbox out of the case. Only the tag goes; the Blackbox and its recordings are not touched. */
export async function stopUsingInCase(api: CaseUseApi, caseId: string, name: string): Promise<CaseUse> {
  const now = caseUse(await api.listTags(caseId), name);
  if (now.state === 'this') await api.removeTag(caseId, now.tagId);
  return { state: 'none' };
}
