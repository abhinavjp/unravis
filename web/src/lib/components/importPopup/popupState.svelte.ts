import { CaseApiError, cancelImportJob, importRunner, runImportQueue, startCheckJob, waitForCheckJob } from '../../cases';
import type { BatchItem, BatchOutcome, BatchRun, CheckFileVerdict, CheckReport } from '../../cases';
import { ackTextFor, batchReasonText, errorText, importerLabel, reasonText } from '../../copy/importCopy';
import { filesFromDrop } from '../../dropEntries';
import { stageFiles } from '../../fileKinds';
import type { StagedFile } from '../../fileKinds';
import type { Query } from '../../types';
import { saveHost } from '../../wizardHosts.svelte';
import type { SaveHostResult } from '../../wizardHosts.svelte';
import { isZipName, readZip } from '../../zipRead';

/**
 * The step machine and the work of the "Import files" popup (wireframe: prototype/import-popup-WIREFRAME.html). It holds what the person
 * sees and decides: the step, the fix cards, the files ticked, the acknowledgement and the close confirmation. It also runs the check
 * (`continueToCheck`), the import (`startImport`) and the short re-checks after a fix; the server calls go through `PopupApi` so a test can fake them.
 *
 * Naming used by the step bodies:
 *  - A file's key is `fileKeyOf(file)` (name|size|lastModified); the check report echoes it back. Keys are unique in `files`.
 *  - A fix card is named by `cardIdOf(verdict)`: the server name for a missing computer, `no-computer:<kind>` or `needs-zone:<kind>` otherwise.
 *  - A group (what `toggle` and `groupAll` take) is `groupOf(verdict)`: the card's name, or `kind:<importer>` for files that are ready.
 *  - `focus` asks the popup to move focus to the element carrying that `data-focus` value: `head-<group>` (a group's header button),
 *    `card-<card>` (a card's main button or its Undo), `name:<card>` (a card's name field), `ack`, `keep`, `close` and `open-tl`.
 */

export type PopupStep = 1 | 2 | 3;
export type CardState = 'open' | 'resolved' | 'leftout';

/** What the popup hands to its body and footer. */
export interface PopupContext {
  state: PopupState;
  caseId: string;
  caseTitle: string;
  query: Query;
  zone: string;
}

/** One line of the Importing step: a kind of log, the files of it that ended, and how many there are. */
export interface ImportGroup {
  name: string;
  done: number;
  count: number;
}

/** What the Result step shows. */
export interface ImportResult {
  imported: { name: string; count: number }[];
  notImported: { name: string; reason: string }[];
  computers: string[];
}

/** The server calls the popup makes. */
export interface PopupApi {
  /** Reads the files without keeping them and answers one verdict per file. Aborting the signal frees the server's slot. */
  check(caseId: string, files: { key: string; file: File }[], signal: AbortSignal): Promise<CheckReport>;
  /** Sends one file: read it once, then keep it with the notice acknowledged. */
  runner(caseId: string): BatchRun;
  /** Adds one computer to the host list. The kind of file decides whether a zone is needed; a server name card needs none. */
  addComputer(label: string): Promise<SaveHostResult>;
}

export const realPopupApi: PopupApi = {
  async check(caseId, files, signal) {
    const job = await startCheckJob({ caseId, files }, signal);
    try {
      return await waitForCheckJob(caseId, job, { signal });
    } catch (error) {
      if (signal.aborted) void cancelImportJob(caseId, job.id).catch(() => {});
      throw error;
    }
  },
  runner: (caseId) => importRunner(caseId, ackTextFor()),
  addComputer: (label) => saveHost(label, '', true)
};

export interface PopupState {
  /** The case the files go into. The shell keeps it in step with its own prop. */
  caseId: string;
  step: PopupStep;
  checking: boolean;
  /** Some files are being read again after a fix. Import waits for it. */
  rechecking: boolean;
  /** Why the check could not finish, or ''. */
  checkError: string;
  importing: boolean;
  /** Cancel was pressed: the file being sent finishes, the rest are not started. Import waits for it. */
  stopping: boolean;
  importDone: boolean;
  importProgress: number;
  importTotal: number;
  importGroups: ImportGroup[];
  result: ImportResult | null;
  expanded: Record<string, boolean>;
  /** Keyed by file key. A file with no entry counts as ticked. */
  selected: Record<string, boolean>;
  /** Keyed by card id. A card with no entry is open. */
  cards: Record<string, CardState>;
  /** The name of the computer added for each resolved card, keyed by card id. */
  labels: Record<string, string>;
  /** The kind picked for a file the check could not place, keyed by file key. */
  kinds: Record<string, string>;
  ack: boolean;
  discard: boolean;
  closed: boolean;
  /** Set with `closed` when the person chose "Open timeline". */
  timeline: boolean;
  retryServerName: string | null;
  /** The `data-focus` value to move focus to once it is on screen, or null. */
  focus: string | null;
  files: StagedFile[];
  report: CheckReport | null;

  fixesNeeded(): number;
  targetCount(): number;
  canImport(): boolean;
  listedFiles(): number;
  cardIds(): string[];
  isSelected(key: string): boolean;

  addFiles(files: File[]): Promise<void>;
  drop(event: DragEvent): Promise<void>;
  continueToCheck(): void;
  goTo(step: number): void;
  toggle(group: string): void;
  setSelected(key: string, on: boolean): void;
  groupAll(group: string, on: boolean): void;
  /** `label` is the computer that was added for the card. It defaults to the card's own name (a server name card). */
  resolveCard(name: string, label?: string): void;
  leaveOut(name: string): void;
  undoCard(name: string): void;
  addAllServers(): void;
  removeUnsupported(key: string): void;
  setKind(key: string, kind: string): void;
  openRetry(name: string): void;
  startImport(): void;
  cancelImport(): void;
  importMore(): void;
  openTimeline(): void;
  requestClose(): void;
  keepFiles(): void;
  discardNow(): void;
  /** Ends every request in flight. The shell calls it when the popup goes away. */
  dispose(): void;
}

export const fileKeyOf = (item: Pick<StagedFile, 'name' | 'size' | 'file'>): string => [item.name, item.size, item.file?.lastModified ?? 0].join('|');

/** The fix card a verdict belongs to, or null when the file needs no fix. */
export function cardIdOf(verdict: CheckFileVerdict): string | null {
  if (verdict.verdict === 'needs-computer') return verdict.serverName;
  if (verdict.verdict === 'no-computer') return 'no-computer:' + verdict.importer;
  if (verdict.verdict === 'needs-zone') return 'needs-zone:' + verdict.importer;
  return null;
}

/** The group a verdict is listed under. */
export const groupOf = (verdict: CheckFileVerdict): string => cardIdOf(verdict) ?? (verdict.verdict === 'ready' ? 'kind:' + verdict.importer : 'unsupported');

const GROUP_NAMES: Record<string, string> = { 'iis-w3c': 'IIS web logs', 'sql-errorlog': 'SQL error log' };

/** How the Importing and Result steps call a kind of log. */
export const groupNameOf = (importer: string): string => GROUP_NAMES[importer] ?? (importer.startsWith('log4net') ? 'log4net' : importerLabel(importer));

const initialExpanded = (): Record<string, boolean> => ({ unsupported: true });

export function createPopupState(api: PopupApi = realPopupApi): PopupState {
  const cardOf = (id: string): CardState => s.cards[id] ?? 'open';
  const checked = (): CheckFileVerdict[] => s.report?.files ?? [];
  const leftOut = (verdict: CheckFileVerdict): boolean => {
    const id = cardIdOf(verdict);
    return id !== null && cardOf(id) === 'leftout';
  };
  const cardIds = (): string[] => {
    const ids: string[] = [];
    if (!s.report && s.retryServerName) ids.push(s.retryServerName);
    for (const verdict of checked()) {
      const id = cardIdOf(verdict);
      if (id !== null && !ids.includes(id)) ids.push(id);
    }
    return ids;
  };
  const fixesNeeded = (): number => cardIds().filter((id) => cardOf(id) === 'open').length;
  const isSelected = (key: string): boolean => s.selected[key] !== false;
  const targets = (): CheckFileVerdict[] => checked().filter((verdict) => verdict.verdict !== 'not-supported' && isSelected(verdict.key) && !leftOut(verdict));
  /** The keys of the files that name this computer. Only a server name card has any; the other cards name no computer in the file. */
  const filesOfServer = (name: string): string[] => checked().filter((verdict) => verdict.verdict === 'needs-computer' && verdict.serverName === name).map((verdict) => verdict.key);
  const stagedBy = (key: string): StagedFile | undefined => s.files.find((item) => fileKeyOf(item) === key);

  // Requests in flight. A number that no longer matches its counter means the person moved on, and the answer is dropped.
  let checkRun = 0;
  let checkAbort: AbortController | null = null;
  let pendingChecks = 0;
  let importRun = 0;
  let stopAfter: AbortController | null = null;
  let addingAll = false;
  let nextId = 1;
  const leave = new AbortController();
  // What earlier runs of this popup already did, so the Result step tells the whole story even after a refused file sent the person back.
  let carryImported = new Map<string, number>();
  let carryNotImported: { name: string; reason: string }[] = [];

  const abortChecks = () => {
    checkAbort?.abort();
    checkAbort = null;
    checkRun += 1;
    pendingChecks = 0;
    s.rechecking = false;
  };

  // Everything the person built up, back to a fresh popup. Used when files are discarded and when the person imports more.
  const clearSession = () => {
    abortChecks();
    importRun += 1;
    stopAfter?.abort();
    stopAfter = null;
    carryImported = new Map();
    carryNotImported = [];
    s.step = 1; s.checking = false; s.checkError = ''; s.importing = false; s.stopping = false; s.importDone = false;
    s.importProgress = 0; s.importTotal = 0; s.importGroups = []; s.result = null;
    s.expanded = initialExpanded(); s.selected = {}; s.cards = {}; s.labels = {}; s.kinds = {};
    s.ack = false; s.discard = false; s.retryServerName = null; s.focus = null;
    s.files = []; s.report = null;
  };

  /** What the check would say about a file it was not asked about: one that is refused before sending, or one the server left out. */
  function localVerdict(item: StagedFile): CheckFileVerdict {
    const reason = item.refusal === 'compressed' ? 'archive-extract-first' : item.refusal === 'empty' ? 'no-rows' : '';
    return { key: fileKeyOf(item), name: item.name, size: item.size, verdict: 'not-supported', importer: '', tied: [], serverName: '', needsZone: false, reason, rows: 0, emitted: 0, rejected: 0 };
  }

  /** The server's verdicts in the order the files are listed. */
  function ordered(report: CheckReport): CheckFileVerdict[] {
    const byKey = new Map(report.files.map((verdict) => [verdict.key, verdict]));
    return s.files.map((item) => byKey.get(fileKeyOf(item)) ?? localVerdict(item));
  }

  /** The person asked to reopen on a server's card (Retry): once the report holds that card it opens, with its name field in focus. */
  function consumeRetry() {
    const name = s.retryServerName;
    if (name === null) return;
    s.retryServerName = null;
    if (!cardIds().includes(name)) return;
    s.cards[name] = 'open'; s.expanded[name] = true; s.focus = 'name:' + name;
  }

  async function runCheck() {
    abortChecks();
    const mine = checkRun;
    const controller = new AbortController();
    checkAbort = controller;
    const toSend = s.files.filter((item) => item.refusal === '').map((item) => ({ key: fileKeyOf(item), file: item.file }));
    try {
      const report = toSend.length > 0 ? await api.check(s.caseId, toSend, controller.signal) : { files: [], checkedAt: '' };
      if (mine !== checkRun) return;
      s.report = { files: ordered(report), checkedAt: report.checkedAt };
      s.checking = false;
      consumeRetry();
    } catch (error) {
      if (mine !== checkRun) return;
      s.checking = false;
      s.checkError = errorText(error instanceof CaseApiError ? error : { status: 0, reason: 'unreachable', message: '' });
    }
  }

  /**
   * Reads some files again, so a case the first check could not see (a file that names a second computer, say) shows up as a new card.
   * After a fix (`keepSame`), a file the check still places on the same card stays where it is, so a resolved card keeps its line and its Undo.
   * A file the check now calls ready also stays: it was ready for the person's fix.
   */
  async function recheck(keys: string[], keepSame: boolean) {
    const wanted = new Set(keys);
    const files = s.files.filter((item) => wanted.has(fileKeyOf(item))).map((item) => ({ key: fileKeyOf(item), file: item.file }));
    if (files.length === 0 || !s.report) return;
    const mine = checkRun;
    const signal = (checkAbort ??= new AbortController()).signal;
    pendingChecks += 1;
    s.rechecking = true;
    try {
      const fresh = new Map((await api.check(s.caseId, files, signal)).files.map((verdict) => [verdict.key, verdict]));
      if (mine !== checkRun || !s.report) return;
      s.report = {
        ...s.report,
        files: s.report.files.map((old) => {
          const next = fresh.get(old.key);
          if (!next || next.verdict === 'ready') return old;
          if (keepSame && next.verdict === old.verdict && cardIdOf(next) === cardIdOf(old)) return old;
          return next;
        })
      };
    } catch {
      // The import reports a case this missed, and sends the person back to its card.
    } finally {
      if (mine === checkRun) {
        pendingChecks -= 1;
        s.rechecking = pendingChecks > 0;
      }
    }
  }

  /** Takes files out of the list, the report and the choices (they were imported, or reported as failed). */
  function dropFiles(keys: ReadonlySet<string>) {
    if (keys.size === 0) return;
    s.files = s.files.filter((item) => !keys.has(fileKeyOf(item)));
    if (s.report) s.report = { ...s.report, files: s.report.files.filter((verdict) => !keys.has(verdict.key)) };
    for (const key of keys) { delete s.selected[key]; delete s.kinds[key]; }
  }

  /** Counts what the files did and takes them off the list, so they are never sent twice. */
  function settle(entries: ReadonlyArray<{ key: string; outcome: BatchOutcome }>, groupNames: ReadonlyMap<string, string>) {
    const ended = new Set<string>();
    for (const { key, outcome } of entries) {
      ended.add(key);
      if (outcome.verdict !== 'rejected') {
        const name = groupNames.get(key) ?? groupNameOf(outcome.importer);
        carryImported.set(name, (carryImported.get(name) ?? 0) + 1);
      } else {
        carryNotImported.push({ name: stagedBy(key)?.name ?? key, reason: batchReasonText(outcome) });
      }
    }
    dropFiles(ended);
  }

  function showResult() {
    const unsupported = checked()
      .filter((verdict) => verdict.verdict === 'not-supported')
      .map((verdict) => ({ name: verdict.name, reason: verdict.tied.length > 1 ? '' : (reasonText(verdict.reason) ?? '') }));
    s.result = {
      imported: [...carryImported].map(([name, count]) => ({ name, count })),
      notImported: [...carryNotImported, ...unsupported],
      computers: [...new Set(Object.values(s.labels))]
    };
    s.step = 3; s.importing = false; s.importDone = true; s.focus = 'open-tl';
  }

  async function finishImport(done: Array<{ key: string; outcome: BatchOutcome }>, cancelled: boolean, groupNames: ReadonlyMap<string, string>, mine: number) {
    // A refusal for an unknown computer is not a failure of the file: the person can fix it on its card and send it again.
    const refused = done.filter((entry) => entry.outcome.verdict === 'rejected' && entry.outcome.reason === 'unknown-host');
    const refusedKeys = new Set(refused.map((entry) => entry.key));
    settle(done.filter((entry) => !refusedKeys.has(entry.key)), groupNames);
    s.importing = false;
    if (cancelled) {
      s.stopping = false;
      s.step = s.files.length === 0 ? 1 : 2;
      return;
    }
    if (refused.length > 0) {
      s.step = 2; s.checking = false;
      await recheck([...refusedKeys], false);
      if (mine !== importRun) return;
      const hit = checked().find((verdict) => refusedKeys.has(verdict.key) && cardIdOf(verdict) !== null);
      const id = hit ? cardIdOf(hit) : null;
      if (id !== null) {
        s.openRetry(id);
        s.retryServerName = null;
        return;
      }
      settle(refused, groupNames);
    }
    showResult();
  }

  async function runImport(items: BatchItem[]) {
    const mine = ++importRun;
    const stop = new AbortController();
    stopAfter = stop;
    const groupNames = new Map(items.map((item) => [item.key, groupNameOf(item.importer)]));
    const groups: ImportGroup[] = [];
    for (const name of groupNames.values()) {
      const group = groups.find((entry) => entry.name === name);
      if (group) group.count += 1;
      else groups.push({ name, done: 0, count: 1 });
    }
    s.step = 3; s.importing = true; s.stopping = false; s.importDone = false; s.result = null;
    s.importProgress = 0; s.importTotal = items.length; s.importGroups = groups;
    const runner = api.runner(s.caseId);
    const done = await runImportQueue(items, (item) => runner(item, leave.signal), {
      signal: stop.signal,
      onDone: (key) => {
        if (mine !== importRun) return;
        s.importProgress += 1;
        const group = s.importGroups.find((entry) => entry.name === groupNames.get(key));
        if (group) group.done += 1;
      }
    });
    if (leave.signal.aborted || mine !== importRun) return;
    stopAfter = null;
    await finishImport(done, stop.signal.aborted, groupNames, mine);
  }

  // A .zip is opened here, so the server never gets an archive. One that cannot be opened stays on the list and is reported as not supported.
  async function expand(picked: File[]): Promise<File[]> {
    const out: File[] = [];
    for (const file of picked) {
      if (!isZipName(file.name)) { out.push(file); continue; }
      const zip = await readZip(file);
      if (zip.problem !== '' || zip.files.length === 0) out.push(file);
      else out.push(...zip.files);
    }
    return out;
  }

  const s: PopupState = $state<PopupState>({
    caseId: '', step: 1, checking: false, rechecking: false, checkError: '', importing: false, stopping: false, importDone: false,
    importProgress: 0, importTotal: 0, importGroups: [], result: null,
    expanded: initialExpanded(), selected: {}, cards: {}, labels: {}, kinds: {},
    ack: false, discard: false, closed: false, timeline: false,
    retryServerName: null, focus: null, files: [], report: null,

    fixesNeeded,
    targetCount: () => targets().length,
    canImport: () => fixesNeeded() === 0 && s.ack && !s.rechecking && !s.stopping && !s.checking && targets().length > 0,
    listedFiles: () => s.files.length,
    cardIds,
    isSelected,

    async addFiles(picked) {
      const opened = await expand(picked);
      const known = new Set(s.files.map(fileKeyOf));
      // The check names a file by its key, so the same file chosen twice is listed once.
      const rows = stageFiles(opened, nextId).filter((row) => {
        const key = fileKeyOf(row);
        if (known.has(key)) return false;
        known.add(key);
        return true;
      });
      nextId += opened.length;
      if (rows.length > 0) s.files = [...s.files, ...rows];
    },
    async drop(event) {
      const transfer = event.dataTransfer;
      if (!transfer) return;
      // Taken now: the browser clears the drop's items as soon as this handler yields.
      const items = Array.from(transfer.items ?? []);
      const plain = Array.from(transfer.files ?? []);
      const dropped = await filesFromDrop(items, plain);
      await s.addFiles(dropped.files);
    },
    continueToCheck() {
      if (s.files.length === 0) return;
      s.step = 2; s.checking = true; s.report = null; s.cards = {}; s.ack = false; s.checkError = '';
      for (const item of s.files) {
        const key = fileKeyOf(item);
        if (!(key in s.selected)) s.selected[key] = true;
      }
      void runCheck();
    },
    // Only back, and never while the import runs: leaving then would hide an import that is still reading files.
    goTo(step) {
      if (step < 1 || step >= s.step || s.importing) return;
      abortChecks();
      s.step = step as PopupStep;
      s.checking = false; s.importing = false; s.importDone = false;
    },
    toggle(group) {
      s.expanded[group] = !s.expanded[group];
      s.focus = 'head-' + group;
    },
    setSelected(key, on) { s.selected[key] = on; },
    groupAll(group, on) {
      for (const verdict of checked()) if (groupOf(verdict) === group) s.selected[verdict.key] = on;
    },
    resolveCard(name, label) {
      s.cards[name] = 'resolved'; s.labels[name] = label ?? name; s.focus = 'card-' + name;
      if (!s.report && s.files.length === 0) { s.retryServerName = null; s.focus = null; s.goTo(1); return; }
      void recheck(filesOfServer(name), true);
    },
    leaveOut(name) { s.cards[name] = 'leftout'; },
    undoCard(name) { s.cards[name] = 'open'; s.focus = 'card-' + name; },
    // Only the cards that name a server: the others need a name and a time zone typed in, so they cannot be added blind.
    addAllServers() {
      if (addingAll) return;
      const open = cardIds().filter((id) => cardOf(id) === 'open' && filesOfServer(id).length > 0);
      addingAll = true;
      void (async () => {
        const keys: string[] = [];
        try {
          for (const id of open) {
            const result = await api.addComputer(id);
            // A name already in the list is the goal reached.
            if (!result.ok && result.reason !== 'taken') continue;
            s.cards[id] = 'resolved'; s.labels[id] = id;
            keys.push(...filesOfServer(id));
          }
        } finally {
          addingAll = false;
        }
        s.focus = 'ack';
        await recheck(keys, true);
      })();
    },
    removeUnsupported(key) {
      s.files = s.files.filter((item) => fileKeyOf(item) !== key);
      if (s.report) s.report = { ...s.report, files: s.report.files.filter((verdict) => verdict.key !== key) };
      delete s.selected[key];
      delete s.kinds[key];
    },
    setKind(key, kind) {
      if (kind === '') delete s.kinds[key];
      else s.kinds[key] = kind;
    },
    // A saved retry can explain the missing computer before its file is picked again.
    openRetry(name) {
      s.retryServerName = name;
      if (!s.report && s.files.length > 0) { s.continueToCheck(); return; }
      s.step = 2; s.checking = false;
      s.cards[name] = 'open'; s.expanded[name] = true; s.focus = 'name:' + name;
    },
    startImport() {
      if (!s.canImport()) return;
      const items: BatchItem[] = [];
      for (const verdict of targets()) {
        const staged = stagedBy(verdict.key);
        if (!staged) continue;
        const card = cardIdOf(verdict);
        // A file that names its computer is matched against the list by the server. A card that named none gives its files the computer typed on it.
        const host = verdict.verdict === 'needs-computer' || card === null ? '' : (s.labels[card] ?? '');
        items.push({ key: verdict.key, file: staged.file, importer: verdict.importer, host });
      }
      if (items.length === 0) return;
      void runImport(items);
    },
    cancelImport() {
      s.stopping = stopAfter !== null;
      stopAfter?.abort();
      s.importing = false; s.importDone = false; s.step = 2; s.checking = false;
    },
    importMore() { clearSession(); },
    openTimeline() { s.timeline = true; s.closed = true; },
    // The import is not left by accident: the close button, Esc and a click outside wait until it ends or Cancel is pressed.
    requestClose() {
      if (s.importing) return;
      if (s.listedFiles() > 0 && s.step !== 3) { s.discard = true; s.focus = 'keep'; }
      else s.closed = true;
    },
    keepFiles() { s.discard = false; s.focus = 'close'; },
    discardNow() { clearSession(); s.closed = true; },
    dispose() {
      leave.abort();
      abortChecks();
    }
  });
  return s;
}
