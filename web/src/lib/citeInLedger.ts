/// <reference types="vite/client" />
import { citeLabel, isEvidenceId } from './patterns';

/**
 * "Cite in ledger" for the Patterns drawer. The ledger module owns the real add-evidence function; this wrapper finds it by name when the
 * page loads it, so the build never depends on it. If it is missing or throws, the reference goes to the clipboard instead.
 *   added: the ledger took it. copied: the reference is on the clipboard. invalid: not a valid reference. unavailable: neither worked.
 */
export type CiteResult = 'added' | 'copied' | 'invalid' | 'unavailable';

type AddEvidence = (evidenceId: string, label: string) => unknown;
const ADD_NAMES = ['addEvidence', 'addEvidenceReference', 'addEvidenceRef', 'citeEvidence', 'citeInLedger', 'addEvidenceToLedger'];

// A glob that matches no file is an empty object, so a missing ledger module is not a build error.
const ledgerModules = import.meta.glob('./ledger*.ts');

async function findAdd(): Promise<AddEvidence | null> {
  for (const load of Object.values(ledgerModules)) {
    try {
      const loaded = (await load()) as Record<string, unknown>;
      for (const name of ADD_NAMES) {
        const candidate = loaded[name];
        if (typeof candidate === 'function') return candidate as AddEvidence;
      }
    } catch { /* a module that fails to load is skipped */ }
  }
  return null;
}

async function copyReference(evidenceId: string): Promise<boolean> {
  try {
    if (typeof navigator === 'undefined' || !navigator.clipboard) return false;
    await navigator.clipboard.writeText(evidenceId);
    return true;
  } catch { return false; }
}

export async function citeInLedger(evidenceId: string, label: string): Promise<CiteResult> {
  if (!isEvidenceId(evidenceId)) return 'invalid';
  const add = await findAdd();
  if (add) {
    try {
      const outcome = await add(evidenceId, citeLabel(label));
      if (outcome !== false) return 'added';
    } catch { /* fall back to the clipboard */ }
  }
  return (await copyReference(evidenceId)) ? 'copied' : 'unavailable';
}
