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
