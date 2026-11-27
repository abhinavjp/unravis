import { serversApi } from './serversApi';
import {
  FAILURE_TEXT, ServersError, buildAddBody, buildPatchBody, buildPullBody, caseRange, caseRangeProblem, connectProblems, folderKindInfo, initialPicks, jobPercent,
  pullTargets, readableTargets, rangeHours, togglePick, withFolder, withoutFolder,
  type AuthKind, type ConnectForm, type Failure, type FolderMap, type HostKey, type JobView, type MachineView, type PlanView, type PullTarget, type RangeChoice,
  type ServerEntry, type TestResult
} from './servers';
import type { WindowFields } from './tracing/windows';

/**
 * The state and the steps of the Servers screen. Everything here comes from the backend: nothing is made up for the page.
 * A password or key passphrase is an argument of `connect` and `saveSecret` only. It is passed on and never stored in this object.
 */

export type Step = 'connect' | 'trust' | 'pick' | 'reach' | 'has' | 'pull' | 'land';
export const STEP_ORDER: Step[] = ['connect', 'trust', 'pick', 'reach', 'has', 'pull', 'land'];

export interface ChangedKey { pinned: string; presented: string; next: string; error: string }
export interface TestState { state: 'running' | 'done' | 'failed'; result?: TestResult; failure?: Failure }
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

  constructor(env: Env) { this.env = env; }

  list = $state<ServerEntry[] | null>(null);
  listError = $state('');
  step = $state<Step>('connect');
  reached = $state(0);
  server = $state<ServerEntry | null>(null);
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
  rows = $state<Record<string, RowState>>({});
  targets = $state<PullTarget[]>([]);
  pulls = $state<Record<string, PullState>>({});
  order = $state<string[]>([]);
  running = $state(false);

  /* ------------------------------------------------------------- basics */

  dispose(): void { this.disposed = true; }

  caseRange() { return caseRange(this.env.windows(), this.env.now()); }

  private goto(step: Step): void {
    this.step = step;
    this.reached = Math.max(this.reached, STEP_ORDER.indexOf(step));
  }

  /** Back to an earlier step. Not while a copy runs. */
  go(step: Step): void {
    if (this.running || STEP_ORDER.indexOf(step) > this.reached) return;
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

  private async guard<T>(label: string, fn: () => Promise<T>): Promise<T | undefined> {
    this.busy = label; this.error = null;
    try { return await fn(); } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return undefined;
      this.fail(e);
      return undefined;
    } finally { this.busy = ''; }
  }

  private setServer(entry: ServerEntry): void {
    this.server = entry;
    if (this.list) this.list = this.list.some((s) => s.id === entry.id) ? this.list.map((s) => (s.id === entry.id ? { ...entry, running: s.running } : s)) : [...this.list, entry];
  }

  /* -------------------------------------------------------- saved servers */

  async load(): Promise<void> {
    this.listError = '';
    try { this.list = (await serversApi.list()).items; } catch (e) { this.listError = failureOf(e).error; if (!this.list) this.list = []; }
  }

  reset(): void {
    this.server = null; this.needSecret = false; this.error = null; this.note = ''; this.changed = null; this.hostKeys = [];
    this.machines = []; this.picks = []; this.tests = {}; this.rows = {}; this.targets = []; this.pulls = {}; this.order = [];
    this.running = false; this.reached = 0; this.step = 'connect';
    void this.load();
  }

  /** Opens a saved server at the step it needs. */
  async use(entry: ServerEntry): Promise<void> {
    this.reset();
    this.server = entry;
    this.sudo = !!entry.saltSudo;
    if (!entry.hasSecret) { this.needSecret = true; this.goto('connect'); return; }
    if (!entry.hostKey) { this.goto('trust'); await this.fetchKeys(); return; }
    if (entry.running) { await this.attachRunning(); return; }
    await this.loadMachines();
  }

  async forget(entry: ServerEntry): Promise<void> {
    await this.guard('forget', async () => { await serversApi.forget(entry.id); await this.load(); });
  }

  /* ---------------------------------------------------------------- connect */

  /** The secret and passphrase are used for this one call and are not kept. */
  async connect(form: ConnectForm, secret: string, passphrase: string): Promise<boolean> {
    if (connectProblems(form, secret !== '').length) return false;
    const entry = await this.guard('connect', () => serversApi.add(buildAddBody(form, secret, passphrase)));
    if (!entry) return false;
    this.setServer(entry);
    this.sudo = false;
    this.goto('trust');
    await this.fetchKeys();
    return true;
  }

  async saveSecret(auth: AuthKind, secret: string, passphrase: string): Promise<boolean> {
    const server = this.server;
    if (!server || !secret) return false;
    const ok = await this.guard('secret', async () => { await serversApi.setSecret(server.id, auth, secret, passphrase); return true; });
    if (!ok) return false;
    this.needSecret = false;
    this.setServer({ ...server, hasSecret: true, auth });
    if (!server.hostKey) { this.goto('trust'); await this.fetchKeys(); } else await this.loadMachines();
    return true;
  }

  /* ------------------------------------------------------------------ trust */

  async fetchKeys(): Promise<void> {
    const server = this.server;
    if (!server) return;
    const res = await this.guard('keys', () => serversApi.hostKeys(server.id));
    if (res) this.hostKeys = res.keys;
  }

  async trust(fingerprint: string): Promise<void> {
    const server = this.server;
    if (!server || !fingerprint) return;
    const entry = await this.guard('pin', () => serversApi.pin(server.id, fingerprint));
    if (!entry) return;
    this.setServer(entry);
    this.hostKeys = [];
    await this.loadMachines();
  }

  async replaceKey(): Promise<void> {
    const server = this.server, changed = this.changed;
    if (!server || !changed || !changed.pinned || !changed.presented) return;
    const entry = await this.guard('replace', () => serversApi.replaceKey(server.id, changed.pinned, changed.presented));
    if (!entry) return;
    this.setServer(entry);
    this.changed = null;
    await this.loadMachines();
  }

  /* ------------------------------------------------------------------- pick */

  async loadMachines(): Promise<void> {
    const server = this.server;
    if (!server) return;
    this.goto('pick');
    const res = await this.guard('machines', () => serversApi.machines(server.id));
    if (!res) return;
    this.machines = res.machines;
    this.picks = initialPicks(res.machines);
  }

  toggle(id: string, on: boolean): void { this.picks = togglePick(this.picks, this.machines, id, on); }

  async confirmMachines(): Promise<void> {
    const server = this.server;
    if (!server || this.picks.length === 0) return;
    const entry = await this.guard('save-machines', () => serversApi.patch(server.id, buildPatchBody({ minionIds: this.picks, saltSudo: this.sudo })));
    if (!entry) return;
    this.setServer(entry);
    this.tests = {}; this.rows = {}; this.targets = [];
    this.goto('reach');
  }

  /* ------------------------------------------------------------------ reach */

  async addFolder(kind: string, path: string): Promise<boolean> {
    const server = this.server;
    if (!server) return false;
    const next = withFolder(server.folders, kind, path);
    return this.saveFolders(next);
  }

  async removeFolder(kind: string, path: string): Promise<boolean> {
    const server = this.server;
    if (!server) return false;
    return this.saveFolders(withoutFolder(server.folders, kind, path));
  }

  private async saveFolders(next: FolderMap): Promise<boolean> {
    const server = this.server;
    if (!server) return false;
    const entry = await this.guard('folders', () => serversApi.patch(server.id, buildPatchBody({ folders: next })));
    if (!entry) return false;
    this.setServer(entry);
    this.tests = {};
    return true;
  }

  async testMachine(machine: string): Promise<void> {
    const server = this.server;
    if (!server) return;
    this.tests[machine] = { state: 'running' };
    try {
      const result = await serversApi.test(server.id, machine);
      this.tests[machine] = { state: 'done', result };
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
      const plan = await serversApi.plan(server.id, buildPullBody(target, hours));
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
    return buildPullBody(target, hours ?? 0, resume);
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
    const res = await this.guard('pulls', () => serversApi.pulls(server.id));
    const job = res?.items.find((j) => j.state === 'running');
    if (!job) { await this.loadMachines(); return; }
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
