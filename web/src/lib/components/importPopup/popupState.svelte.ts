import type { CheckFileVerdict, CheckReport } from '../../cases';
import type { StagedFile } from '../../fileKinds';
import type { Query } from '../../types';

/**
 * The step machine of the "Import files" popup (wireframe: prototype/import-popup-WIREFRAME.html). It holds what the person sees and
 * decides: the step, the fix cards, the files ticked, the acknowledgement and the close confirmation. It sends nothing; the check and the
 * import are run by the step bodies, which write `report`, `checking`, `importProgress` and `importDone` here.
 *
 * Naming used by the step bodies:
 *  - A file's key is `fileKeyOf(file)` (name|size|lastModified); the check report echoes it back.
 *  - A fix card is named by `cardIdOf(verdict)`: the server name for a missing computer, `no-computer:<kind>` or `needs-zone:<kind>` otherwise.
 *  - A group (what `toggle` and `groupAll` take) is `groupOf(verdict)`: the card's name, or `kind:<importer>` for files that are ready.
 *  - `focus` asks the popup to move focus to the element carrying that `data-focus` value: `head-<group>` (a group's header button),
 *    `card-<card>` (a card's main button or its Undo), `name:<card>` (a card's name field), `ack`, `keep` and `close`.
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

export interface PopupState {
  step: PopupStep;
  checking: boolean;
  importing: boolean;
  importDone: boolean;
  importProgress: number;
  expanded: Record<string, boolean>;
  /** Keyed by file key. A file with no entry counts as ticked. */
  selected: Record<string, boolean>;
  /** Keyed by card id. A card with no entry is open. */
  cards: Record<string, CardState>;
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

  continueToCheck(): void;
  goTo(step: number): void;
  toggle(group: string): void;
  setSelected(key: string, on: boolean): void;
  groupAll(group: string, on: boolean): void;
  resolveCard(name: string): void;
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

const initialExpanded = (): Record<string, boolean> => ({ unsupported: true });

export function createPopupState(): PopupState {
  const cardOf = (id: string): CardState => s.cards[id] ?? 'open';
  const checked = (): CheckFileVerdict[] => s.report?.files ?? [];
  const leftOut = (verdict: CheckFileVerdict): boolean => {
    const id = cardIdOf(verdict);
    return id !== null && cardOf(id) === 'leftout';
  };
  const cardIds = (): string[] => {
    const ids: string[] = [];
    for (const verdict of checked()) {
      const id = cardIdOf(verdict);
      if (id !== null && !ids.includes(id)) ids.push(id);
    }
    return ids;
  };
  const fixesNeeded = (): number => cardIds().filter((id) => cardOf(id) === 'open').length;
  const isSelected = (key: string): boolean => s.selected[key] !== false;

  // Everything the person built up, back to a fresh popup. Used when files are discarded and when the person imports more.
  const clearSession = () => {
    s.step = 1; s.checking = false; s.importing = false; s.importDone = false; s.importProgress = 0;
    s.expanded = initialExpanded(); s.selected = {}; s.cards = {}; s.kinds = {};
    s.ack = false; s.discard = false; s.retryServerName = null; s.focus = null;
    s.files = []; s.report = null;
  };

  const s: PopupState = $state<PopupState>({
    step: 1, checking: false, importing: false, importDone: false, importProgress: 0,
    expanded: initialExpanded(), selected: {}, cards: {}, kinds: {},
    ack: false, discard: false, closed: false, timeline: false,
    retryServerName: null, focus: null, files: [], report: null,

    fixesNeeded,
    targetCount: () => checked().filter((verdict) => verdict.verdict !== 'not-supported' && isSelected(verdict.key) && !leftOut(verdict)).length,
    canImport: () => fixesNeeded() === 0 && s.ack,
    listedFiles: () => s.files.length,
    cardIds,
    isSelected,

    continueToCheck() {
      s.step = 2; s.checking = true; s.report = null; s.cards = {}; s.ack = false;
      for (const item of s.files) {
        const key = fileKeyOf(item);
        if (!(key in s.selected)) s.selected[key] = true;
      }
    },
    // Only back, and never while the import runs: leaving then would hide an import that is still reading files.
    goTo(step) {
      if (step < 1 || step >= s.step || s.importing) return;
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
    resolveCard(name) { s.cards[name] = 'resolved'; s.focus = 'card-' + name; },
    leaveOut(name) { s.cards[name] = 'leftout'; },
    undoCard(name) { s.cards[name] = 'open'; s.focus = 'card-' + name; },
    addAllServers() {
      for (const id of cardIds()) if (cardOf(id) === 'open') s.cards[id] = 'resolved';
      s.focus = 'ack';
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
    openRetry(name) {
      s.retryServerName = name; s.step = 2; s.checking = false;
      s.cards[name] = 'open'; s.expanded[name] = true; s.focus = 'name:' + name;
    },
    startImport() {
      if (!s.canImport()) return;
      s.step = 3; s.importing = true; s.importDone = false; s.importProgress = 0;
    },
    cancelImport() {
      s.importing = false; s.importDone = false; s.step = 2; s.checking = false;
    },
    importMore() { clearSession(); },
    openTimeline() { s.timeline = true; s.closed = true; },
    requestClose() {
      if (s.listedFiles() > 0 && s.step !== 3) { s.discard = true; s.focus = 'keep'; }
      else s.closed = true;
    },
    keepFiles() { s.discard = false; s.focus = 'close'; },
    discardNow() { clearSession(); s.closed = true; }
  });
  return s;
}
