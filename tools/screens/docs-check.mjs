// Checks the draft docs against the screenshots. Run: node docs-check.mjs (from tools/screens).
//   a) every image link in buildout/docs/*.md points to a file that exists;
//   b) every id in screens.json appears in the doc page it names;
//   c) no page links to a screenshot of a screen that is no longer in screens.json.
// Exit 0 when all hold, 1 with one plain line per problem otherwise.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const docsDir = resolve(here, '..', '..', 'buildout', 'docs');
const shotsDir = resolve(here, '..', '..', 'buildout', 'screenshots');
const problems = [];

let screens = [];
try {
  screens = JSON.parse(readFileSync(join(here, 'screens.json'), 'utf8')).screens;
} catch (err) {
  console.error('screens.json could not be read: ' + err.message);
  process.exit(1);
}
if (!existsSync(docsDir)) {
  console.error('The docs folder is missing: ' + docsDir);
  process.exit(1);
}

const pages = readdirSync(docsDir).filter((name) => name.endsWith('.md'));
const text = new Map(pages.map((name) => [name, readFileSync(join(docsDir, name), 'utf8')]));
const knownIds = new Set(screens.map((s) => s.id));

// a) and c): every image link
const image = /!\[[^\]]*\]\(([^)\s]+)\)/g;
for (const [name, body] of text) {
  for (const match of body.matchAll(image)) {
    const target = match[1];
    if (/^[a-z]+:/i.test(target)) continue; // an address on the web is not checked here
    const file = resolve(docsDir, decodeURI(target.split('#')[0]));
    const inShots = file.startsWith(shotsDir);
    if (!existsSync(file)) problems.push(`${name}: the image ${target} does not exist.`);
    if (inShots) {
      const stem = basename(file, '.png').replace(/-(dark|light)(-narrow)?$/, '');
      if (!knownIds.has(stem)) problems.push(`${name}: the image ${target} is of a screen (${stem}) that screens.json no longer captures.`);
    }
  }
}

// b) every screen id sits in its page
for (const s of screens) {
  const body = text.get(s.doc);
  if (body === undefined) problems.push(`screens.json: ${s.id} names the page ${s.doc}, which does not exist.`);
  else if (!body.includes(s.id)) problems.push(`${s.doc}: it does not mention the screen ${s.id}.`);
}

if (problems.length) {
  for (const p of problems) console.error(p);
  console.error(`${problems.length} docs problem${problems.length === 1 ? '' : 's'}.`);
  process.exit(1);
}
console.log(`Docs are in step: ${pages.length} pages, ${screens.length} screens.`);
