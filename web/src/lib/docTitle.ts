/**
 * The browser tab title. index.html names the app ("Unravis"); a screen that wants to be named adds its own words in front
 * ("Investigation Timeline - Unravis") and the returned function puts the app name back when the screen leaves.
 */
const APP_TITLE = 'Unravis';

/** Sets the document title to "<label> - Unravis" and returns a function that restores the plain app title. A blank label leaves the app title. */
export function setDocTitle(label: string): () => void {
  const text = label.trim();
  if (typeof document !== 'undefined') document.title = text ? text + ' - ' + APP_TITLE : APP_TITLE;
  return () => { if (typeof document !== 'undefined') document.title = APP_TITLE; };
}
