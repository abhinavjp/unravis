// Release notes for the Updates page and the "What's new" window.
// The notes are one bundled file (web/public/releases/notes.json) that ships inside the app, so it is covered by the same signature as the program itself.
// They are written when a version is released (tools/release/whatsnew). The app only reads them: it never fetches notes from the internet.

export interface NoteHighlight {
  /** A short tag such as "Charts". Optional. */
  label?: string;
  text: string;
  /** A screenshot, as a path inside the app (/whatsnew/...). With `before` too it is shown as before and after. */
  image?: string;
  before?: string;
  alt?: string;
}
export interface ReleaseNotes {
  version: string;
  date: string;
  headline: string;
  highlights: NoteHighlight[];
  /** The full developer list, one line per change. */
  changes: string[];
}

export type NoteKind = 'before-after' | 'screenshot' | 'note';
/** How a highlight is shown follows from what it carries: two pictures, one picture, or only words. */
export function highlightKind(item: NoteHighlight): NoteKind {
  if (item.image && item.before) return 'before-after';
  if (item.image) return 'screenshot';
  return 'note';
}

export const NOTES_URL = '/releases/notes.json';
export const SEEN_KEY = 'unravis.whatsNewSeen';
/** The tour shows at most this many versions; the full list on the Updates page has the rest. */
export const TOUR_MAX_VERSIONS = 8;

/** "v0.1.0-rc.62" and "0.1.0-rc.62" are the same version. */
export function normalizeVersion(value: string): string {
  return String(value ?? '').trim().replace(/^v/i, '');
}

/** The short form shown in small places: "rc.62" for a release candidate, the whole number otherwise. */
export function shortVersion(value: string): string {
  const version = normalizeVersion(value);
  const rc = /-(rc\.\d+)$/.exec(version);
  return rc ? rc[1] : version;
}

function parts(value: string): number[] | null {
  const match = /^(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$/.exec(normalizeVersion(value));
  if (!match) return null;
  // A final release (no -rc) comes after every release candidate of the same number.
  return [Number(match[1]), Number(match[2]), Number(match[3]), match[4] === undefined ? Number.MAX_SAFE_INTEGER : Number(match[4])];
}

/** Negative when a is older than b, positive when newer, 0 when equal or when either is not a version we know. */
export function compareVersions(a: string, b: string): number {
  const left = parts(a);
  const right = parts(b);
  if (!left || !right) return 0;
  for (let i = 0; i < 4; i++) if (left[i] !== right[i]) return left[i] < right[i] ? -1 : 1;
  return 0;
}

function text(value: unknown, max = 400): string {
  return typeof value === 'string' ? value.trim().slice(0, max) : '';
}
// Pictures come only from the app's own /whatsnew/ folder, so a notes file can never point the window at another site.
function imagePath(value: unknown): string {
  const path = text(value, 200);
  if (!path.startsWith('/whatsnew/') || /(^|\/)\.\.(\/|$)/.test(path) || path.includes('//') || path.includes('\\') || /[?#%]/.test(path)) return '';
  return path;
}

/** Reads a notes file defensively: anything wrong is dropped, never thrown. Newest version first, one entry per version. */
export function parseNotes(raw: unknown): ReleaseNotes[] {
  const file = raw && typeof raw === 'object' && !Array.isArray(raw) ? (raw as { schema?: unknown; releases?: unknown }) : null;
  if (!file || file.schema !== 1 || !Array.isArray(file.releases)) return [];
  const seen = new Set<string>();
  const out: ReleaseNotes[] = [];
  for (const entry of file.releases) {
    if (!entry || typeof entry !== 'object') continue;
    const item = entry as Record<string, unknown>;
    const version = normalizeVersion(text(item.version, 60));
    if (!parts(version) || seen.has(version)) continue;
    seen.add(version);
    const highlights: NoteHighlight[] = [];
    for (const shot of Array.isArray(item.highlights) ? item.highlights.slice(0, 40) : []) {
      if (!shot || typeof shot !== 'object') continue;
      const h = shot as Record<string, unknown>;
      const body = text(h.text, 600);
      if (!body) continue;
      const image = imagePath(h.image);
      const before = image ? imagePath(h.before) : '';
      const label = text(h.label, 40);
      const alt = text(h.alt, 300);
      highlights.push({ text: body, ...(label ? { label } : {}), ...(image ? { image } : {}), ...(before ? { before } : {}), ...(alt ? { alt } : {}) });
    }
    const changes = (Array.isArray(item.changes) ? item.changes : []).filter((line): line is string => typeof line === 'string').map(line => line.trim().slice(0, 300)).filter(Boolean).slice(0, 1000);
    out.push({ version, date: text(item.date, 20), headline: text(item.headline, 200), highlights, changes });
  }
  return out.sort((a, b) => compareVersions(b.version, a.version));
}

let cached: Promise<ReleaseNotes[]> | null = null;
/** Reads the bundled notes once. A missing or broken file gives an empty list: the page then says the notes are not available. */
export function loadNotes(fetcher: typeof fetch = fetch): Promise<ReleaseNotes[]> {
  if (!cached) {
    cached = fetcher(NOTES_URL, { cache: 'no-store' })
      .then(response => (response.ok ? response.json() : null))
      .then(parseNotes)
      .catch(() => [] as ReleaseNotes[]);
    // An empty answer is not kept, so the next look tries again.
    const mine = cached;
    void mine.then(list => { if (!list.length && cached === mine) cached = null; });
  }
  return cached;
}

/** The versions the tour covers: everything after the one last read, up to the installed one. With no memory it is only the installed version. Newest first. */
export function versionsSince(releases: ReleaseNotes[], lastSeen: string, current: string, max = TOUR_MAX_VERSIONS): ReleaseNotes[] {
  const now = normalizeVersion(current);
  const last = normalizeVersion(lastSeen);
  const upTo = releases.filter(release => compareVersions(release.version, now) <= 0);
  if (!last || !parts(last) || compareVersions(last, now) >= 0) return upTo.filter(release => release.version === now);
  return upTo.filter(release => compareVersions(release.version, last) > 0).slice(0, max);
}

export interface Slide {
  version: string;
  headline: string;
  kind: NoteKind;
  label?: string;
  text: string;
  image?: string;
  before?: string;
  alt?: string;
  /** For the "Also in" slide: the plain notes of the version, one per line. */
  lines?: string[];
}

/** One slide for every picture, then one text slide per version for the notes that have none. Versions keep their order (newest first). */
export function buildSlides(releases: ReleaseNotes[]): Slide[] {
  const slides: Slide[] = [];
  for (const release of releases) {
    const plain: string[] = [];
    for (const item of release.highlights) {
      const kind = highlightKind(item);
      if (kind === 'note') { plain.push(item.text); continue; }
      slides.push({ version: release.version, headline: release.headline, kind, label: item.label, text: item.text, image: item.image, before: item.before, alt: item.alt });
    }
    if (plain.length || !release.highlights.length) {
      slides.push({ version: release.version, headline: release.headline, kind: 'note', text: plain.length ? '' : 'Mostly work behind the scenes in this version.', lines: plain });
    }
  }
  return slides;
}

export function readSeen(): string {
  try { return normalizeVersion(localStorage.getItem(SEEN_KEY) ?? ''); } catch { return ''; }
}
export function writeSeen(version: string): void {
  try { localStorage.setItem(SEEN_KEY, normalizeVersion(version)); } catch { /* it is then asked again next time */ }
}

/** Whether the tour should open by itself: the app was updated since the notes were last read. A first run is not an update. */
export function shouldAutoOpen(lastSeen: string, current: string): boolean {
  const last = normalizeVersion(lastSeen);
  return !!last && !!parts(last) && !!parts(current) && compareVersions(last, current) < 0;
}

/** True for a version number this app knows how to order, such as 0.1.0-rc.62. A development build is not one. */
export function parseableVersion(value: string): boolean { return parts(value) !== null; }
