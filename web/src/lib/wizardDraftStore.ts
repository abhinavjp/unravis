// What is safe to keep of the create-case draft for a reload of this tab: the name, the environments and whether an own key was asked for.
// Never a key, a password, a connection or a chosen source (CASES-25). sessionStorage only, so it ends with the tab. Kept apart from the
// wizard state so that file stays free of any browser store.

export interface SavedDraft { title: string; envText: string; keyOn: boolean }

const STORE = 'unravis.wizardDraft';

export function readSavedDraft(): SavedDraft | null {
  try {
    const raw = typeof sessionStorage === 'undefined' ? null : sessionStorage.getItem(STORE);
    if (!raw) return null;
    const saved = JSON.parse(raw) as { title?: unknown; envText?: unknown; keyOn?: unknown };
    return {
      title: typeof saved.title === 'string' ? saved.title.slice(0, 500) : '',
      envText: typeof saved.envText === 'string' ? saved.envText.slice(0, 2000) : '',
      keyOn: saved.keyOn === true
    };
  } catch { return null; }
}

export function saveDraft(draft: SavedDraft): void {
  try { if (typeof sessionStorage !== 'undefined') sessionStorage.setItem(STORE, JSON.stringify(draft)); } catch { /* storage refused: the draft lives in memory only */ }
}

export function clearSavedDraft(): void {
  try { if (typeof sessionStorage !== 'undefined') sessionStorage.removeItem(STORE); } catch { /* nothing stored */ }
}

/** True when a kept draft holds something worth coming back to: a name or environments (an own-key switch alone is not). */
export function draftHasContent(draft: SavedDraft | null): boolean {
  return draft != null && (draft.title.trim() !== '' || draft.envText.trim() !== '');
}
