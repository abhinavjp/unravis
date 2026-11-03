// The scope bar appears only after the first load, which pushes the page down. The last measured height is kept for this window width,
// so the next load can hold the same space while it loads. Storage may be blocked: then there is simply no hint.
const KEY = 'unravis.subbar';
const bucket = (width: number) => Math.round(width / 50);

export function readSubbarHint(width: number): number {
  try {
    const saved = JSON.parse(localStorage.getItem(KEY) ?? 'null');
    return saved && saved.b === bucket(width) && Number.isFinite(saved.h) && saved.h > 0 && saved.h < 600 ? Math.round(saved.h) : 0;
  } catch { return 0; }
}

export function writeSubbarHint(width: number, height: number): void {
  if (!(height > 0)) return;
  try { localStorage.setItem(KEY, JSON.stringify({ b: bucket(width), h: Math.round(height) })); } catch { /* the hint is optional */ }
}
