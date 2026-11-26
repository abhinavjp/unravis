import {
  assistantRows, backStep, buildPatch, canNext, checkApplied, cliState, DEFAULT_MINUTES, fmtClock, minutesError, newDraft, nextStep, parseMinutes, PatchError,
  phraseOk, quickPatch, stepSkipped, STEP_RESULT, type AssistantRow, type CliChoice, type Draft, type QuickAction, type SettingsPatch
} from './codeMode';
import { listHosts } from './mcpGrantsApi';
import type { McpList } from './mcpGrants';
import { codeModeStore, type CodeModeStore } from './codeModeStore.svelte';
import { CODE_TEXT } from './copy/codeModeCopy';

/**
 * The logic of the code-mode popup (seven steps; the markup is CodeModePopup.svelte). Nothing is saved until the Review step is
 * confirmed, and one save carries every choice. The Result step reads the server's last answer from the store, never the draft.
 */

export interface FlowDeps {
  listHosts(): Promise<McpList>;
}

export type AssistantsStatus = 'idle' | 'loading' | 'ready' | 'error';

export class CodeModeFlow {
  step = $state(0);
  furthest = $state(0);
  draft = $state<Draft>(newDraft(null, { kind: 'off', leftMs: 0 }));
  saving = $state(false);
  /** The words of the last refused or unapplied save; empty otherwise. */
  saveError = $state('');
  /** A short confirmation after a save; spoken by the popup's status line. */
  notice = $state('');
  assistantsStatus = $state<AssistantsStatus>('idle');
  assistants = $state<AssistantRow[]>([]);

  private store: CodeModeStore;
  private deps: FlowDeps;

  constructor(store: CodeModeStore, deps: FlowDeps = { listHosts: () => listHosts() }) {
    this.store = store;
    this.deps = deps;
  }

  /** Opens the popup. Code mode on, or a damaged file, lands on Result; otherwise on the first step. */
  open(): void {
    const s = this.store.settings;
    this.draft = newDraft(s, this.store.cli);
    this.step = s && (s.enabled || s.damaged) ? STEP_RESULT : 0;
    this.furthest = this.step === STEP_RESULT ? STEP_RESULT : 0;
    this.saveError = '';
    this.notice = '';
    void this.loadAssistants();
  }

  async loadAssistants(): Promise<void> {
    this.assistantsStatus = 'loading';
    try {
      const list = await this.deps.listHosts();
      this.assistants = assistantRows(list);
      this.assistantsStatus = 'ready';
    } catch {
      this.assistants = [];
      this.assistantsStatus = 'error';
    }
  }

  /* ---- what the steps ask ---- */

  get hasUnlimited(): boolean { return this.store.settings?.hasUnlimited === true; }
  get allowedAssistants(): AssistantRow[] { return this.assistants.filter((a) => a.tone === 'allowed'); }
  get minsError(): string { return this.draft.mode === 'timed' ? minutesError(this.draft.minsRaw) : ''; }
  get typedOk(): boolean { return phraseOk(this.draft.typed); }
  get nextOn(): boolean { return canNext(this.draft, this.step, this.hasUnlimited); }
  skipped(i: number): boolean { return i < STEP_RESULT && this.furthest >= 1 && stepSkipped(this.draft, i); }
  /** The rail button of step i can be pressed: it has been reached, it is not the current one, and Result is only there once code mode is on. */
  reachable(i: number): boolean { return i <= this.furthest && i !== this.step && !(i === STEP_RESULT && !this.store.settings?.enabled && !this.store.settings?.damaged); }

  get confirmLabel(): string {
    const on = this.store.settings?.enabled === true;
    if (this.draft.enabled) return on ? CODE_TEXT.confirmSave : CODE_TEXT.confirmOn;
    return on ? CODE_TEXT.confirmOff : CODE_TEXT.confirmKeepOff;
  }

  /** The words for the command line on the Review step. */
  get cliSummary(): { value: string; sub: string; danger: boolean } {
    const d = this.draft;
    const T = CODE_TEXT;
    if (!d.enabled) return { value: T.cliShut, sub: T.cliOffSub, danger: false };
    const cli = this.store.cli;
    if (d.mode === 'keep') return cli.kind === 'unlimited' ? { value: T.cliNoLimit, sub: T.cliStays, danger: true } : { value: T.cliOpenLeft(fmtClock(cli.leftMs)), sub: T.cliStays, danger: false };
    if (d.mode === 'timed') { const n = parseMinutes(d.minsRaw); return { value: T.cliOpenFor(Number.isNaN(n) ? 0 : n), sub: T.cliOpenForSub, danger: false }; }
    if (d.mode === 'unlimited') return { value: T.cliNoLimit, sub: T.cliNoLimitSub, danger: true };
    return { value: T.cliShut, sub: T.cliShutSub, danger: false };
  }

  /* ---- moving ---- */

  go(i: number): void {
    // Leaving Result for an earlier step starts a new draft from what is true now.
    if (this.step === STEP_RESULT && i < STEP_RESULT) this.draft = newDraft(this.store.settings, this.store.cli);
    this.step = i;
    this.furthest = Math.max(this.furthest, i);
    this.saveError = '';
    this.notice = '';
  }
  next(): void { if (this.nextOn) this.go(nextStep(this.draft, this.step)); }
  back(): void { this.go(backStep(this.draft, this.step)); }

  /* ---- choosing ---- */

  setEnabled(on: boolean): void { this.draft.enabled = on; }
  setKeep(on: boolean): void { this.draft.keep = on; }
  setDeg(on: boolean): void { this.draft.deg = on; }

  setMode(mode: CliChoice): void {
    if (mode === 'unlimited' && !this.hasUnlimited) return;
    this.draft.mode = mode;
    if (mode !== 'unlimited') this.draft.typed = '';
  }

  /** The minutes box. Typing 0 means shut, so it goes back to "Keep it shut" and returns true so the page can say so. */
  setMinutes(raw: string): boolean {
    this.draft.minsRaw = raw;
    if (parseMinutes(raw) === 0) {
      this.draft.mode = 'shut';
      this.draft.minsRaw = String(DEFAULT_MINUTES);
      return true;
    }
    return false;
  }
  pickMinutes(n: number): void { this.draft.minsRaw = String(n); }
  setTyped(text: string): void { this.draft.typed = text; }

  /* ---- saving ---- */

  /** The one save of the Review step. */
  async commit(): Promise<boolean> {
    const s = this.store.settings;
    if (!s || this.saving) return false;
    let patch: SettingsPatch;
    try {
      patch = buildPatch(s, this.draft);
    } catch (error) {
      this.saveError = error instanceof PatchError ? error.message : CODE_TEXT.refusedBase;
      return false;
    }
    return this.persist(patch, this.draft.enabled ? CODE_TEXT.savedOn : CODE_TEXT.savedOff);
  }

  /** A button on the Result step: close the command line, a 15 minute window, or code mode off. */
  async quick(action: QuickAction): Promise<boolean> {
    const s = this.store.settings;
    if (!s || this.saving) return false;
    const notice = action === 'off' ? CODE_TEXT.turnedOff : action === 'tofifteen' ? CODE_TEXT.fifteenDone : CODE_TEXT.closedCli;
    return this.persist(quickPatch(s, action), notice);
  }

  private async persist(patch: SettingsPatch, okNotice: string): Promise<boolean> {
    this.saving = true;
    this.saveError = '';
    this.notice = '';
    try {
      const saved = await this.store.save(patch);
      const applied = checkApplied(patch, saved, this.store.nowMs, this.store.offsetMs);
      if (applied === 'ok') this.notice = okNotice;
      else this.saveError = applied === 'noLimitStillOn' ? CODE_TEXT.notAppliedNoLimit : CODE_TEXT.notApplied;
      this.draft = newDraft(saved, cliState(saved, this.store.nowMs, this.store.offsetMs));
      this.step = STEP_RESULT;
      this.furthest = STEP_RESULT;
      return applied === 'ok';
    } catch (error) {
      // A refused save changes nothing: the page stays where it is and says why, in the server's own words.
      this.saveError = error instanceof Error && error.message ? error.message : CODE_TEXT.refusedBase;
      return false;
    } finally {
      this.saving = false;
    }
  }
}

/** The popup the app shows: one flow over the shared settings store. */
export const codeModeFlow = new CodeModeFlow(codeModeStore);
