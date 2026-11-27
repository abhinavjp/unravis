import { serversApi } from './serversApi';
import { browserStorage, forgetServer, loadMemory, patternFor, recordCheck, recordPatterns, recordPull, saveMemory, type Memory } from './serversMemory';
import { SV_TEXT } from './copy/serversCopy';
import {
  FAILURE_TEXT, ServersError, buildAddBody, buildPatchBody, buildPullBody, caseRange, caseRangeProblem, connectProblems, emptyConnectForm, findConnection,
  dedupeMachines, folderKindInfo, initialPicks, jobPercent, matchesConnection, patternsFrom, pullTargets, readableTargets, rangeHours, togglePick, withFolder, withFolders, withoutFolder, withTimeout,
  type AuthKind, type ConnectForm, type Discovery, type Failure, type FolderMap, type HostKey, type JobView, type MachineView, type PlanView, type PullTarget, type RangeChoice,
  type ServerEntry, type TestResult
} from './servers';
import type { WindowFields } from './tracing/windows';

/**
 * The state and the steps of the Servers screen. Everything here comes from the backend: nothing is made up for the page.
 * `connect` and `saveSecret` pass credentials to the API without keeping them in flow state.
 */

export type Step = 'connect' | 'trust' | 'pick' | 'reach' | 'has' | 'pull' | 'land';
export const STEP_ORDER: Step[] = ['connect', 'trust', 'pick', 'reach', 'has', 'pull', 'land'];

export interface ChangedKey { pinned: string; presented: string; next: string; error: string }
export interface TestState { state: 'running' | 'done' | 'failed'; result?: TestResult; failure?: Failure }
export interface DiscoverState { state: 'running' | 'done' | 'failed'; discovery?: Discovery; failure?: Failure }
export interface RowState { on: boolean; range: RangeChoice; state: 'idle' | 'loading' | 'ok' | 'failed'; plan: PlanView | null; failure?: Failure; note?: string }
export interface PullState { state: 'queued' | 'running' | 'done' | 'failed' | 'stopped'; job?: JobView; planned: number; failure?: Failure; stopping?: boolean }

interface Env { windows: () => WindowFields | null; now: () => number }

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const POLL_MS = 1000;

const failureOf = (e: unknown): Failure => e instanceof ServersError ? e.failure : { status: 0, error: FAILURE_TEXT.offline, reason: 'offline' };

export class ServersFlow {
  private env: Env;
  private disposed = false;
  private stopAsked = false;
  private call: AbortController | null = null;
  private listCall: AbortController | null = null;
  private provisionalServerId: string | null = null;
  private sudoRetries = new Map<string, Promise<unknown>>();

  constructor(env: Env) { this.env = env; }

  private remember(change: (m: Memory) => Memory): void { this.memory = change(this.memory); saveMemory(browserStorage(), this.memory); }

  list = $state<ServerEntry[] | null>(null);
  listError = $state('');
  step = $state<Step>('connect');
  reached = $state(0);
  server = $state<ServerEntry | null>(null);
  draft = $state(emptyConnectForm());
  needSecret = $state(false);
  busy = $state('');
  error = $state<Failure | null>(null);
  note = $state('');

  hostKeys = $state<HostKey[]>([]);
  changed = $state<ChangedKey | null>(null);

  machines = $state<MachineView[]>([]);
  picks = $state<string[]>([]);
  sudo = $state(false);

  tests = $state<Record<string, TestState>>({});
  discoveries = $state<Record<string, DiscoverState>>({});
  memory = $state<Memory>(loadMemory(browserStorage()));
  rows = $state<Record<string, RowState>>({});
  targets = $state<PullTarget[]>([]);
  pulls = $state<Record<string, PullState>>({});
  order = $state<string[]>([]);
  running = $state(false);

  /* ------------------------------------------------------------- basics */

  dispose(): void { this.disposed = true; this.abortCall(); this.listCall?.abort(); }

  caseRange() { return caseRange(this.env.windows(), this.env.now()); }

  private goto(step: Step): void {
    this.step = step;
    this.reached = Math.max(this.reached, STEP_ORDER.indexOf(step));
  }

  /** Back to an earlier step. Not while a copy runs. */
  go(step: Step): void {
    if (this.running || STEP_ORDER.indexOf(step) > this.reached) return;
    this.abortCall();
    this.error = null; this.note = '';
    this.step = step;
  }

  private fail(e: unknown): void {
    const f = failureOf(e);
    if (f.reason === 'host-key-changed') {
      this.changed = { pinned: f.pinned ?? '', presented: f.presented ?? '', next: f.next ?? '', error: f.error };
      this.error = null;
      this.goto('trust');
      return;
    }
    this.error = f;
  }

  private abortCall(): void {
    this.call?.abort();
    this.call = null;
    this.busy = '';
  }

  private async guard<T>(label: string, fn: (signal: AbortSignal) => Promise<T>, apply?: (value: T) => void): Promise<T | undefined> {
    if (this.disposed) return;
    this.abortCall();
    const call = new AbortController();
    this.call = call;
    this.busy = label; this.error = null;
    try {
      const value = await fn(call.signal);
      if (this.call !== call) return;
      apply?.(value);
      return value;
    } catch (e) {
      if (this.call === call && !(e instanceof DOMException && e.name === 'AbortError')) this.fail(e);
    } finally {
      if (this.call === call) { this.call = null; this.busy = ''; }
    }
  }

  private setServer(entry: ServerEntry): void {
    this.server = entry;
    if (this.list) this.list = this.list.some((s) => s.id === entry.id) ? this.list.map((s) => (s.id === entry.id ? { ...entry, running: s.running } : s)) : [...this.list, entry];
    if (this.listCall) void this.load();
  }

  /* -------------------------------------------------------- saved servers */

  async load(): Promise<void> {
    this.listCall?.abort();
    const call = new AbortController();
    this.listCall = call;
    this.listError = '';
    try {
      const { items } = await serversApi.list(call.signal);
      if (!call.signal.aborted) this.list = items;
    } catch (e) {
      if (!call.signal.aborted) { this.listError = failureOf(e).error; if (!this.list) this.list = []; }
    } finally {
      if (this.listCall === call) this.listCall = null;
    }
  }

  private clearConnection(): void {
    this.server = null; this.needSecret = false; this.error = null; this.note = ''; this.changed = null; this.hostKeys = [];
    this.machines = []; this.picks = []; this.sudo = false; this.tests = {}; this.discoveries = {}; this.rows = {}; this.targets = []; this.pulls = {}; this.order = [];
    this.running = false; this.reached = 0;
  }

  reset(): void {
    this.abortCall();
    this.clearConnection();
    this.provisionalServerId = null;
    this.draft = emptyConnectForm();
    this.step = 'connect';
    void this.load();
  }

  private fillDraft(entry: ServerEntry): void {
    this.draft = { host: entry.host, port: String(entry.port), user: entry.user, os: entry.os, auth: entry.auth };
  }

  /** Opens a saved server at the step it needs. */
  async use(entry: ServerEntry): Promise<void> {
    this.reset();
    this.server = entry;
    this.fillDraft(entry);
    this.sudo = !!entry.saltSudo;
    if (!entry.hasSecret) { this.needSecret = true; this.goto('connect'); return; }
    if (!entry.hostKey) { this.goto('trust'); await this.fetchKeys(); return; }
    if (entry.running) { await this.attachRunning(); return; }
    await this.loadMachines();
  }

  async forget(entry: ServerEntry): Promise<void> {
    await this.guard('forget', (signal) => serversApi.forget(entry.id, signal), () => {
      this.memory = forgetServer(this.memory, entry.id); saveMemory(browserStorage(), this.memory);
      if (this.server?.id === entry.id) this.clearConnection();
      if (this.provisionalServerId === entry.id) this.provisionalServerId = null;
      if (this.list) this.list = this.list.filter((saved) => saved.id !== entry.id);
      void this.load();
    });
  }

  /* ---------------------------------------------------------------- connect */

  /** The secret and passphrase are used for this one call and are not kept. */
  async connect(form: ConnectForm, secret: string, passphrase: string): Promise<boolean> {
    const draft = { ...form };
    if (connectProblems(draft, true).length) return false;
    const previous = this.server;
    if (previous && matchesConnection(draft, previous)) {
      if (secret) return this.saveSecret(draft.auth, secret, passphrase);
      if (!previous.hasSecret) return false;
      this.openConnection(previous);
      return true;
    }

    const result = await this.guard('connect', async (signal) => {
      let entry: ServerEntry;
      let added = false;
      let list: ServerEntry[] | undefined;
      try {
        entry = await serversApi.add(buildAddBody(draft, secret, secret ? passphrase : ''), signal);
        added = true;
      } catch (e) {
        signal.throwIfAborted();
        if (failureOf(e).reason !== 'duplicate') throw e;
        let saved = findConnection(draft, this.list);
        if (!saved || (!secret && !saved.hasSecret)) {
          list = (await serversApi.list(signal)).items;
          signal.throwIfAborted();
          saved = findConnection(draft, list);
        }
        if (!saved || (!saved.hasSecret && !secret)) throw e;
        entry = secret ? await this.storeSecret(saved, draft.auth, secret, passphrase, signal) : saved;
      }
      signal.throwIfAborted();
      let forgotten: string | undefined;
      if (previous && previous.id === this.provisionalServerId && !previous.hostKey && previous.id !== entry.id) {
        try { await serversApi.forget(previous.id, signal); forgotten = previous.id; } catch { /* Keep the old entry if it cannot be removed. */ }
      }
      return { entry, added, list, forgotten };
    }, ({ entry, added, list, forgotten }) => {
      if (list) {
        this.listCall?.abort();
        this.listCall = null;
        this.list = list;
        this.listError = '';
      }
      if (forgotten && this.list) this.list = this.list.filter((saved) => saved.id !== forgotten);
      if (previous?.id !== entry.id) {
        this.clearConnection();
        this.provisionalServerId = added ? entry.id : null;
      }
      if (!matchesConnection(this.draft, entry)) this.fillDraft(entry);
      this.openConnection(entry);
    });
    return !!result;
  }

  async saveSecret(auth: AuthKind, secret: string, passphrase: string): Promise<boolean> {
    const server = this.server;
    if (!server || !secret) return false;
    return !!(await this.guard('secret', (signal) => this.storeSecret(server, auth, secret, passphrase, signal), (entry) => this.openConnection(entry)));
  }

  private async storeSecret(server: ServerEntry, auth: AuthKind, secret: string, passphrase: string, signal: AbortSignal): Promise<ServerEntry> {
    await serversApi.setSecret(server.id, auth, secret, passphrase, signal);
    return { ...server, hasSecret: true, auth };
  }

  private openConnection(entry: ServerEntry): void {
    this.needSecret = false;
    if (entry !== this.server) this.setServer(entry);
    this.sudo = !!entry.saltSudo;
    if (entry.hostKey) void this.loadMachines();
    else { this.goto('trust'); void this.fetchKeys(); }
  }

  /* ------------------------------------------------------------------ trust */

  async fetchKeys(): Promise<void> {
    const server = this.server;
    if (!server) return;
    await this.guard('keys', (signal) => serversApi.hostKeys(server.id, signal), ({ keys }) => { this.hostKeys = keys; });
  }

  async trust(fingerprint: string): Promise<void> {
    const server = this.server;
    if (!server || !fingerprint) return;
    await this.guard('pin', (signal) => serversApi.pin(server.id, fingerprint, signal), (entry) => {
      this.setServer(entry);
      this.hostKeys = [];
      void this.loadMachines();
    });
  }

  async replaceKey(): Promise<void> {
    const server = this.server, changed = this.changed;
    if (!server || !changed || !changed.pinned || !changed.presented) return;
    await this.guard('replace', (signal) => serversApi.replaceKey(server.id, changed.pinned, changed.presented, signal), (entry) => {
      this.setServer(entry);
      this.changed = null;
      void this.loadMachines();
    });
  }

  /* ------------------------------------------------------------------- pick */

  async loadMachines(autoSudo = true): Promise<void> {
    const server = this.server;
    if (!server) return;
    this.goto('pick');
    this.note = '';
    this.machines = [];
    this.picks = [];
    await this.guard('machines', async (signal): Promise<{ machines: MachineView[]; entry?: ServerEntry }> => {
      await this.sudoRetries.get(server.id)?.catch(() => {});
      signal.throwIfAborted();
      try {
        return await withTimeout(signal, 100_000, (timed) => serversApi.machines(server.id, timed));
      } catch (e) {
        signal.throwIfAborted();
        if (!autoSudo || server.os !== 'linux' || this.sudo || failureOf(e).reason !== 'salt-master') throw e;
        const retry = this.retryMachinesWithSudo(server.id, e, signal);
        this.sudoRetries.set(server.id, retry);
        try { return await retry; } finally { this.sudoRetries.delete(server.id); }
      }
    }, ({ machines, entry }) => {
      if (entry) {
        this.setServer(entry);
        this.sudo = true;
        this.note = SV_TEXT.pickSudoSwitched;
      }
      this.machines = dedupeMachines(machines);
      this.picks = initialPicks(this.machines);
    });
  }

  private async retryMachinesWithSudo(id: string, original: unknown, signal: AbortSignal): Promise<{ machines: MachineView[]; entry: ServerEntry }> {
    let entry: ServerEntry | undefined;
    try {
      // Let the settings write settle so backing out can still undo it.
      entry = await serversApi.patch(id, buildPatchBody({ saltSudo: true }));
      signal.throwIfAborted();
      const { machines } = await withTimeout(signal, 100_000, (timed) => serversApi.machines(id, timed));
      signal.throwIfAborted();
      return { machines, entry };
    } catch (e) {
      if (entry) await withTimeout(new AbortController().signal, 5_000, (timed) => serversApi.patch(id, buildPatchBody({ saltSudo: false }), timed)).catch(() => {});
      signal.throwIfAborted();
      throw failureOf(e).reason === 'timeout' ? e : original;
    }
  }

  async setSudo(on: boolean): Promise<void> {
    const server = this.server;
    if (server?.os !== 'linux' || on === this.sudo) return;
    await this.guard('machines', async (signal) => {
      await this.sudoRetries.get(server.id)?.catch(() => {});
      signal.throwIfAborted();
      return serversApi.patch(server.id, buildPatchBody({ saltSudo: on }), signal);
    }, (entry) => {
      this.setServer(entry);
      this.sudo = !!entry.saltSudo;
      void this.loadMachines(false);
    });
  }

  toggle(id: string, on: boolean): void { this.picks = togglePick(this.picks, this.machines, id, on); }

  async confirmMachines(): Promise<void> {
    const server = this.server;
    if (!server || this.picks.length === 0) return;
    await this.guard('save-machines', (signal) => serversApi.patch(server.id, buildPatchBody({ minionIds: this.picks }), signal), (entry) => {
      this.setServer(entry);
      this.tests = {}; this.discoveries = {}; this.rows = {}; this.targets = [];
      this.goto('reach');
    });
  }

  /* ------------------------------------------------------------------ reach */

  async addFolder(kind: string, path: string): Promise<boolean> {
    const server = this.server;
    if (!server) return false;
    const next = withFolder(server.folders, kind, path);
    return this.saveFolders(next);
  }

  async addFolders(additions: readonly { kind: string; path: string }[]): Promise<boolean> {
    const server = this.server;
    if (!server) return false;
    const next = withFolders(server.folders, additions);
    const count = (folders: FolderMap | undefined) => Object.values(folders ?? {}).reduce((n, list) => n + list.length, 0);
    if (count(next) === count(server.folders)) return true;
    return this.saveFolders(next);
  }

  async removeFolder(kind: string, path: string): Promise<boolean> {
    const server = this.server;
    if (!server) return false;
    return this.saveFolders(withoutFolder(server.folders, kind, path));
  }

  /** Adding a folder keeps the earlier answers on the page, marked as out of date by the missing line for the new folder. Removing one clears them. */
  private async saveFolders(next: FolderMap, keepTests = false): Promise<boolean> {
    const server = this.server;
    if (!server) return false;
    return !!(await this.guard('folders', (signal) => serversApi.patch(server.id, buildPatchBody({ folders: next }), signal), (entry) => {
      this.setServer(entry);
      if (!keepTests) this.tests = {};
    }));
  }

  /** Adds the folders the recorder named, then checks the machine again so each new folder shows what it holds. */
  async addSuggestions(machine: string, list: readonly { kind: string; path: string }[]): Promise<void> {
    if (!this.server || list.length === 0) return;
    const saved = await this.addFolders(list);
    if (saved) { this.tests = {}; await this.testMachine(machine, false); }
  }

  /** Asks the machine's Blackbox where everything is. Never throws; a machine with no list gives `found: false`. */
  async discover(machine: string): Promise<void> {
    const server = this.server;
    if (!server) return;
    this.discoveries[machine] = { state: 'running' };
    try {
      const discovery = await serversApi.sources(server.id, machine);
      this.discoveries[machine] = { state: 'done', discovery };
      if (discovery.found) this.remember((m) => recordPatterns(m, server.id, machine, patternsFrom(discovery)));
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      const f = failureOf(e);
      if (f.reason === 'host-key-changed') { delete this.discoveries[machine]; this.fail(e); return; }
      this.discoveries[machine] = { state: 'failed', failure: f };
    }
  }

  async testMachine(machine: string, andFind = true): Promise<void> {
    const server = this.server;
    if (!server) return;
    this.tests[machine] = { state: 'running' };
    try {
      const result = await serversApi.test(server.id, machine);
      this.tests[machine] = { state: 'done', result };
      this.remember((m) => recordCheck(m, server.id, machine, new Date(), result.blackbox ? result.blackbox.ok : undefined));
      if (andFind && this.discoveries[machine]?.state !== 'running') void this.discover(machine);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      const f = failureOf(e);
      if (f.reason === 'host-key-changed') { delete this.tests[machine]; this.fail(e); return; }
      this.tests[machine] = { state: 'failed', failure: f };
    }
  }

  async testAll(): Promise<void> {
    for (const machine of this.server?.minionIds ?? []) { if (this.disposed) return; await this.testMachine(machine); }
  }

  get readable(): PullTarget[] {
    const out: PullTarget[] = [];
    for (const machine of this.server?.minionIds ?? []) { const t = this.tests[machine]; if (t?.result) out.push(...readableTargets(machine, t.result)); }
    return out;
  }

  /* -------------------------------------------------------------------- has */

  private defaultRange(): RangeChoice { return this.caseRange().state === 'ok' ? 'case' : '72'; }

  /** Moves to the file lists and reads each one. A row the person already set keeps its choice. */
  async openHas(): Promise<void> {
    this.targets = this.readable;
    const kept: Record<string, RowState> = {};
    for (const t of this.targets) kept[t.key] = this.rows[t.key] ?? { on: true, range: this.defaultRange(), state: 'idle', plan: null };
    this.rows = kept;
    this.goto('has');
    for (const t of this.targets) { if (this.disposed) return; if (this.rows[t.key]?.state === 'idle') await this.planRow(t.key); }
  }

  async planRow(key: string): Promise<void> {
    const server = this.server, row = this.rows[key], target = this.targets.find((t) => t.key === key);
    if (!server || !row || !target) return;
    const hours = rangeHours(row.range, this.caseRange(), this.env.now());
    if (hours === null) { row.state = 'failed'; row.plan = null; row.failure = { status: 0, error: caseRangeProblem(this.caseRange()) || 'Choose a time range.', reason: 'invalid' }; return; }
    row.state = 'loading'; row.failure = undefined;
    try {
      const plan = await serversApi.plan(server.id, buildPullBody(target, hours, undefined, patternFor(this.memory, server.id, target.machine, target.dir)));
      row.plan = plan; row.state = 'ok';
      if (plan.files.length === 0 || plan.problem) row.on = false;
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      const f = failureOf(e);
      if (f.reason === 'host-key-changed') { row.state = 'idle'; this.fail(e); return; }
      row.state = 'failed'; row.plan = null; row.failure = f;
    }
  }

  async setRange(key: string, range: RangeChoice): Promise<void> {
    const row = this.rows[key];
    if (!row) return;
    row.range = range;
    await this.planRow(key);
  }

  async setAllRanges(range: RangeChoice): Promise<void> {
    for (const t of this.targets) { if (this.disposed) return; const row = this.rows[t.key]; if (row) { row.range = range; await this.planRow(t.key); } }
  }

  get chosenKeys(): string[] {
    return this.targets.filter((t) => { const r = this.rows[t.key]; return !!r && r.on && r.state === 'ok' && !!r.plan && r.plan.files.length > 0 && !r.plan.problem; }).map((t) => t.key);
  }

  /* ------------------------------------------------------------------- pull */

  private bodyFor(target: PullTarget, resume?: string) {
    const row = this.rows[target.key];
    const hours = row ? rangeHours(row.range, this.caseRange(), this.env.now()) : null;
    return buildPullBody(target, hours ?? 0, resume, patternFor(this.memory, this.server?.id ?? '', target.machine, target.dir));
  }

  /** Copies the chosen items one after the other. One copy runs on a server at a time. */
  async startPulls(): Promise<void> {
    const keys = this.chosenKeys;
    if (!this.server || keys.length === 0 || this.running) return;
    this.pulls = {}; this.order = keys; this.stopAsked = false; this.error = null;
    for (const k of keys) this.pulls[k] = { state: 'queued', planned: this.rows[k]?.plan?.bytes ?? 0 };
    this.goto('pull');
    this.running = true;
    try {
      for (const key of keys) {
        if (this.disposed) return;
        if (this.stopAsked) { this.pulls[key].state = 'stopped'; continue; }
        const target = this.targets.find((t) => t.key === key);
        if (target) await this.runOne(key, target);
      }
    } finally { this.running = false; }
    this.finishIfDone();
  }

  private finishIfDone(): void {
    if (this.order.length > 0 && this.order.every((k) => this.pulls[k]?.state === 'done')) this.goto('land');
  }

  private async runOne(key: string, target: PullTarget, resume?: string): Promise<void> {
    const server = this.server;
    if (!server) return;
    const state = this.pulls[key];
    state.state = 'running'; state.failure = undefined; state.stopping = false;
    try {
      let job = await serversApi.startPull(server.id, this.bodyFor(target, resume));
      state.job = job;
      job = await this.follow(server.id, key, job);
      state.job = job;
      state.state = job.state === 'running' ? 'running' : job.state;
      if (job.state === 'failed' && job.failure) state.failure = job.failure;
      if (state.state === 'done') this.remember((m) => recordPull(m, server.id, target.machine, new Date()));
    } catch (e) {
      const f = failureOf(e);
      if (f.reason === 'host-key-changed') { state.state = 'stopped'; this.fail(e); return; }
      state.state = 'failed'; state.failure = f;
    }
  }

  private async follow(serverId: string, key: string, first: JobView): Promise<JobView> {
    let job = first, misses = 0;
    while (job.state === 'running' && !this.disposed) {
      await sleep(POLL_MS);
      try { job = await serversApi.pull(serverId, job.id); this.pulls[key].job = job; misses = 0; } catch (e) {
        if (!(e instanceof ServersError) || e.failure.reason !== 'offline' || ++misses > 5) throw e;
      }
    }
    return job;
  }

  /** Asks the server to stop the copy that is running. The page then sees it as stopped. */
  async stop(): Promise<void> {
    const server = this.server;
    if (!server) return;
    this.stopAsked = true;
    for (const key of this.order) {
      const p = this.pulls[key];
      if (p?.state === 'running' && p.job) {
        p.stopping = true;
        try { await serversApi.cancel(server.id, p.job.id); } catch (e) { p.stopping = false; this.error = failureOf(e); }
      }
    }
  }

  /** Starts a stopped or failed copy again. It picks up at the byte where it stopped. */
  async resume(key: string): Promise<void> {
    const target = this.targets.find((t) => t.key === key), p = this.pulls[key];
    if (!target || !p || this.running) return;
    this.running = true; this.stopAsked = false;
    try { await this.runOne(key, target, p.job?.id); } finally { this.running = false; }
    this.finishIfDone();
  }

  /** A copy that was already running when the panel opened. */
  async attachRunning(): Promise<void> {
    const server = this.server;
    if (!server) return;
    await this.guard('pulls', (signal) => serversApi.pulls(server.id, signal), ({ items }) => {
      const job = items.find((j) => j.state === 'running');
      if (job) void this.showRunning(server, job);
      else void this.loadMachines();
    });
  }

  private async showRunning(server: ServerEntry, job: JobView): Promise<void> {
    const key = (job.machine ?? '') + '|' + (folderKindInfo(job.kind)?.kind ?? job.kind) + '|' + job.dir;
    this.order = [key];
    this.pulls = { [key]: { state: 'running', job, planned: 0 } };
    this.targets = [{ machine: job.machine ?? '', kind: folderKindInfo(job.kind)?.kind ?? job.kind, pullKind: job.kind, label: folderKindInfo(job.kind)?.label ?? job.kind, dir: job.dir, key }];
    this.note = 'attached';
    this.goto('pull');
    this.running = true;
    try {
      const done = await this.follow(server.id, key, job);
      this.pulls[key].job = done;
      this.pulls[key].state = done.state === 'running' ? 'running' : done.state;
      if (done.state === 'failed' && done.failure) this.pulls[key].failure = done.failure;
    } catch (e) { this.pulls[key].state = 'failed'; this.pulls[key].failure = failureOf(e); } finally { this.running = false; }
    this.finishIfDone();
  }

  percent(key: string): number | null {
    const p = this.pulls[key];
    return p?.job ? jobPercent(p.job, p.planned) : null;
  }

  goLand(): void { this.goto('land'); }

  /** Every target the server could pull, whether or not it was tested. Used when the person wants to see the folders only. */
  get allowedTargets(): PullTarget[] { return pullTargets(this.server?.minionIds ?? [], this.server?.folders); }
}
