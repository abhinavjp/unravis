// Shared ledger state, kept for as long as the tab lives. The drawer reads and writes it, and the header badge reads the count.
// Drafts (editors), the open entry and the new-hypothesis text live here so closing the drawer does not lose typing.
import { api } from './api';
import { addCitation, editorFromHypothesis, type CitedRef, type EditorState } from './ledger';
import type { Hypothesis } from './types';

export const ledger = $state({
  items: [] as Hypothesis[],
  loaded: false,
  editors: {} as Record<string, EditorState>,
  openId: null as string | null,
  newText: '',
  /** References collected from Patterns, waiting to be attached to an entry. */
  cited: [] as CitedRef[]
});

let inflight: Promise<void> | null = null;

/** Reads the saved entries. Existing drafts are kept; entries with no draft get a fresh editor. Rejects with the API error. */
export function refreshLedger(): Promise<void> {
  if (inflight) return inflight;
  inflight = (async () => {
    try {
      const list = (await api.ledgerList()) ?? [];
      ledger.items = list;
      for (const h of list) if (!ledger.editors[h.id]) ledger.editors[h.id] = editorFromHypothesis(h);
      ledger.loaded = true;
    } finally { inflight = null; }
  })();
  return inflight;
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
