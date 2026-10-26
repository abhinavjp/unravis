// Shared ledger state, kept for as long as the tab lives. The drawer reads and writes it, and the header badge reads the count.
// Drafts (editors), the open entry and the new-hypothesis text live here so closing the drawer does not lose typing.
import { api } from './api';
import { addCitation, editorFromHypothesis, type CitedRef, type EditorState } from './ledger';
import type { Hypothesis } from './types';

export const ledger = $state({
  /** The case whose theories these are. Empty means the older list that is shared by every case. */
  caseId: '',
  items: [] as Hypothesis[],
  loaded: false,
  editors: {} as Record<string, EditorState>,
  openId: null as string | null,
  newText: '',
  /** References collected from Patterns, waiting to be attached to an entry. */
  cited: [] as CitedRef[]
});

let inflight: { caseId: string; promise: Promise<void> } | null = null;

/**
 * Points the ledger at another case's theories. The list, the drafts and the open entry belong to one case, so they are put away; the new
 * case's theories are read in the background. Pointing at the same case again does nothing.
 */
export function setLedgerCase(caseId: string): void {
  if (ledger.caseId === caseId) return;
  ledger.caseId = caseId;
  ledger.items = [];
  ledger.loaded = false;
  ledger.editors = {};
  ledger.openId = null;
  ledger.newText = '';
  inflight = null;
  if (caseId) void refreshLedger().catch(() => { /* the panel that shows the theories asks again and shows the reason */ });
}

/** Reads the saved entries. Existing drafts are kept; entries with no draft get a fresh editor. Rejects with the API error. */
export function refreshLedger(): Promise<void> {
  const caseId = ledger.caseId;
  if (inflight && inflight.caseId === caseId) return inflight.promise;
  const mine: { caseId: string; promise: Promise<void> } = { caseId, promise: Promise.resolve() };
  mine.promise = (async () => {
    try {
      const list = (await api.ledgerList(caseId)) ?? [];
      // The person moved to another case while this was in flight: its answer belongs to the old case, so it is dropped.
      if (ledger.caseId !== caseId) return;
      ledger.items = list;
      for (const h of list) if (!ledger.editors[h.id]) ledger.editors[h.id] = editorFromHypothesis(h);
      ledger.loaded = true;
    } finally { if (inflight === mine) inflight = null; }
  })();
  inflight = mine;
  return mine.promise;
}

/**
 * Cite a piece of evidence in the ledger. `evidenceId` is the reference token (for example "episode:2026-10-24T18:19:37Z")
 * and `label` is the short text people see (for example "Episode 2, 18:19"). The reference waits in the ledger drawer
 * until the analyst attaches it to an entry. Returns true when it was added, false when it was blank or already there.
 */
export function citeInLedger(evidenceId: string, label: string): boolean {
  const next = addCitation(ledger.cited, evidenceId, label);
  if (next === ledger.cited) return false;
  ledger.cited = next;
  return true;
}
