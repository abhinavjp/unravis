// Checks the docs against the screenshots. Run: node tools/screens/docs-check.mjs (from the project folder).
//   a) every image link in docs/user/*.md points to a file that exists;
//   b) every screen in screens.json has a picture of itself (an image link whose file name starts with its id)
//      in the doc page it names, except the screens in PENDING below, whose pictures are not made yet;
//   c) no page links to a screenshot of a screen that is no longer in screens.json;
//   d) screens.json itself is sound: no screen named twice, every id starts with its phase, every screen has steps;
//   e) the alt text of every screenshot says "dark" or "light" (the in-app guide shows the alt, not the picture);
//   f) no page names a screen id in its text (screenshots.md, the contributor page, may);
//   g) no page uses a word that claims a cause or a proof (see BANNED and PROVE below);
//   h) the Blackbox states table in blackbox.md uses the app's own words (web/src/lib/copy/blackboxCopy.ts).
// An optional first argument names the project folder (default: two folders up from this file); the tests use it.
// Exit 0 when all hold, 1 with one plain line per problem otherwise.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = process.argv[2] ? resolve(process.argv[2]) : resolve(here, '..', '..');
const docsDir = join(root, 'docs', 'user');
const shotsDir = join(root, 'docs', 'user', 'screenshots');
const problems = [];

// Screens that are listed in screens.json but have no picture yet (the screenshot harness has not run on a
// build with them). They need no picture in their page. See D-C-29. Remove an id from here when its pictures
// are added to docs/user/screenshots and linked from its page.
const PENDING = new Set(['p12-update-none', 'p12-update-available', 'p12-update-offline', 'p13-guide-index', 'p13-guide-page']);

// Words that claim a cause or a proof. Code (fenced or in backticks) is not read.
const BANNED = /\b(caused|waiting on|blocked by|due to|the database was waiting|the API waited for)\b/gi;
const PROVE = /\b(prove|proves|proved|proven|proof)\b/gi;
const NEGATED = /\b(not|never|nor|cannot)\b|n['’]t/i;

let screens = [];
try {
  screens = JSON.parse(readFileSync(join(root, 'tools', 'screens', 'screens.json'), 'utf8')).screens;
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

// d) the screens file
const seen = new Set();
for (const s of screens) {
  if (typeof s.id !== 'string' || !/^p\d+[a-z]?-[a-z0-9-]+$/.test(s.id)) problems.push(`screens.json: the id ${JSON.stringify(s.id)} must look like p1-home (or p14b-home for a part phase).`);
  else if (s.phase !== undefined && !s.id.startsWith(String(s.phase).toLowerCase() + '-')) problems.push(`screens.json: ${s.id} must start with its phase (${s.phase}).`);
  if (seen.has(s.id)) problems.push(`screens.json: the screen ${s.id} is listed twice.`);
  seen.add(s.id);
  if (!Array.isArray(s.steps) || s.steps.length === 0) problems.push(`screens.json: ${s.id} has no steps.`);
  if (typeof s.doc !== 'string') problems.push(`screens.json: ${s.id} names no doc page.`);
}

// a), c) and e): every image link
const image = /!\[([^\]]*)\]\(([^)\s]+)\)/g;
const pictured = new Map(pages.map((name) => [name, new Set()])); // page -> ids it has a picture of
for (const [name, body] of text) {
  for (const match of body.matchAll(image)) {
    const alt = match[1];
    const target = match[2];
    if (/^[a-z]+:/i.test(target)) continue; // an address on the web is not checked here
    const file = resolve(docsDir, decodeURI(target.split('#')[0]));
    const inShots = file.startsWith(shotsDir);
    if (!existsSync(file)) problems.push(`${name}: the image ${target} does not exist.`);
    if (inShots) {
      const stem = basename(file, '.png').replace(/-(dark|light)(-narrow)?$/, '');
      if (!knownIds.has(stem)) problems.push(`${name}: the image ${target} is of a screen (${stem}) that screens.json no longer captures.`);
      pictured.get(name).add(stem);
      if (!/\b(dark|light)\b/i.test(alt)) problems.push(`${name}: the picture ${target} needs "dark" or "light" in its alt text.`);
    }
  }
}

// b) every screen has a picture in its page
for (const s of screens) {
  const body = text.get(s.doc);
  if (body === undefined) problems.push(`screens.json: ${s.id} names the page ${s.doc}, which does not exist.`);
  else if (!PENDING.has(s.id) && !pictured.get(s.doc).has(s.id)) problems.push(`${s.doc}: it has no picture of the screen ${s.id}.`);
}

// f) no page names a screen id in its text
const wellFormed = [...knownIds].filter((id) => typeof id === 'string' && /^p\d+[a-z]?-[a-z0-9-]+$/.test(id)).sort((a, b) => b.length - a.length);
if (wellFormed.length) {
  const named = new RegExp('(?<![\\w-])(' + wellFormed.join('|') + ')(?![\\w-])', 'g');
  for (const [name, body] of text) {
    if (name === 'screenshots.md') continue;
    const mentioned = new Set();
    for (const match of body.replace(image, '').matchAll(named)) mentioned.add(match[1]);
    for (const id of mentioned) problems.push(`${name}: it names the screen id ${id} in its text.`);
  }
}

// g) words that claim a cause or a proof
for (const [name, body] of text) {
  const prose = body.replace(/```[\s\S]*?```/g, ' ').replace(/`[^`\n]*`/g, ' ');
  const said = new Set();
  for (const match of prose.matchAll(BANNED)) said.add(match[1].toLowerCase());
  for (const match of prose.matchAll(PROVE)) {
    const before = prose.slice(Math.max(0, match.index - 20), match.index);
    if (!NEGATED.test(before)) said.add(match[1].toLowerCase());
  }
  for (const word of said) problems.push(`${name}: the text says "${word}".`);
}

// h) the Blackbox states table says what the app says
const copyFile = join(root, 'web', 'src', 'lib', 'copy', 'blackboxCopy.ts');
const blackbox = text.get('blackbox.md');
if (existsSync(copyFile) && blackbox !== undefined) {
  const block = readFileSync(copyFile, 'utf8').match(/stateNext\s*:\s*\{([\s\S]*?)\}\s*as Record/);
  if (!block) problems.push('web/src/lib/copy/blackboxCopy.ts: the stateNext block was not found.');
  else {
    for (const entry of block[1].matchAll(/(?:'([\w-]+)'|([A-Za-z_]\w*))\s*:\s*'((?:[^'\\]|\\.)*)'/g)) {
      const sentence = entry[3].replace(/\\'/g, "'");
      if (!blackbox.includes(sentence)) problems.push(`blackbox.md: the states table does not say "${sentence}" (state ${entry[1] ?? entry[2]}).`);
    }
  }
  if (!/\|\s*Stopped\s*\|\s*Partly ready\s*\|/.test(blackbox)) problems.push('blackbox.md: the Stopped row of the states table must say Partly ready.');
}

if (problems.length) {
  for (const p of problems) console.error(p);
  console.error(`${problems.length} docs problem${problems.length === 1 ? '' : 's'}.`);
  process.exit(1);
}
console.log(`Docs are in step: ${pages.length} pages, ${screens.length} screens.`);
