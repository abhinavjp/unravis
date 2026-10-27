import type { VersionInfo } from './types';

/** The quiet line at the foot of the help panel: "Unravis v1.2.3 (build abc1234, 2026-10-27)". Parts the build did not stamp are left out. */
export function versionLine(info: VersionInfo | null | undefined): string {
  const version = (info?.version ?? '').trim();
  if (!version) return '';
  const name = /^\d/.test(version) ? 'v' + version : version;
  const parts: string[] = [];
  const commit = (info?.commit ?? '').trim().slice(0, 7);
  if (commit) parts.push('build ' + commit);
  const date = (info?.date ?? '').trim();
  if (date) parts.push(date);
  return 'Unravis ' + name + (parts.length ? ' (' + parts.join(', ') + ')' : '');
}
