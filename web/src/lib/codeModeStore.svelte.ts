import { codeApi, CodeApiError, UNAVAILABLE_CODE, type SettingsReply } from './codeModeApi';
import { badgeModel, cliState, clockOffsetMs, type BadgeModel, type CliState, type CodeSettings, type SettingsPatch } from './codeMode';
import { visiblePoll } from './visibility';
import { CODE_TEXT } from './copy/codeModeCopy';

/**
 * The one shared copy of the code-mode settings, read from the server (GET /api/code/settings). The badge and the popup both read it.
 * It is read when the store starts, when the window gets focus again, every 30 seconds, and after every save.
 * The countdown is worked out here from `cliOpenUntil`, corrected by the gap between the server clock and this one. It is a display
 * only: the server's gate is what lets the command line run.
 */

export const REFRESH_MS = 30_000;
export const TICK_MS = 1_000;

export type CodeStatus = 'idle' | 'loading' | 'ready' | 'unavailable' | 'error';

export interface StoreApi {
  get(): Promise<SettingsReply>;
  put(patch: SettingsPatch): Promise<SettingsReply>;
}

/** Everything the store takes from the outside, so a test can drive the clock, the timers and the calls. */
export interface StoreDeps {
  api: StoreApi;
  now(): number;
  /** Runs `fn` every `ms` while the page is shown; returns a handle for `cancel`. */
  every(fn: () => void, ms: number): unknown;
  cancel(handle: unknown): void;
  /** Calls `fn` whenever the window gets focus or the page becomes visible; returns the way to stop. */
  onFocus(fn: () => void): () => void;
}

function realDeps(): StoreDeps {
  return {
    api: codeApi,
    now: () => Date.now(),
    every: (fn, ms) => visiblePoll(() => { fn(); }, ms),
    cancel: (h) => (h as () => void)(),
    onFocus: (fn) => {
      if (typeof window === 'undefined') return () => {};
      const onVisible = () => { if (document.visibilityState === 'visible') fn(); };
      window.addEventListener('focus', fn);
      document.addEventListener('visibilitychange', onVisible);
      return () => { window.removeEventListener('focus', fn); document.removeEventListener('visibilitychange', onVisible); };
    }
  };
}

export class CodeModeStore {
  settings = $state<CodeSettings | null>(null);
  status = $state<CodeStatus>('idle');
  /** The words of the last failed read or save; empty when the last one worked. */
  error = $state('');
  /** The browser clock as of the last tick, in ms. The countdown reads this, so it moves once a second. */
  nowMs = $state(0);
  /** The server clock minus this clock, in ms. */
  offsetMs = $state(0);

  private deps: StoreDeps;
  private seq = 0;
  private inflight: Promise<void> | null = null;
  private poll: unknown = null;
  private tickHandle: unknown = null;
  private stopFocus: (() => void) | null = null;
  private expiryRead = false;

  constructor(deps: StoreDeps = realDeps()) {
    this.deps = deps;
    this.nowMs = deps.now();
  }

  /** Where the command line stands now. */
  get cli(): CliState { return cliState(this.settings, this.nowMs, this.offsetMs); }

  /** The header badge, or null when there is nothing to show. */
  get badge(): BadgeModel | null { return this.status === 'ready' ? badgeModel(this.settings, this.nowMs, this.offsetMs) : null; }

  /** Starts reading: once now, on focus, every 30 seconds, and a tick each second. Calling it twice does nothing. */
  start(): void {
    if (this.poll !== null) return;
    this.poll = this.deps.every(() => { void this.refresh(); }, REFRESH_MS);
    this.tickHandle = this.deps.every(() => this.tick(), TICK_MS);
    this.stopFocus = this.deps.onFocus(() => { void this.refresh(); });
    void this.refresh();
  }

  stop(): void {
    if (this.poll !== null) this.deps.cancel(this.poll);
    if (this.tickHandle !== null) this.deps.cancel(this.tickHandle);
    this.stopFocus?.();
    this.poll = this.tickHandle = this.stopFocus = null;
  }

  /** One tick of the countdown. At zero the settings are read again, once, so the badge follows the server and not only this clock. */
  tick(): void {
    this.nowMs = this.deps.now();
    if (this.cli.kind === 'open') { this.expiryRead = false; return; }
    const s = this.settings;
    if (s && s.enabled && !s.cliUnlimited && s.cliOpenUntil > 0 && !this.expiryRead) {
      this.expiryRead = true;
      void this.refresh();
    }
  }

  /** Reads the settings. A read that is already on its way is shared. A failed read keeps the last good answer and says so in `error`. */
  refresh(): Promise<void> {
    if (this.inflight) return this.inflight;
    const seq = ++this.seq;
    if (this.status === 'idle') this.status = 'loading';
    const run = this.deps.api.get().then(
      (reply) => { if (seq === this.seq) this.take(reply); },
      (error) => { if (seq === this.seq) this.fail(error); }
    ).finally(() => { if (this.inflight === run) this.inflight = null; });
    this.inflight = run;
    return run;
  }

  /**
   * Saves with PUT, shows the server's reply at once, then reads again. Resolves with the reply; rejects with the server's own words
   * when the save is refused (the page keeps showing what the server last said).
   */
  async save(patch: SettingsPatch): Promise<CodeSettings> {
    this.seq++; // a read that started before this save is out of date
    let reply: SettingsReply;
    try {
      reply = await this.deps.api.put(patch);
    } catch (error) {
      if (error instanceof CodeApiError && error.code === UNAVAILABLE_CODE) this.fail(error);
      throw error;
    }
    this.seq++;
    this.inflight = null; // a read that was on its way is out of date: the read below must be a new one
    this.take(reply);
    void this.refresh();
    return reply.settings;
  }

  private take(reply: SettingsReply): void {
    const received = this.deps.now();
    this.offsetMs = clockOffsetMs(reply.serverDate, received);
    this.nowMs = received;
    this.settings = reply.settings;
    this.status = 'ready';
    this.error = '';
  }

  private fail(error: unknown): void {
    const e = error instanceof CodeApiError ? error : null;
    if (e && e.code === UNAVAILABLE_CODE) {
      this.settings = null;
      this.status = 'unavailable';
      this.error = '';
      return;
    }
    this.error = e ? e.message : CODE_TEXT.refreshFailed;
    // With an earlier good answer the page keeps it (a danger badge must not vanish on a bad read); without one it has nothing to show.
    if (!this.settings) this.status = 'error';
  }
}

/** The store the app uses. Nothing is read until `start()` is called. */
export const codeModeStore = new CodeModeStore();
