/**
 * The create request id (decisions A14). One id is made per wizard run and sent with "make the case". The server makes one case per id, so a
 * double press, a lost reply, a reload or a restart in the middle of a create all end in the same single case.
 *
 * The id is kept in sessionStorage with the title it was made for: after a reload the same title picks up the same id (and so the same case),
 * while a different title is a different case and gets a fresh id. It is forgotten when the wizard is reset. Nothing secret is in it.
 */

const KEY = 'unravis.createRequest';
const ID_SHAPE = /^[A-Za-z0-9_-]{8,64}$/;

function storage(): Storage | null {
  try { return typeof sessionStorage === 'undefined' ? null : sessionStorage; } catch { return null; }
}

/** A new request id: 32 hex characters from the browser's random source (the server accepts 8 to 64 letters, digits, dash and underscore). */
export function newRequestId(): string {
  const bytes = new Uint8Array(16);
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') crypto.getRandomValues(bytes);
  else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

const normal = (title: string): string => title.trim();

/** The id for this title's create: the one kept for it, or a new one that is kept now. */
export function requestIdFor(title: string): string {
  const store = storage();
  try {
    const raw = store?.getItem(KEY);
    if (raw) {
      const kept = JSON.parse(raw) as { title?: unknown; id?: unknown };
      if (typeof kept.id === 'string' && ID_SHAPE.test(kept.id) && kept.title === normal(title)) return kept.id;
    }
  } catch { /* an unreadable entry is replaced below */ }
  const id = newRequestId();
  try { store?.setItem(KEY, JSON.stringify({ title: normal(title), id })); } catch { /* the id then lives for this run only */ }
  return id;
}

/** Forgets the kept id: the wizard run is over (reset, or a new run started). */
export function forgetRequestId(): void {
  try { storage()?.removeItem(KEY); } catch { /* nothing to forget */ }
}
