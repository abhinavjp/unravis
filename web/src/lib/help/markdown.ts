// Small Markdown parser for the in-app guide. Pure functions, no DOM, no HTML output:
// the app renders these nodes with Svelte. Raw HTML is not supported, so any `<` is plain text.

export type Inline =
  | { t: 'text'; text: string } | { t: 'code'; text: string }
  | { t: 'strong'; children: Inline[] } | { t: 'em'; children: Inline[] }
  | { t: 'link'; href: string; children: Inline[] } | { t: 'image'; alt: string; src: string };
export type Block =
  | { t: 'heading'; level: 1 | 2 | 3 | 4; id: string; children: Inline[] }
  | { t: 'p'; children: Inline[] }
  | { t: 'list'; ordered: boolean; items: { children: Inline[]; sub: Block[] }[] }
  | { t: 'code'; lang: string; text: string }
  | { t: 'quote'; blocks: Block[] }
  | { t: 'table'; head: Inline[][]; rows: Inline[][][] };

export function slugify(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N} -]/gu, '')
    .replace(/ /g, '-');
}

// ---------- inline ----------

const isSpace = (ch: string | undefined): boolean => ch === undefined || /\s/.test(ch);
const isWord = (ch: string | undefined): boolean => ch !== undefined && /[\p{L}\p{N}]/u.test(ch);

function pushText(out: Inline[], text: string): void {
  if (!text) return;
  const last = out[out.length - 1];
  if (last && last.t === 'text') last.text += text;
  else out.push({ t: 'text', text });
}

function tickRun(src: string, start: number): number {
  let n = 0;
  while (src[start + n] === '`') n++;
  return n;
}

// End index (exclusive) of a code span that opens at `start`, or -1 when it never closes.
function codeSpanEnd(src: string, start: number): number {
  const n = tickRun(src, start);
  let j = start + n;
  while (j < src.length) {
    if (src[j] !== '`') { j++; continue; }
    const m = tickRun(src, j);
    if (m === n) return j + m;
    j += m;
  }
  return -1;
}

// Index of the `]` that closes the `[` at `start`, or -1.
function closeBracket(src: string, start: number): number {
  let depth = 0;
  for (let j = start; j < src.length; j++) {
    const ch = src[j];
    if (ch === '`') {
      const end = codeSpanEnd(src, j);
      if (end > 0) j = end - 1;
    } else if (ch === '[') depth++;
    else if (ch === ']') { depth--; if (depth === 0) return j; }
  }
  return -1;
}

// `(dest)` right after a `]`: returns the destination and the index after `)`.
function parseDest(src: string, open: number): { dest: string; end: number } | null {
  if (src[open] !== '(') return null;
  let depth = 0;
  for (let j = open; j < src.length; j++) {
    const ch = src[j];
    if (ch === '(') depth++;
    else if (ch === ')') {
      depth--;
      if (depth === 0) {
        const raw = src.slice(open + 1, j).trim();
        return { dest: raw.split(/\s+/)[0] ?? '', end: j + 1 };
      }
    }
  }
  return null;
}

// Closing marker for strong (`**`) or em (`*`, `_`). Skips code spans. Returns -1 when none.
function findCloser(src: string, from: number, marker: string): number {
  for (let j = from; j < src.length; j++) {
    const ch = src[j];
    if (ch === '`') {
      const end = codeSpanEnd(src, j);
      if (end > 0) j = end - 1;
      continue;
    }
    if (marker === '**') {
      if (src.startsWith('**', j) && j > from && !isSpace(src[j - 1])) return j;
      continue;
    }
    if (marker === '*' && ch === '*' && src[j + 1] === '*') { j++; continue; }
    if (ch !== marker) continue;
    if (j === from || isSpace(src[j - 1])) continue;
    if (isWord(src[j + 1])) continue;
    return j;
  }
  return -1;
}

function parseInline(src: string): Inline[] {
  const out: Inline[] = [];
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (ch === '`') {
      const n = tickRun(src, i);
      const end = codeSpanEnd(src, i);
      if (end < 0) {
        pushText(out, src.slice(i, i + n));
        i += n;
      } else {
        out.push({ t: 'code', text: src.slice(i + n, end - n) });
        i = end;
      }
      continue;
    }
    if (ch === '!' && src[i + 1] === '[') {
      const close = closeBracket(src, i + 1);
      const dest = close > 0 ? parseDest(src, close + 1) : null;
      if (close > 0 && dest) {
        out.push({ t: 'image', alt: src.slice(i + 2, close), src: dest.dest });
        i = dest.end;
      } else {
        pushText(out, '!');
        i++;
      }
      continue;
    }
    if (ch === '[') {
      const close = closeBracket(src, i);
      const dest = close > 0 ? parseDest(src, close + 1) : null;
      if (close > 0 && dest) {
        out.push({ t: 'link', href: dest.dest, children: parseInline(src.slice(i + 1, close)) });
        i = dest.end;
      } else {
        pushText(out, '[');
        i++;
      }
      continue;
    }
    if (ch === '*' && src[i + 1] === '*') {
      const close = isSpace(src[i + 2]) ? -1 : findCloser(src, i + 2, '**');
      if (close > 0) {
        out.push({ t: 'strong', children: parseInline(src.slice(i + 2, close)) });
        i = close + 2;
      } else {
        pushText(out, '**');
        i += 2;
      }
      continue;
    }
    if ((ch === '*' || ch === '_') && !isWord(src[i - 1]) && !isSpace(src[i + 1]) && src[i + 1] !== ch) {
      const close = findCloser(src, i + 1, ch);
      if (close > 0) {
        out.push({ t: 'em', children: parseInline(src.slice(i + 1, close)) });
        i = close + 1;
        continue;
      }
    }
    pushText(out, ch);
    i++;
  }
  return out;
}

function plainText(nodes: Inline[]): string {
  return nodes.map((n) => {
    switch (n.t) {
      case 'text': case 'code': return n.text;
      case 'image': return n.alt;
      default: return plainText(n.children);
    }
  }).join('');
}

// ---------- blocks ----------

const FENCE = /^\s{0,3}(`{3,})\s*([^`\s]*)[^`]*$/;
const HEADING = /^\s{0,3}(#{1,4})\s+(.*?)\s*$/;
const QUOTE = /^\s{0,3}>/;
const MARKER = /^(\s*)([-*]|\d+\.)(?:\s+(.*))?$/;
const SEPARATOR_CELL = /^:?-+:?$/;

const indentOf = (line: string): number => line.length - line.trimStart().length;
const isOrdered = (marker: string): boolean => /\d/.test(marker);

function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith('|')) s = s.slice(1);
  if (s.endsWith('|') && !s.endsWith('\\|')) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = '';
  for (let j = 0; j < s.length; j++) {
    if (s[j] === '\\' && s[j + 1] === '|') { cur += '|'; j++; }
    else if (s[j] === '|') { cells.push(cur); cur = ''; }
    else cur += s[j];
  }
  cells.push(cur);
  return cells.map((c) => c.trim());
}

function isTableStart(lines: string[], i: number): boolean {
  const head = lines[i];
  const sep = lines[i + 1];
  if (head === undefined || sep === undefined || !head.includes('|') || !sep.includes('-')) return false;
  const sepCells = splitRow(sep);
  if (!sepCells.every((c) => SEPARATOR_CELL.test(c))) return false;
  return sepCells.length === splitRow(head).length;
}

const isMarker = (line: string): boolean => MARKER.test(line);

function startsBlock(lines: string[], i: number): boolean {
  const line = lines[i];
  return FENCE.test(line) || HEADING.test(line) || QUOTE.test(line) || isMarker(line) || isTableStart(lines, i);
}

function parseList(lines: string[], start: number, ids: Map<string, number>): { block: Block; next: number } {
  const first = MARKER.exec(lines[start])!;
  const base = first[1].length;
  const ordered = isOrdered(first[2]);
  const items: { children: Inline[]; sub: Block[] }[] = [];
  let i = start;
  while (i < lines.length) {
    const m = MARKER.exec(lines[i]);
    if (!m) break;
    const ind = indentOf(lines[i]);
    if (ind < base || ind >= base + 2 || isOrdered(m[2]) !== ordered) break;
    const contentOffset = ind + m[2].length + 1;
    const rest: string[] = [];
    let j = i + 1;
    while (j < lines.length) {
      const line = lines[j];
      if (line.trim() === '') {
        let k = j;
        while (k < lines.length && lines[k].trim() === '') k++;
        if (k < lines.length && indentOf(lines[k]) >= base + 2) {
          for (; j < k; j++) rest.push('');
          continue;
        }
        break;
      }
      if (indentOf(line) >= base + 2) { rest.push(line); j++; continue; }
      if (isMarker(line)) break;
      const prevBlank = rest.length > 0 && rest[rest.length - 1].trim() === '';
      if (prevBlank || startsBlock(lines, j)) break;
      rest.push(line);
      j++;
    }
    const dedented = rest.map((l) => l.slice(Math.min(indentOf(l), contentOffset)));
    let split = 0;
    while (split < dedented.length && dedented[split].trim() !== '' && !startsBlock(dedented, split)) split++;
    const text = [(m[3] ?? '').trim(), ...dedented.slice(0, split).map((l) => l.trim())].filter((s) => s !== '').join(' ');
    items.push({ children: parseInline(text), sub: parseBlocks(dedented.slice(split), ids) });
    // A blank line between two items of the same list does not end the list.
    let k = j;
    while (k < lines.length && lines[k].trim() === '') k++;
    const nm = k < lines.length ? MARKER.exec(lines[k]) : null;
    const nind = nm ? indentOf(lines[k]) : -1;
    const more = nm !== null && nind >= base && nind < base + 2 && isOrdered(nm[2]) === ordered;
    i = more ? k : j;
    if (!more) break;
  }
  return { block: { t: 'list', ordered, items }, next: i };
}

function parseBlocks(lines: string[], ids: Map<string, number>): Block[] {
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === '') { i++; continue; }

    const fence = FENCE.exec(line);
    if (fence) {
      const closer = new RegExp('^\\s{0,3}`{' + fence[1].length + ',}\\s*$');
      const body: string[] = [];
      let j = i + 1;
      while (j < lines.length && !closer.test(lines[j])) { body.push(lines[j]); j++; }
      blocks.push({ t: 'code', lang: fence[2], text: body.join('\n') });
      i = j < lines.length ? j + 1 : j;
      continue;
    }

    const heading = HEADING.exec(line);
    if (heading) {
      const children = parseInline(heading[2]);
      const base = slugify(plainText(children));
      let id = base;
      if (ids.has(id)) {
        let n = 1;
        while (ids.has(base + '-' + n)) n++;
        id = base + '-' + n;
      }
      ids.set(id, 0);
      blocks.push({ t: 'heading', level: heading[1].length as 1 | 2 | 3 | 4, id, children });
      i++;
      continue;
    }

    if (QUOTE.test(line)) {
      const inner: string[] = [];
      let j = i;
      while (j < lines.length && QUOTE.test(lines[j])) { inner.push(lines[j].replace(/^\s{0,3}> ?/, '')); j++; }
      blocks.push({ t: 'quote', blocks: parseBlocks(inner, ids) });
      i = j;
      continue;
    }

    if (isTableStart(lines, i)) {
      const head = splitRow(lines[i]);
      const rows: Inline[][][] = [];
      let j = i + 2;
      while (j < lines.length && lines[j].trim() !== '' && lines[j].includes('|')) {
        const cells = splitRow(lines[j]);
        while (cells.length < head.length) cells.push('');
        rows.push(cells.slice(0, head.length).map(parseInline));
        j++;
      }
      blocks.push({ t: 'table', head: head.map(parseInline), rows });
      i = j;
      continue;
    }

    if (isMarker(line)) {
      const { block, next } = parseList(lines, i, ids);
      blocks.push(block);
      i = next;
      continue;
    }

    const para: string[] = [line.trim()];
    let j = i + 1;
    while (j < lines.length && lines[j].trim() !== '' && !startsBlock(lines, j)) { para.push(lines[j].trim()); j++; }
    blocks.push({ t: 'p', children: parseInline(para.join(' ')) });
    i = j;
  }
  return blocks;
}

export function parseMarkdown(source: string): Block[] {
  try {
    return parseBlocks(String(source ?? '').replace(/\r\n?/g, '\n').split('\n'), new Map());
  } catch {
    return [];
  }
}
