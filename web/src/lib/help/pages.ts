/// <reference types="vite/client" />
import { parseMarkdown, type Block } from './markdown';
import { PAGE_ORDER, titleFromSource } from './links';

const raw = import.meta.glob('../../../../docs/user/*.md', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;

export type HelpPage = { slug: string; title: string; blocks: Block[] };

const sources = new Map<string, string>();
for (const [path, text] of Object.entries(raw)) {
  const slug = (path.split('/').pop() ?? '').replace(/\.md$/, '');
  sources.set(slug, text);
}

export const helpPages: HelpPage[] = PAGE_ORDER.filter((slug) => sources.has(slug)).map((slug) => {
  const source = sources.get(slug) as string;
  return { slug, title: titleFromSource(source, slug), blocks: parseMarkdown(source) };
});

export const helpSlugs: string[] = helpPages.map((p) => p.slug);

export function helpPage(slug: string): HelpPage | undefined {
  return helpPages.find((p) => p.slug === slug);
}
