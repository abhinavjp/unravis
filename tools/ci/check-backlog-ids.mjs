// Every backlog id is defined once. Run: node tools/ci/check-backlog-ids.mjs [path-to-BACKLOG.md]   (from the repo root)
// Definitions are the checklist lines "- [ ] **BL-123** ..." / "- [x] **BL-123a** ..." (also **SWP-...** ids) and headings that start
// with an id ("### BL-123 ..."). An id may be defined by only one line. "(was BL-123)" and ids in running text are references, not
// definitions, so a renumbered item keeps its old id in brackets without a clash. Fenced code blocks are skipped.
import fs from 'node:fs';
import { pathToFileURL } from 'node:url';

const CHECKLIST = /^\s*- \[[ xX]\] \*{0,2}((?:BL|SWP)-[0-9A-Za-z-]+?)\*{0,2}(?=[\s:.)]|$)/;
const HEADING = /^#{1,6}\s+\*{0,2}(BL-[0-9]+[a-z]?)\b/;

/** Returns [{id, lines:[n,...]}] for every id defined more than once. Pure. */
export function findDuplicateIds(text) {
  const seen = new Map();
  let fenced = false;
  text.split(/\r?\n/).forEach((line, i) => {
    if (/^\s*```/.test(line)) { fenced = !fenced; return; }
    if (fenced) return;
    const m = CHECKLIST.exec(line) || HEADING.exec(line);
    if (!m) return;
    if (!seen.has(m[1])) seen.set(m[1], []);
    seen.get(m[1]).push(i + 1);
  });
  return [...seen].filter(([, l]) => l.length > 1).map(([id, lines]) => ({ id, lines }));
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const file = process.argv[2] || 'docs/BACKLOG.md';
  const dups = findDuplicateIds(fs.readFileSync(file, 'utf8'));
  if (dups.length) {
    for (const d of dups) console.error(`${file}: ${d.id} is defined on lines ${d.lines.join(', ')}; give the later one a new number and write "(was ${d.id})"`);
    process.exit(1);
  }
  console.log(`backlog ids ok: each id in ${file} is defined once`);
}
