import { CaseApiError } from './cases';
import { MAX_FINDINGS_PER_CALL, outcomeFromError, windowProblem } from './discover';
import type { DiscoverImportRequest, JobRef, RowOutcome, ScanHost, TimeWindow } from './discover';
import type { SourceTagInput } from './caseTagsApi';
import { parseDatabaseRef, sourceKey } from './wizardSources';
import type { DraftSource } from './wizardFlow';

/**
 * The last step of the create wizard (TSK-19): make the case, write its environments and source tags, then import what was chosen.
 * Pure orchestration: every call goes through the `SubmitApi` it is given, so a test passes a fake and the screen passes the real routes.
 *
 * The rules this file keeps:
 *  - Nothing is called until `submitWizard` runs. Leaving the wizard before that creates nothing on the server.
 *  - Once the case exists it is never deleted here. A step that fails is recorded with its reason and the next steps still run.
 *  - One failed import does not stop the others.
 *  - A locked master key (HTTP 423) stops the run where it is, keeps everything that is done, and returns `locked`. Calling `submitWizard`
 *    again with the same state carries on from there; it never creates the case a second time and never repeats a tag that was written.
 *  - A second call while one is running returns the running one (a double click makes one case).
 *  - Nothing here holds a password, a connection string or a path. A Mongo connection is named by an id the api adapter looks up.
 */

export type StepStatus = 'waiting' | 'running' | 'done' | 'failed' | 'skipped';

export interface StepState { status: StepStatus; reason: string }

/** One source that will be imported: a scan finding, or a file the person picked on the review step. */
export interface ImportRow extends StepState {
  label: string;
  /** 0 to 100 while the server reads it, or null when it does not know. */
  percent: number | null;
  outcome: RowOutcome | null;
}

export interface SubmitState {
  caseId: string;
  caseTitle: string;
  /** Why the case could not be made (no case exists then). */
  createError: string;
  envs: StepState;
  tags: Record<string, StepState>;
  audit: Record<string, StepState>;
  imports: Record<string, ImportRow>;
  /** The master key was locked in the middle of the run. The run stopped; call submitWizard again after unlocking. */
  locked: boolean;
  /** Every step has been tried (some may have failed). */
  finished: boolean;
}

export const blankSubmitState = (): SubmitState => ({
  caseId: '', caseTitle: '', createError: '', envs: { status: 'waiting', reason: '' }, tags: {}, audit: {}, imports: {}, locked: false, finished: false
});

/** What the review step hands over. Titles, environments and tags are already cleaned by the draft. */
export interface SubmitInput {
  title: string;
  envs: readonly string[];
  sources: readonly DraftSource[];
  /** The time slice for the scan findings. Needed only when a scan finding is chosen. */
  window: TimeWindow | null;
  scanId: string;
  host: ScanHost | null;
  /** The files picked on the review step, by source key. */
  files: Readonly<Record<string, File>>;
  /** The notice the person ticked, sent with every import. */
  ackText: string;
}

/** The calls the run makes. The real ones are in wizardSubmitApi.ts. */
export interface SubmitApi {
  createCase(title: string): Promise<{ id: string }>;
  putEnvironments(caseId: string, envs: readonly string[]): Promise<unknown>;
  postSourceTag(caseId: string, tag: SourceTagInput): Promise<unknown>;
  /** Tests the held MongoDB connection again so the server accepts a classification of its databases. */
  testConnection(connectionId: string): Promise<unknown>;
  /** Makes a classified audit database a source of the analysis. */
  addAuditSource(connectionId: string, database: string): Promise<unknown>;
  confirmHost?(host: ScanHost): Promise<unknown>;
  startImports(caseId: string, request: DiscoverImportRequest): Promise<JobRef[]>;
  /** Follows one job to its end. Resolves with the outcome; never throws for a refusal. */
  watchJob(caseId: string, ref: JobRef, onProgress: (percent: number | null) => void): Promise<RowOutcome>;
  /** Detects the file's type, then keeps it. Resolves with the outcome; never throws for a refusal. */
  importFile(caseId: string, file: File, onProgress: (percent: number | null) => void): Promise<RowOutcome>;
}

export type FailureStep = 'case' | 'environments' | 'tag' | 'audit-source' | 'import';

export interface Failure { step: FailureStep; key: string; label: string; reason: string }

export interface SubmitResult {
  caseId: string;
  locked: boolean;
  finished: boolean;
  /** Sources whose import is finished (added, or already in the case). */
  imported: number;
  failures: Failure[];
}

/* ------------------------------------------------------------------ what a source needs */

/** Kinds that are read from a file the person picks here. */
const FILE_SOURCE_KINDS: readonly string[] = ['iis-logs', 'app-log', 'hangfire-csv', 'redis-slowlog-csv', 'file'];

export const isFileSource = (source: Pick<DraftSource, 'kind'>): boolean => FILE_SOURCE_KINDS.includes(source.kind);
export const isScanSource = (source: Pick<DraftSource, 'kind'>): boolean => source.kind === 'discover';
/** A source that is imported (the others, such as a SQL Server or Redis address, are named and tagged only). */
export const isImportable = (source: Pick<DraftSource, 'kind'>): boolean => isScanSource(source) || isFileSource(source);

/** How many sources of each sort the review will act on. */
export function importPlan(sources: readonly DraftSource[]): { scan: number; files: number; tagOnly: number } {
  let scan = 0, files = 0, tagOnly = 0;
  for (const source of sources) {
    if (isScanSource(source)) scan++;
    else if (isFileSource(source)) files++;
    else tagOnly++;
  }
  return { scan, files, tagOnly };
}

/** The file sources that still have no file picked. A case cannot read them until the person picks one. */
export const filesMissing = (sources: readonly DraftSource[], files: Readonly<Record<string, unknown>>): DraftSource[] =>
  sources.filter((source) => isFileSource(source) && !files[sourceKey(source)]);

/**
 * The four numbers of the review line. Portals and APIs count the distinct names (a blank is not a name). A shared source has no portal
 * named, so it serves them all.
 */
export function reviewCounts(envs: readonly string[], sources: readonly Pick<DraftSource, 'portal' | 'api'>[]): { environments: number; portals: number; apis: number; shared: number } {
  const distinct = (values: string[]): number => new Set(values.filter((v) => v !== '').map((v) => v.toLowerCase())).size;
  return { environments: envs.length, portals: distinct(sources.map((s) => s.portal)), apis: distinct(sources.map((s) => s.api)), shared: sources.filter((s) => s.portal === '').length };
}

/* ------------------------------------------------------------------ small helpers */

const reasonOf = (error: unknown): string => (error instanceof CaseApiError ? error.reason || 'failed' : 'unreachable');
const isLocked = (error: unknown): boolean => error instanceof CaseApiError && error.status === 423;
const isAbort = (error: unknown): boolean => error instanceof DOMException && error.name === 'AbortError';

class Locked extends Error {}

async function guarded<T>(call: () => Promise<T>): Promise<T> {
  try { return await call(); } catch (error) { if (isLocked(error)) throw new Locked('locked'); throw error; }
}

const step = (status: StepStatus = 'waiting', reason = ''): StepState => ({ status, reason });

/** Gives every importable source a row, so the progress list shows them all at once. A row that exists is kept. */
export function prepareState(input: Pick<SubmitInput, 'sources' | 'envs'>, state: SubmitState): void {
  if (input.envs.length === 0 && state.envs.status === 'waiting') state.envs = step('skipped');
  for (const source of input.sources) {
    const key = sourceKey(source);
    if (!state.tags[key]) state.tags[key] = step();
    if (source.kind === 'mongo-database' && source.classification === 'audit' && !state.audit[key]) state.audit[key] = step();
    if (isImportable(source) && !state.imports[key]) state.imports[key] = { ...step(), label: source.label, percent: null, outcome: null };
  }
}

/** What did not go through, in the order the steps run. */
export function failuresOf(state: SubmitState, sources: readonly Pick<DraftSource, 'kind' | 'ref' | 'label'>[]): Failure[] {
  const out: Failure[] = [];
  if (!state.caseId) {
    if (state.createError) out.push({ step: 'case', key: '', label: '', reason: state.createError });
    return out;
  }
  if (state.envs.status === 'failed') out.push({ step: 'environments', key: '', label: '', reason: state.envs.reason });
  for (const source of sources) {
    const key = sourceKey(source);
    const tag = state.tags[key];
    if (tag?.status === 'failed') out.push({ step: 'tag', key, label: source.label, reason: tag.reason });
    const audit = state.audit[key];
    if (audit?.status === 'failed') out.push({ step: 'audit-source', key, label: source.label, reason: audit.reason });
    const row = state.imports[key];
    if (row && (row.status === 'failed' || row.status === 'skipped')) out.push({ step: 'import', key, label: source.label, reason: row.reason });
  }
  return out;
}

export const importedCount = (state: SubmitState): number => Object.values(state.imports).filter((row) => row.status === 'done').length;

/** True when the analysis should open by itself: everything went through. Otherwise the person reads what failed first. */
export const shouldOpenAnalysis = (result: Pick<SubmitResult, 'finished' | 'locked' | 'caseId' | 'failures'>): boolean =>
  result.finished && !result.locked && result.caseId !== '' && result.failures.length === 0;

/* ------------------------------------------------------------------ the run */

function settle(row: ImportRow, outcome: RowOutcome, state: SubmitState): void {
  row.outcome = outcome;
  if (outcome.kind === 'refused' && outcome.reason === 'locked') { row.status = 'waiting'; row.reason = ''; row.outcome = null; state.locked = true; return; }
  row.percent = outcome.kind === 'added' || outcome.kind === 'duplicate' ? 100 : row.percent;
  if (outcome.kind === 'added' || outcome.kind === 'duplicate') { row.status = 'done'; row.reason = outcome.kind; return; }
  row.status = 'failed';
  row.reason = outcome.kind === 'refused' || outcome.kind === 'failed' ? outcome.reason || outcome.kind : outcome.kind;
}

function failRows(rows: ImportRow[], error: unknown): void {
  const outcome = outcomeFromError(error);
  for (const row of rows) { row.outcome = outcome; row.status = 'failed'; row.reason = outcome.kind === 'refused' || outcome.kind === 'failed' ? outcome.reason || outcome.kind : outcome.kind; }
}

const inflight = new WeakMap<SubmitState, Promise<SubmitResult>>();

/**
 * Runs the whole submit. Never throws for a failed step: it records the failure and goes on. Resolves when every step has been tried,
 * or sooner with `locked` set when the master key closed. A call while another is running for the same state returns that run.
 */
export function submitWizard(input: SubmitInput, api: SubmitApi, state: SubmitState): Promise<SubmitResult> {
  const running = inflight.get(state);
  if (running) return running;
  const run = execute(input, api, state).finally(() => { inflight.delete(state); });
  inflight.set(state, run);
  return run;
}

async function execute(input: SubmitInput, api: SubmitApi, state: SubmitState): Promise<SubmitResult> {
  state.locked = false;
  state.finished = false;
  prepareState(input, state);
  const result = (): SubmitResult => ({ caseId: state.caseId, locked: state.locked, finished: state.finished, imported: importedCount(state), failures: failuresOf(state, input.sources) });
  try {
    // 1. The case. Without it nothing else can happen, so a failure here ends the run with nothing kept on the server.
    if (!state.caseId) {
      state.createError = '';
      try {
        const made = await guarded(() => api.createCase(input.title));
        state.caseId = made.id;
        state.caseTitle = input.title;
      } catch (error) {
        if (error instanceof Locked) throw error;
        state.createError = reasonOf(error);
        state.finished = true;
        return result();
      }
    }
    const caseId = state.caseId;

    // 2. The environments.
    if (input.envs.length > 0 && state.envs.status !== 'done') {
      state.envs = step('running');
      try { await guarded(() => api.putEnvironments(caseId, input.envs)); state.envs = step('done'); }
      catch (error) { if (error instanceof Locked) { state.envs = step(); throw error; } state.envs = step('failed', reasonOf(error)); }
    }

    // 3. The source tags (and the audit sources of classified Mongo databases).
    const tested = new Set<string>();
    for (const source of input.sources) {
      const key = sourceKey(source);
      if (state.tags[key].status !== 'done') {
        state.tags[key] = step('running');
        try {
          if (source.kind === 'mongo-database') {
            const ref = parseDatabaseRef(source.ref);
            if (!ref) throw new CaseApiError('That database is not known.', 422, 'database-not-tested');
            if (!tested.has(ref.connectionId)) { await guarded(() => api.testConnection(ref.connectionId)); tested.add(ref.connectionId); }
          }
          await guarded(() => api.postSourceTag(caseId, { importId: 0, kind: source.kind, label: source.label, env: source.env, portal: source.portal, api: source.api, classification: source.kind === 'mongo-database' ? source.classification : '' }));
          state.tags[key] = step('done');
        } catch (error) {
          if (error instanceof Locked) { state.tags[key] = step(); throw error; }
          state.tags[key] = step('failed', reasonOf(error));
          continue;
        }
      }
      const audit = state.audit[key];
      if (audit && audit.status !== 'done') {
        const ref = parseDatabaseRef(source.ref);
        state.audit[key] = step('running');
        try {
          if (!ref) throw new CaseApiError('That database is not known.', 422, 'database-not-tested');
          await guarded(() => api.addAuditSource(ref.connectionId, ref.database));
          state.audit[key] = step('done');
        } catch (error) {
          if (error instanceof Locked) { state.audit[key] = step(); throw error; }
          state.audit[key] = step('failed', reasonOf(error));
        }
      }
    }

    // 4. The imports. A row that is already done (an earlier run) is left alone; an interrupted one starts again.
    await importAll(input, api, state, caseId);
    if (!state.locked) state.finished = true;
  } catch (error) {
    if (error instanceof Locked) state.locked = true;
    else if (!isAbort(error)) throw error;
  }
  if (state.locked) for (const row of Object.values(state.imports)) if (row.status === 'running') { row.status = 'waiting'; row.percent = null; }
  return result();
}

async function importAll(input: SubmitInput, api: SubmitApi, state: SubmitState, caseId: string): Promise<void> {
  const todo = input.sources.filter((source) => isImportable(source) && state.imports[sourceKey(source)].status !== 'done');
  const scan = todo.filter(isScanSource);
  const files = todo.filter(isFileSource);
  const rowOf = (source: DraftSource): ImportRow => state.imports[sourceKey(source)];
  for (const source of todo) { const row = rowOf(source); row.status = 'waiting'; row.reason = ''; row.outcome = null; row.percent = null; }

  const tasks: Array<Promise<void>> = [];

  if (scan.length > 0) {
    const problem = windowProblem(input.window);
    if (problem || !input.window || !input.scanId) {
      for (const source of scan) { const row = rowOf(source); row.status = 'failed'; row.reason = problem ?? 'no-window'; }
    } else {
      const window = input.window;
      tasks.push((async () => {
        try {
          if (input.host?.proposed) await guarded(async () => { await api.confirmHost?.(input.host as ScanHost); });
        } catch (error) {
          if (error instanceof Locked) throw error;
          failRows(scan.map(rowOf), error);
          return;
        }
        const watchers: Array<Promise<void>> = [];
        for (let at = 0; at < scan.length; at += MAX_FINDINGS_PER_CALL) {
          const chunk = scan.slice(at, at + MAX_FINDINGS_PER_CALL);
          let refs: JobRef[];
          try {
            refs = await guarded(() => api.startImports(caseId, { scanId: input.scanId, findingIds: chunk.map((s) => s.ref), window, ackGating: true, ackText: input.ackText }));
          } catch (error) {
            if (error instanceof Locked) throw error;
            failRows(chunk.map(rowOf), error);
            continue;
          }
          for (const source of chunk) {
            const row = rowOf(source);
            const ref = refs.find((r) => r.findingId === source.ref);
            if (!ref) { row.status = 'failed'; row.reason = 'not-started'; continue; }
            row.status = 'running';
            watchers.push(api.watchJob(caseId, ref, (percent) => { row.percent = percent; }).then((outcome) => settle(row, outcome, state), (error) => {
              if (isAbort(error)) return;
              settle(row, outcomeFromError(error), state);
            }));
          }
        }
        await Promise.all(watchers);
      })());
    }
  }

  // Files go one after another: the server reads one import at a time.
  tasks.push((async () => {
    for (const source of files) {
      if (state.locked) break;
      const row = rowOf(source);
      const file = input.files[sourceKey(source)];
      if (!file) { row.status = 'skipped'; row.reason = 'no-file'; continue; }
      row.status = 'running';
      try {
        settle(row, await api.importFile(caseId, file, (percent) => { row.percent = percent; }), state);
      } catch (error) {
        if (isAbort(error)) { row.status = 'waiting'; continue; }
        settle(row, outcomeFromError(error), state);
      }
    }
  })());

  const outcomes = await Promise.allSettled(tasks);
  for (const outcome of outcomes) if (outcome.status === 'rejected') { if (outcome.reason instanceof Locked) state.locked = true; else if (!isAbort(outcome.reason)) throw outcome.reason; }
}
