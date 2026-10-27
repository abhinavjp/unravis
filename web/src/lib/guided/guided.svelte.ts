import { CaseApiError } from '../cases';
import { CHECKLIST_ERRORS, GUIDED_TEXT, SAVED_VIEW_ERRORS, refusalText } from '../copy/guidedCopy';
import {
  addChecklistItem, buildReport, deleteChecklistItem, deleteView, listChecklist, listViews, runInvestigation, saveView, updateChecklistItem
} from './api';
import { emptyFrame, quickPick, requestKey, toRequest, type Frame, type QuickPick } from './frame';
import { clampStep, suggestedChecks, type RunPhase } from './stepModel';
import { cleanViewName, viewSettings, viewsFull, type ToolId } from './tools';
import type { ChecklistItem, ChecklistList, InvestigationAnswer, InvestigationRequest, ReportAnswer, ReportInclude, SavedView } from './types';

/**
 * The state of one guided analysis: the windows (Step 0), the answer of the last run, the step on screen, the checklist and the saved views.
 * One instance belongs to one open case. It holds no logic of its own beyond sequencing: what a number means is in stepModel.ts, afterFix.ts
 * and tools.ts, and the server does the reading. A run that is no longer wanted is ignored when it answers.
 */

export type Mode = 'guided' | 'manual';
const MODE_KEY = 'unravis.mode.';

const aborted = (cause: unknown): boolean => cause instanceof DOMException && cause.name === 'AbortError';

function readMode(caseId: string): Mode {
  try { return (typeof localStorage !== 'undefined' && localStorage.getItem(MODE_KEY + caseId) === 'manual') ? 'manual' : 'guided'; } catch { return 'guided'; }
}
function writeMode(caseId: string, mode: Mode): void {
  try { if (typeof localStorage !== 'undefined') localStorage.setItem(MODE_KEY + caseId, mode); } catch { /* the choice is only not remembered */ }
}

/** The sentence for any failure, never the raw message of the network or the server. */
function failureText(cause: unknown, table: Readonly<Record<string, string>>, fallback: string): string {
  return cause instanceof CaseApiError ? refusalText(cause.reason ?? '', table, fallback) : fallback;
}

export class GuidedStore {
  caseId: string;
  mode = $state<Mode>('guided');
  step = $state(0);
  frame = $state<Frame>(emptyFrame());
  /** The onset time the person typed, UTC. Shown beside the suggested one. The engine does not use it. */
  myOnset = $state('');
  phase = $state<RunPhase>('idle');
  answer = $state.raw<InvestigationAnswer | null>(null);
  error = $state('');
  /** The key of the request the answer on screen was made from. */
  answeredKey = $state('');
  items = $state.raw<ChecklistItem[]>([]);
  views = $state.raw<SavedView[]>([]);
  planLoaded = $state(false);
  planError = $state('');
  viewError = $state('');
  report = $state.raw<ReportAnswer | null>(null);
  reportState = $state<'idle' | 'building' | 'failed'>('idle');
  reportError = $state('');
  tool = $state<ToolId | ''>('');

  #run = 0;
  /** The request the answer on screen was made from. A report is built from this one, so it matches what the person sees. */
  #answered: InvestigationRequest | null = null;
  #controller: AbortController | null = null;
  #plan = 0;

  constructor(caseId: string) {
    this.caseId = caseId;
    this.mode = readMode(caseId);
  }

  get result() { return this.answer?.result ?? null; }

  /** The key of the request the windows on screen would make, or '' when they cannot be run. */
  keyFor(sourceIds: readonly string[]): string {
    const request = toRequest(this.frame, sourceIds, false);
    return request ? requestKey(request) : '';
  }

  /** True when an answer is on screen and the windows, sources or lists have changed since it was made. */
  outOfDate(sourceIds: readonly string[]): boolean {
    return !!this.answer && this.phase !== 'running' && this.answeredKey !== '' && this.keyFor(sourceIds) !== this.answeredKey;
  }

  setMode(mode: Mode): void {
    this.mode = mode;
    writeMode(this.caseId, mode);
  }

  goTo(step: number): void { this.step = clampStep(step); }

  pick(pick: QuickPick, now: number): void { this.frame = { ...this.frame, ...quickPick(pick, now) }; }

  /** Reads the audit data and runs the steps. A second press while one run is going replaces it. */
  async run(sourceIds: readonly string[]): Promise<boolean> {
    const request = toRequest(this.frame, sourceIds, true);
    if (!request) return false;
    this.cancel();
    const mine = ++this.#run;
    const controller = new AbortController();
    this.#controller = controller;
    this.phase = 'running';
    this.error = '';
    try {
      const answer = await runInvestigation(this.caseId, request, controller.signal);
      if (mine !== this.#run) return false;
      this.answer = answer;
      this.answeredKey = requestKey(request);
      this.#answered = request;
      this.phase = 'done';
      return true;
    } catch (cause) {
      if (mine !== this.#run || aborted(cause)) return false;
      this.phase = 'failed';
      this.error = cause instanceof CaseApiError && cause.status === 413 ? GUIDED_TEXT.truncated : GUIDED_TEXT.runFailed;
      return false;
    } finally {
      if (mine === this.#run) this.#controller = null;
    }
  }

  /** Stops the run on screen. The answer from an earlier run, if any, stays. */
  cancel(): void {
    this.#run++;
    this.#controller?.abort();
    this.#controller = null;
    if (this.phase === 'running') this.phase = this.answer ? 'done' : 'idle';
  }

  dispose(): void { this.cancel(); this.#plan++; }

  /* ------------------------------------------------------------- checklist and views */

  async loadPlan(): Promise<void> {
    const mine = ++this.#plan;
    try {
      const [items, views] = await Promise.all([listChecklist(this.caseId), listViews(this.caseId)]);
      if (mine !== this.#plan) return;
      this.items = items;
      this.views = views;
      this.planLoaded = true;
      this.planError = '';
    } catch (cause) {
      if (mine !== this.#plan) return;
      this.planError = failureText(cause, CHECKLIST_ERRORS, 'The checklist could not be read.');
      this.planLoaded = true;
    }
  }

  async addItem(list: ChecklistList, text: string, suspect?: string): Promise<boolean> {
    const clean = text.trim();
    if (!clean) return false;
    try {
      const item = await addChecklistItem(this.caseId, { list, text: clean.slice(0, 300), ...(suspect ? { suspect } : {}) });
      this.items = [...this.items, item];
      this.planError = '';
      return true;
    } catch (cause) {
      this.planError = failureText(cause, CHECKLIST_ERRORS, 'That item could not be saved.');
      if (cause instanceof CaseApiError && cause.reason === 'item-not-found') void this.loadPlan();
      return false;
    }
  }

  /** Adds every check Step 10 suggests that is not on the list yet. Returns how many were added. */
  async addSuggested(): Promise<number> {
    const result = this.result;
    if (!result) return 0;
    let added = 0;
    for (const check of suggestedChecks(result, this.items)) {
      if (!(await this.addItem('todo', check.text, check.suspect))) break;
      added++;
    }
    return added;
  }

  async moveItem(item: ChecklistItem, list: ChecklistList): Promise<void> {
    if (item.list === list) return;
    try {
      const next = await updateChecklistItem(this.caseId, item.id, { list, text: item.text, suspect: item.suspect, order: item.order });
      this.items = this.items.map((i) => (i.id === item.id ? next : i));
      this.planError = '';
    } catch (cause) {
      this.planError = failureText(cause, CHECKLIST_ERRORS, 'That item could not be moved.');
      if (cause instanceof CaseApiError && cause.reason === 'item-not-found') void this.loadPlan();
    }
  }

  async removeItem(item: ChecklistItem): Promise<void> {
    try {
      await deleteChecklistItem(this.caseId, item.id);
      this.items = this.items.filter((i) => i.id !== item.id);
      this.planError = '';
    } catch (cause) {
      this.planError = failureText(cause, CHECKLIST_ERRORS, 'That item could not be deleted.');
      if (cause instanceof CaseApiError && cause.reason === 'item-not-found') void this.loadPlan();
    }
  }

  async saveCurrentView(name: string, tool: string, extra: { minCalls?: number; match?: string } = {}): Promise<boolean> {
    const clean = cleanViewName(name);
    if (!clean) { this.viewError = GUIDED_TEXT.viewNameMissing; return false; }
    if (viewsFull(this.views)) { this.viewError = GUIDED_TEXT.viewsFull; return false; }
    try {
      const view = await saveView(this.caseId, { name: clean, tool, settings: viewSettings(this.frame, extra) });
      this.views = [view, ...this.views];
      this.viewError = '';
      return true;
    } catch (cause) {
      this.viewError = failureText(cause, SAVED_VIEW_ERRORS, 'That view could not be saved.');
      return false;
    }
  }

  async removeView(view: SavedView): Promise<void> {
    try {
      await deleteView(this.caseId, view.id);
      this.views = this.views.filter((v) => v.id !== view.id);
      this.viewError = '';
    } catch (cause) {
      this.viewError = failureText(cause, SAVED_VIEW_ERRORS, 'That view could not be deleted.');
      if (cause instanceof CaseApiError && cause.reason === 'view-not-found') void this.loadPlan();
    }
  }

  /* ------------------------------------------------------------- export */

  /** Builds a report on the server. The request is sent only when an answer is on screen, so an empty case exports with its notice. */
  async buildReport(format: 'html' | 'csv', include: ReportInclude): Promise<ReportAnswer | null> {
    this.reportState = 'building';
    this.reportError = '';
    try {
      const request = this.answer && this.#answered ? this.#answered : undefined;
      const out = await buildReport(this.caseId, format, include, request);
      this.report = out;
      this.reportState = 'idle';
      return out;
    } catch (cause) {
      this.reportState = 'failed';
      this.reportError = failureText(cause, {}, GUIDED_TEXT.exportFailed);
      return null;
    }
  }
}
