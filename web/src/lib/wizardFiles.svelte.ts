import { pickFile, pickedFile, heldFileKeys } from './wizardCreate.svelte';
import { onWizardReset, setSources, wizard } from './wizard.svelte';
import { addSource, fileSource, nextFileNumber, sourceKey } from './wizardSources';
import type { DraftSource } from './wizardFlow';
import { baseName, importerFor, stageFiles, usableFiles } from './fileKinds';
import type { PanelKind, StagedFile } from './fileKinds';

/**
 * Adds the files of the "Import files" panel (gap G8) to the draft. Each file becomes one source named for its kind, and the file itself is
 * held for the last step, so the review step does not ask for it again. Nothing is sent here: the case does not exist yet.
 * Kept apart from wizardSources.svelte.ts and wizardCreate.svelte.ts so those two never import each other.
 */

/** What the "Import files" panel shows: the rows, whether they came from a folder, and whether files are still being read. */
export interface StagingState {
  list: StagedFile[];
  fromFolder: boolean;
  reading: boolean;
}

export interface Staging {
  readonly state: StagingState;
  /** Starts a read (a drop walk, a zip being opened). Returns the token to give back to current/endRead. */
  beginRead(): number;
  /** False when a Clear, Add or reset ran during the read: that owns the list now, so the read leaves it alone. */
  current(mine: number): boolean;
  /** Ends a read. False when it was cancelled meanwhile. */
  endRead(mine: number): boolean;
  /**
   * Turns files into rows (ids stay unique while the list lives) and appends them. `origin` is the card they were chosen in, and `kind` the
   * kind that card stands for: it is the person's own choice, so it counts as changed unless the file name already suggested it.
   */
  append(files: readonly File[], folder: boolean, origin?: string, kind?: PanelKind): void;
  /** Replaces the rows (a kind or computer changed, a row removed). */
  setList(list: StagedFile[]): void;
  /** Ends every read in flight; the rows stay. */
  cancel(): void;
  /** Ends every read in flight and empties the list. */
  clear(): void;
}

/**
 * The list of files chosen but not yet added. The wizard keeps one for the whole draft (`wizardStaging`), so choosing another add-source form
 * or closing the files form does not lose the rows, the kinds chosen or the computers (gap F37). It is emptied when the draft is thrown away.
 * The drawer of an open case makes its own with `createStaging()`, and ends it when the drawer closes.
 */
export function createStaging(): Staging {
  const state = $state<StagingState>({ list: [], fromFolder: false, reading: false });
  let nextId = 1;
  // A Clear, Add or reset while files are still being read must win: `epoch` marks the cancellation, `pending` counts the reads in flight.
  let epoch = 0;
  let pending = 0;
  const cancel = () => { epoch += 1; pending = 0; state.reading = false; };
  return {
    state,
    beginRead() { pending += 1; state.reading = true; return epoch; },
    current: (mine) => mine === epoch,
    endRead(mine) {
      if (mine !== epoch) return false;
      pending -= 1;
      state.reading = pending > 0;
      return true;
    },
    append(files, folder, origin = '', kind) {
      const rows = stageFiles(files, nextId).map((row) => {
        const chosen = kind !== undefined && row.refusal === '' ? { ...row, choice: kind, changed: kind !== row.suggested } : row;
        return origin === '' ? chosen : { ...chosen, origin };
      });
      nextId += rows.length;
      state.list = [...state.list, ...rows];
      state.fromFolder = folder;
    },
    setList(list) {
      state.list = list;
      if (list.length === 0) state.fromFolder = false;
    },
    cancel,
    clear() { cancel(); state.list = []; state.fromFolder = false; }
  };
}

/** The wizard's own list of staged files. */
export const wizardStaging: Staging = createStaging();
onWizardReset(() => wizardStaging.clear());

export interface AddFilesResult {
  /** Files that became sources. */
  added: number;
  /** Files the draft could not take because the list is full. */
  full: number;
  /** The source keys, in the order of the files that were added. */
  keys: string[];
}

/** The importer name to ask for, or '': only a kind the person set by hand is sent. A suggestion from the file name is left to the server to confirm. */
export const importerHint = (item: Pick<StagedFile, 'choice' | 'changed'>): string => (item.changed ? importerFor(item.choice) : '');

/** Adds every usable row (not refused, with a kind) as a source and holds its file. A refused row or a row left on "Don't use" is skipped. */
export function addStagedFiles(list: readonly StagedFile[]): AddFilesResult {
  const result: AddFilesResult = { added: 0, full: 0, keys: [] };
  let next: DraftSource[] = wizard.sources.map((source) => ({ ...source }));
  const held: Array<{ key: string; item: StagedFile }> = [];
  for (const item of usableFiles(list)) {
    const kind = item.choice as PanelKind;
    // A folder pick carries the whole relative path in item.name; the source label is the file's own name only.
    const source = fileSource(kind, baseName(item.name), nextFileNumber(next, kind));
    const outcome = addSource(next, source);
    if (!outcome.ok) { result.full++; continue; }
    next = outcome.list;
    held.push({ key: sourceKey(source), item });
  }
  if (held.length === 0) return result;
  setSources(next);
  for (const { key, item } of held) { pickFile(key, item.file, importerHint(item), item.host); result.keys.push(key); }
  result.added = held.length;
  return result;
}

/** Lets go of files held for sources that are no longer chosen (after Remove). The memory of a big file should not outlive its source. */
export function releaseOrphanFiles(): void {
  const chosen = new Set(wizard.sources.map(sourceKey));
  for (const key of heldFileKeys()) if (!chosen.has(key)) pickFile(key, null);
}

/** True when a held file exists for the source. */
export const hasHeldFile = (key: string): boolean => pickedFile(key) !== null;
