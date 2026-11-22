import type { UpdateCheckReply } from './types';

export type UpdatePreview = '' | (typeof PREVIEWS)[number];
const UNREACHABLE_NOTE = 'Could not check for updates. Check your connection and try again later.';
const PREVIEWS = ['latest', 'available', 'offline', 'unverified', 'downloading', 'ready', 'failed', 'launched'] as const;
const OWN_RELEASE_PREFIX = 'https://github.com/abhinavjp/unravis/';

/** The one sentence the Help panel shows for a check result. */
export function updateCheckNote(reply: UpdateCheckReply): string {
  switch (reply.status) {
    case 'latest':
      if (reply.held) return `You are on v${reply.current}. A newer version exists, but it is on hold: ${reply.held}.`;
      return `You are on the latest version (v${reply.current}).`;
    case 'available':
      return `Version ${reply.latest} is available. You have v${reply.current}.`;
    case 'dev':
      return 'This is a development build, so there is nothing to compare.';
    case 'unverified':
      return 'An update answer came back, but it did not pass the safety check, so it was ignored.';
    case 'none':
      return 'No check has run yet.';
    default:
      return UNREACHABLE_NOTE;
  }
}

/** The demo-only preview named in the URL hash, such as #update=available. Any other value gives ''. */
export function updatePreviewFrom(hash: string): UpdatePreview {
  const match = /(?:^#|[#&])update=([a-z]+)/.exec(hash);
  if (!match) return '';
  const name = match[1];
  return (PREVIEWS as readonly string[]).includes(name) ? (name as UpdatePreview) : '';
}

/** True only for a release page in this project's own GitHub repository. */
export function isOwnReleaseUrl(url: string): boolean {
  return url.startsWith(OWN_RELEASE_PREFIX);
}

/** The data-state value for a reply: always one of latest, available, unreachable or dev. */
export function updateCheckState(reply: { status?: unknown } | null | undefined): 'latest' | 'available' | 'unreachable' | 'dev' | 'unverified' | 'none' {
  const status = reply && typeof reply.status === 'string' ? reply.status : '';
  return status === 'latest' || status === 'available' || status === 'dev' || status === 'unverified' || status === 'none' ? status : 'unreachable';
}

const UPDATE_PREVIEW_KEY = 'unravis.updatePreview';
let updatePreview: UpdatePreview = '';

/** The preview after the address changes: an address that names update= decides (a bad value gives ''), an address that does not name it keeps what was stored. */
export function updatePreviewAfter(hash: string, stored: UpdatePreview): UpdatePreview {
  return /(?:^#|[#&])update=/.test(hash) ? updatePreviewFrom(hash) : stored;
}

/** Notes a demo preview named in the address and keeps it for this tab, so moving around the app (which changes the address) keeps it. A bad value clears it. */
function noticeUpdatePreview(): void {
  const next = updatePreviewAfter(location.hash, updatePreview);
  if (next === updatePreview) return;
  updatePreview = next;
  try {
    if (next) sessionStorage.setItem(UPDATE_PREVIEW_KEY, next);
    else sessionStorage.removeItem(UPDATE_PREVIEW_KEY);
  } catch { /* the preview then lasts until the page reloads */ }
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

/** Every sentence the update check can show, for the copy lint. The Help panel labels are linted from the component source. */
export function updateCheckSentences(): string[] {
  return [
    updateCheckNote({ status: 'latest', current: '1.2.0', latest: '1.2.0', url: '' }),
    updateCheckNote({ status: 'available', current: '1.2.0', latest: '1.3.0', url: 'https://github.com/abhinavjp/unravis/releases/tag/v1.3.0' }),
    updateCheckNote({ status: 'latest', current: '1.2.0', latest: '1.2.0', url: '', held: 'updates are paused' }),
    updateCheckNote({ status: 'dev', current: 'dev', latest: '', url: '' }),
    updateCheckNote({ status: 'unreachable', current: '1.2.0', latest: '', url: '' }),
    updateCheckNote({ status: 'unverified', current: '1.2.0', latest: '', url: '' })
  ];
}

/** The "how often" choices. A saved value that is not here is shown as its own entry. */
export const UPDATE_INTERVALS: { minutes: number; label: string }[] = [
  { minutes: 30, label: 'Every 30 minutes' },
  { minutes: 60, label: 'Every hour' },
  { minutes: 360, label: 'Every 6 hours' },
  { minutes: 1440, label: 'Every day' }
];

/** The label for any saved interval, such as "Every 45 minutes". */
export function intervalLabel(minutes: number): string {
  const known = UPDATE_INTERVALS.find(item => item.minutes === minutes);
  if (known) return known.label;
  if (minutes % 1440 === 0) return `Every ${minutes / 1440} days`;
  if (minutes % 60 === 0) return `Every ${minutes / 60} hours`;
  return `Every ${minutes} minutes`;
}

/** "Last checked 14:05" or '' when no check has run. The time is the person's own clock. */
export function checkedNote(checkedAt: string | undefined, now: Date = new Date()): string {
  if (!checkedAt) return '';
  const when = new Date(checkedAt);
  if (Number.isNaN(when.getTime())) return '';
  const minutes = Math.max(0, Math.round((now.getTime() - when.getTime()) / 60000));
  if (minutes < 1) return 'Last checked just now.';
  if (minutes < 60) return `Last checked ${minutes} ${minutes === 1 ? 'minute' : 'minutes'} ago.`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `Last checked ${hours} ${hours === 1 ? 'hour' : 'hours'} ago.`;
  return `Last checked ${Math.round(hours / 24)} days ago.`;
}

/** Whole percent of a download, 0 to 100. An unknown total gives 0. */
export function downloadPercent(done: number, total: number): number {
  if (!(total > 0) || !(done > 0)) return 0;
  return Math.min(100, Math.floor((done / total) * 100));
}

/** Why an update failed, in plain words. The one hash message from the server is rewritten; anything else is shown as it came. */
export function failedReason(error: string | undefined): string {
  const text = (error ?? '').trim();
  if (/does not match the signed release/i.test(text)) return 'The downloaded file did not match the signed release, so it was deleted. Nothing was changed.';
  return text;
}

/** The sentence for the download and install step. '' when there is nothing to say. */
export function installNote(reply: UpdateCheckReply): string {
  const step = reply.install;
  if (!step) return '';
  switch (step.state) {
    case 'downloading':
      return `Downloading version ${step.version}... ${downloadPercent(step.done, step.total)}%`;
    case 'ready':
      return reply.applyAuto
        ? `Version ${step.version} is ready. It installs by itself when you are not using Unravis, or press Apply now. Unravis restarts on its own.`
        : `Version ${step.version} is ready. Apply now closes Unravis, updates it and starts it again.`;
    case 'launched':
      return 'The update is being applied. Unravis closes and starts again by itself. If nothing happens, press Apply now again.';
    case 'failed':
      return `Update failed. ${failedReason(step.error)}`.trim();
    default:
      return '';
  }
}

/** The short line a screen reader hears when the state changes (no percentage, so it is not read out every second). */
export function installAnnouncement(reply: UpdateCheckReply): string {
  const step = reply.install;
  if (!step) return '';
  switch (step.state) {
    case 'downloading': return `Downloading version ${step.version}.`;
    case 'ready': return `Version ${step.version} is ready to install.`;
    case 'launched': return 'The updater is open.';
    case 'failed': return 'The update did not go through.';
    default: return '';
  }
}
