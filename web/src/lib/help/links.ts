// Link rules and page list for the in-app guide. Pure functions, no DOM, no Vite.

export type HelpTarget =
  | { kind: 'page'; slug: string; anchor: string }
  | { kind: 'anchor'; anchor: string }
  | { kind: 'external'; href: string }
  | { kind: 'none' };

export const PAGE_ORDER: readonly string[] = [
  'index', 'getting-started', 'install', 'cases', 'sources', 'blackbox',
  'guided-analysis', 'manual-tools', 'upgrade-and-uninstall', 'troubleshooting', 'security', 'logs',
];

const NONE: HelpTarget = { kind: 'none' };

export function resolveHelpLink(href: string, knownSlugs: readonly string[]): HelpTarget {
  if (typeof href !== 'string' || href === '') return NONE;
  // Backslashes, control characters and spaces are never part of a link we follow.
  if (/[\\\u0000- \u007f]/.test(href)) return NONE;
  if (href.startsWith('//')) return NONE;

  if (href.startsWith('#')) {
    return href.length > 1 ? { kind: 'anchor', anchor: href.slice(1) } : NONE;
  }
  if (/^https:\/\/[^/?#]/i.test(href)) return { kind: 'external', href };

  // Anything else with a scheme (http:, javascript:, data:, mailto:, ...) is refused.
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) return NONE;

  const hash = href.indexOf('#');
  const path = hash < 0 ? href : href.slice(0, hash);
  const anchor = hash < 0 ? '' : href.slice(hash + 1);
  const m = /^(?:\.\/)?([^/?]+)\.md$/.exec(path);
  if (!m) return NONE;
  const slug = m[1];
  return knownSlugs.includes(slug) ? { kind: 'page', slug, anchor } : NONE;
}

export function titleFromSource(source: string, slug: string): string {
  for (const line of String(source ?? '').replace(/\r\n?/g, '\n').split('\n')) {
    const m = /^#[ \t]+(.*?)\s*$/.exec(line);
    if (m && m[1] !== '') return m[1];
  }
  return slug.charAt(0).toUpperCase() + slug.slice(1);
}
