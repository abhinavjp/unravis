import type { UpdateCheckReply } from './types';

const UNREACHABLE_NOTE = 'Could not check for updates. Check your connection and try again later.';
const PREVIEWS = ['latest', 'available', 'offline'] as const;
const OWN_RELEASE_PREFIX = 'https://github.com/abhinavjp/unravis/';

/** The one sentence the Help panel shows for a check result. */
export function updateCheckNote(reply: UpdateCheckReply): string {
  switch (reply.status) {
    case 'latest':
      return `You are on the latest version (v${reply.current}).`;
    case 'available':
      return `Version ${reply.latest} is available. You have v${reply.current}.`;
    case 'dev':
      return 'This is a development build, so there is nothing to compare.';
    default:
      return UNREACHABLE_NOTE;
  }
}

/** The demo-only preview named in the URL hash, such as #update=available. Any other value gives ''. */
export function updatePreviewFrom(hash: string): '' | 'latest' | 'available' | 'offline' {
  const match = /(?:^#|[#&])update=([a-z]+)/.exec(hash);
  if (!match) return '';
  const name = match[1];
  return (PREVIEWS as readonly string[]).includes(name) ? (name as 'latest' | 'available' | 'offline') : '';
}

/** True only for a release page in this project's own GitHub repository. */
export function isOwnReleaseUrl(url: string): boolean {
  return url.startsWith(OWN_RELEASE_PREFIX);
}

/** The data-state value for a reply: always one of latest, available, unreachable or dev. */
export function updateCheckState(reply: { status?: unknown } | null | undefined): 'latest' | 'available' | 'unreachable' | 'dev' {
  const status = reply && typeof reply.status === 'string' ? reply.status : '';
  return status === 'latest' || status === 'available' || status === 'dev' ? status : 'unreachable';
}

type UpdatePreview = '' | 'latest' | 'available' | 'offline';
const UPDATE_PREVIEW_KEY = 'unravis.updatePreview';
let updatePreview: UpdatePreview = '';

/** Notes a demo preview named in the address and keeps it for this tab, so moving around the app (which changes the address) keeps it. */
function noticeUpdatePreview(): void {
  const found = updatePreviewFrom(location.hash);
  if (!found) return;
  updatePreview = found;
  try { sessionStorage.setItem(UPDATE_PREVIEW_KEY, found); } catch { /* the preview then lasts until the page reloads */ }
}

if (typeof location !== 'undefined' && typeof window !== 'undefined') {
  try { updatePreview = updatePreviewFrom('#update=' + (sessionStorage.getItem(UPDATE_PREVIEW_KEY) ?? '')); } catch { updatePreview = ''; }
  noticeUpdatePreview();
  // popstate comes first on a hash change, before the app's own view handling may rewrite the address.
  window.addEventListener('popstate', noticeUpdatePreview);
  window.addEventListener('hashchange', noticeUpdatePreview);
}

/** The demo preview for this tab ('' for none). */
export const updatePreviewNow = (): UpdatePreview => updatePreview;
