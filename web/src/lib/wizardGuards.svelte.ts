import { resetWizard } from './wizard.svelte';

// The little state the Add sources guards share between the Sources hub (tabs), the Add sources flow and its cards. In memory only; it holds
// two flags and a case id, never what was typed.

export const addGuard = $state<{
  /** A card of the Add sources screen holds typed input that is not saved (set by the card host, read by the hub before a tab switch). */
  entryDirty: boolean;
  /** The case whose unfinished add list was kept when the person left the Add data tab ('' when none is kept). */
  keptFor: string;
}>({ entryDirty: false, keptFor: '' });

/** Drops the unfinished add list that was kept for the way back (the case page is left, or another case opens). Does nothing while a run is writing it. */
export function dropKeptAddDraft(): void {
  if (addGuard.keptFor === '') return;
  addGuard.keptFor = '';
  resetWizard();
}
