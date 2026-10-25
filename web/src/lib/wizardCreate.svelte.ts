import { listCases } from './cases';
import { windowFor } from './discover';
import type { WindowChoice } from './discover';
import { sourcesView, scanIsStale } from './wizardSources.svelte';
import { draftSnapshot, onWizardReset, wizard } from './wizard.svelte';
import { importPlan, filesMissing, submitWizard, blankSubmitState, failuresOf, importedCount, isScanSource } from './wizardSubmit';
import type { SubmitApi, SubmitInput, SubmitResult } from './wizardSubmit';
import { wizardSubmitApi } from './wizardSubmitApi';
import { ackTextFor } from './copy/importCopy';
import { sourceKey } from './wizardSources';

/**
 * The state of the last step of the create wizard (TSK-19): the choices made on the review (how far back to read, the ticked notice, the
 * files picked), and the progress of the run. In memory only, like the draft; `resetWizard` clears it. The run itself is in wizardSubmit.ts.
 */

/** The periods the review offers for the logs of this computer. A case being made has no incident window yet, so that choice is not here. */
export const REVIEW_WINDOWS: readonly WindowChoice[] = ['1h', '6h', '24h', '7d'];

export interface CreateView {
  /** How far back to read the logs of this computer. */
  window: WindowChoice;
  /** The notice is ticked. */
  ack: boolean;
  /** The run: the case, each step, each source. Never reset by Back; only by resetWizard. */
  submit: ReturnType<typeof blankSubmitState>;
  /** A run is in flight (set at once, before the first await, so a second click does nothing). */
  running: boolean;
  /** The end of the last run, or null before one. */
  result: SubmitResult | null;
  /** The titles of the cases that exist, to warn about a repeat. Null until read. */
  titles: string[] | null;
}

const blank = (): CreateView => ({ window: '24h', ack: false, submit: blankSubmitState(), running: false, result: null, titles: null });

export const createView = $state<CreateView>(blank());

/** The files picked on the review, by source key. They are not reactive state: a File is not copied. */
let pickedFiles: Record<string, File> = {};
let fileVersion = $state(0);

export const pickedFile = (key: string): File | null => { void fileVersion; return pickedFiles[key] ?? null; };
export function pickFile(key: string, file: File | null): void {
  if (file) pickedFiles = { ...pickedFiles, [key]: file };
  else { const { [key]: _gone, ...rest } = pickedFiles; pickedFiles = rest; }
  fileVersion++;
}

export function resetCreate(): void {
  Object.assign(createView, blank());
  pickedFiles = {};
  fileVersion++;
}
onWizardReset(resetCreate);

export const chooseWindow = (choice: WindowChoice): void => { createView.window = choice; };
export const setAck = (on: boolean): void => { createView.ack = on; };

/** Reads the titles of the cases on this computer, once, so the review can say a name repeats. A failure (a locked key) just leaves it unknown. */
export async function loadTitles(): Promise<void> {
  if (createView.titles !== null) return;
  try { createView.titles = (await listCases()).map((c) => c.title.trim().toLowerCase()); } catch { createView.titles = null; }
}

export const titleRepeats = (title: string): boolean => createView.titles !== null && createView.titles.includes(title.trim().toLowerCase());

/** True once a case exists on the server for this draft. From then on the review is closed: the person follows the run instead. */
export const caseMade = (): boolean => createView.submit.caseId !== '';

/** What stops "Create case" from being pressed, in the order the screen shows it. Empty when nothing does. */
export type Blocker = 'scan-busy' | 'scan-stale' | 'no-ack' | 'files';

/** True when scan sources are chosen but the scan they came from is gone (a new one is running, or it was stopped or failed). */
export const scanMissing = (): boolean => importPlan(wizard.sources).scan > 0 && (sourcesView.phase !== 'done' || sourcesView.scanId === '');

export function blockers(now: number = Date.now()): Blocker[] {
  const plan = importPlan(wizard.sources);
  const out: Blocker[] = [];
  if (scanMissing()) out.push('scan-busy');
  if (plan.scan > 0 && scanIsStale(now)) out.push('scan-stale');
  if (plan.scan + plan.files > 0 && !createView.ack) out.push('no-ack');
  if (filesMissing(wizard.sources, pickedFiles).length > 0) out.push('files');
  return out;
}

/** The input for the run, built from the draft at the moment of the press. */
export function buildInput(now: number = Date.now()): SubmitInput {
  const draft = draftSnapshot();
  const files: Record<string, File> = {};
  for (const source of draft.sources) { const file = pickedFiles[sourceKey(source)]; if (file) files[sourceKey(source)] = file; }
  return {
    title: draft.title, envs: draft.envs, sources: draft.sources,
    window: draft.sources.some(isScanSource) ? windowFor(createView.window, now, null, { from: '', to: '' }) : null,
    scanId: sourcesView.scanId, host: sourcesView.host, files, ackText: ackTextFor()
  };
}

/**
 * Makes the case. Safe to call again: while a run is in flight a second call returns at once, and after a stop (the key locked, a refusal)
 * the same state carries on without making the case again. Resolves with the result.
 */
export async function createNow(api: SubmitApi = wizardSubmitApi): Promise<SubmitResult | null> {
  if (createView.running) return null;
  if (!caseMade() && blockers().length > 0) return null;
  createView.running = true;
  try {
    const result = await submitWizard(buildInput(), api, createView.submit);
    createView.result = result;
    // The case could not be made (the reply may have been lost after the server made it): read the names again so the repeat warning is true.
    if (!result.caseId && !result.locked) { createView.titles = null; void loadTitles(); }
    return result;
  } finally {
    createView.running = false;
  }
}

/** The sources that went through and the ones that did not, for the end screen. */
export const progressSummary = (): { imported: number; total: number; failures: ReturnType<typeof failuresOf> } => ({
  imported: importedCount(createView.submit),
  total: Object.keys(createView.submit.imports).length,
  failures: failuresOf(createView.submit, wizard.sources)
});
