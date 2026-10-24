import { citeLabel, isEvidenceId } from './patterns';
import { citeInLedger as addToLedger, ledger } from './ledgerStore.svelte';

/**
 * "Cite in ledger" for the Patterns drawer. The reference goes into the ledger store; if the store did not take it (blank or already
 * there) or throws, it goes to the clipboard instead.
 *   added: the ledger drawer holds it until it is attached to an entry. already: it was there before. copied: the reference is on the clipboard. invalid: not a valid reference. unavailable: neither worked.
 */
export type CiteResult = 'added' | 'already' | 'copied' | 'invalid' | 'unavailable';

async function copyReference(evidenceId: string): Promise<boolean> {
  try {
    if (typeof navigator === 'undefined' || !navigator.clipboard) return false;
    await navigator.clipboard.writeText(evidenceId);
    return true;
  } catch { return false; }
}

export async function citeInLedger(evidenceId: string, label: string): Promise<CiteResult> {
  if (!isEvidenceId(evidenceId)) return 'invalid';
  try {
    if (ledger.cited.some((cited) => cited.id === evidenceId.trim())) return 'already';
    if (addToLedger(evidenceId, citeLabel(label))) return 'added';
  } catch { /* fall back to the clipboard */ }
  return (await copyReference(evidenceId)) ? 'copied' : 'unavailable';
}
