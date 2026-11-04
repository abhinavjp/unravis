import { listCases } from './cases';
import { windowFor } from './discover';
import type { WindowChoice } from './discover';
import { sourcesView, scanIsStale } from './wizardSources.svelte';
import { draftCaseKey, draftSnapshot, onWizardReset, setSources, wipeCaseKey, wizard } from './wizard.svelte';
import { hostsView } from './wizardHosts.svelte';
import { hostProblem } from './zoneChoice';
import { hostNeed } from './fileKinds';
import { importPlan, filesMissing, submitWizard, blankSubmitState, failuresOf, importedCount, isFileSource, isScanSource } from './wizardSubmit';
import type { SubmitApi, SubmitInput, SubmitResult } from './wizardSubmit';
import { wizardSubmitApi } from './wizardSubmitApi';
import { ackTextFor } from './copy/importCopy';
import { sourceKey } from './wizardSources';
import { forgetRequestId } from './createRequest';

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

/** The files held for the file sources, by source key. They are not reactive state: a File is not copied. */
let pickedFiles: Record<string, File> = {};
/** The importer name the person chose for a held file, by source key. A key with none is read by what the server finds in the file. */
let pickedImporters: Record<string, string> = {};
/** The label of the computer the person named for a held file, by source key. A key with none has no computer named. */
let pickedHosts: Record<string, string> = {};
let fileVersion = $state(0);

export const pickedFile = (key: string): File | null => { void fileVersion; return pickedFiles[key] ?? null; };
export const pickedHost = (key: string): string => { void fileVersion; return pickedHosts[key] ?? ''; };
/** Names the computer a held file came from ('' lets it go). Kept apart from the file so the review step can change it. */
export function pickHost(key: string, host: string): void {
  const { [key]: _old, ...others } = pickedHosts;
  pickedHosts = host === '' ? others : { ...others, [key]: host };
  fileVersion++;
}

/** Holds a file for a source (chosen on the sources step, or on the review step as a fallback), or lets it go with `null`. */
export function pickFile(key: string, file: File | null, importer = '', host = ''): void {
  if (file) {
    pickedFiles = { ...pickedFiles, [key]: file };
    const { [key]: _old, ...others } = pickedImporters;
    pickedImporters = importer === '' ? others : { ...others, [key]: importer };
    if (host !== '') pickedHosts = { ...pickedHosts, [key]: host };
  } else {
    const { [key]: _gone, ...rest } = pickedFiles;
    const { [key]: _hint, ...hints } = pickedImporters;
    const { [key]: _host, ...hosts } = pickedHosts;
    pickedFiles = rest;
    pickedImporters = hints;
    pickedHosts = hosts;
  }
  fileVersion++;
}

/** The keys of the sources that have a file held. */
export const heldFileKeys = (): string[] => { void fileVersion; return Object.keys(pickedFiles); };

export function resetCreate(): void {
  forgetRequestId();
  Object.assign(createView, blank());
  pickedFiles = {};
  pickedImporters = {};
  pickedHosts = {};
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
export type Blocker = 'scan-busy' | 'scan-stale' | 'no-ack' | 'files' | 'zone';

/** True when scan sources are chosen but the scan they came from is gone (a new one is running, or it was stopped or failed). */
export const scanMissing = (): boolean => importPlan(wizard.sources).scan > 0 && (sourcesView.phase !== 'done' || sourcesView.scanId === '');

export function blockers(now: number = Date.now()): Blocker[] {
  const plan = importPlan(wizard.sources);
  const out: Blocker[] = [];
  if (scanMissing()) out.push('scan-busy');
  if (plan.scan > 0 && scanIsStale(now)) out.push('scan-stale');
  if (plan.scan + plan.files > 0 && !createView.ack) out.push('no-ack');
  if (filesMissing(wizard.sources, pickedFiles).length > 0) out.push('files');
  if (hostsMissing().length > 0) out.push('zone');
  return out;
}

/** The held files whose kind needs a computer (and a time zone for it) that is not named yet. The review and the Create button wait for them. */
export function hostsMissing(): string[] {
  void fileVersion;
  return wizard.sources.filter((source) => {
    const key = sourceKey(source);
    return pickedFiles[key] !== undefined && hostProblem(source.kind, pickedHosts[key] ?? '', hostsView.entries) !== '';
  }).map(sourceKey);
}

/** The input for the run, built from the draft at the moment of the press. */
export function buildInput(now: number = Date.now(), only?: readonly string[]): SubmitInput {
  const draft = draftSnapshot();
  const files: Record<string, File> = {};
  const importers: Record<string, string> = {};
  const hosts: Record<string, string> = {};
  for (const source of draft.sources) {
    const key = sourceKey(source);
    const file = pickedFiles[key];
    if (file) files[key] = file;
    if (file && pickedImporters[key]) importers[key] = pickedImporters[key];
    if (file && pickedHosts[key] && hostNeed(source.kind) !== 'none') hosts[key] = pickedHosts[key];
  }
  return {
    title: draft.title, envs: draft.envs, sources: draft.sources, importers, hosts,
    window: draft.sources.some(isScanSource) ? windowFor(createView.window, now, null, { from: '', to: '' }) : null,
    scanId: sourcesView.scanId, host: sourcesView.host, files, ackText: ackTextFor(), caseKey: draftCaseKey(), ...(only ? { only } : {})
  };
}

/**
 * Makes the case. Safe to call again: while a run is in flight a second call returns at once, and after a stop (the key locked, a refusal)
 * the same state carries on without making the case again. Resolves with the result.
 */
export async function createNow(api: SubmitApi = wizardSubmitApi, only?: readonly string[]): Promise<SubmitResult | null> {
  if (createView.running) return null;
  if (!caseMade() && blockers().length > 0) return null;
  createView.running = true;
  try {
    const result = await submitWizard(buildInput(Date.now(), only), api, createView.submit);
    createView.result = result;
    // The case could not be made (the reply may have been lost after the server made it): read the names again so the repeat warning is true.
    if (!result.caseId && !result.locked) { createView.titles = null; void loadTitles(); }
    return result;
  } finally {
    createView.running = false;
    // Once the case exists the key was used and is not needed again (a later step never makes the case twice): it is cleared at once.
    if (caseMade()) wipeCaseKey();
  }
}

/** The keys of the file sources whose read did not go through (failed or never started), in list order. A retry sends only these. */
export const failedFileKeys = (): string[] =>
  wizard.sources.filter((source) => isFileSource(source) && ['failed', 'skipped'].includes(createView.submit.imports[sourceKey(source)]?.status ?? '')).map(sourceKey);

/**
 * Takes one file out of a case that is already made: its source goes off the list, the file is let go and its row leaves the run. The case
 * keeps what it has. Then the run is tried again for nothing, so the list of things to try again that the case keeps is brought up to date.
 */
export function leaveOutFile(key: string): void {
  setSources(wizard.sources.filter((source) => sourceKey(source) !== key));
  pickFile(key, null);
  delete createView.submit.imports[key];
  delete createView.submit.tags[key];
  delete createView.submit.audit[key];
}

/** The sources that went through and the ones that did not, for the end screen. */
export const progressSummary = (): { imported: number; total: number; failures: ReturnType<typeof failuresOf> } => ({
  imported: importedCount(createView.submit),
  total: Object.keys(createView.submit.imports).length,
  failures: failuresOf(createView.submit, wizard.sources)
});
