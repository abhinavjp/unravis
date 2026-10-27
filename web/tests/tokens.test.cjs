const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const src = path.join(root, 'src');
const norm = (text) => text.replace(/\r\n/g, '\n');
const tokensCss = norm(fs.readFileSync(path.join(src, 'brand', 'tokens.css'), 'utf8'));

function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    return entry.isDirectory() ? walk(full) : [full];
  });
}
const rel = (file) => 'web/' + path.relative(root, file).split(path.sep).join('/');
const brandDir = path.join(src, 'brand') + path.sep;
const files = walk(src).filter((file) => /\.(css|svelte|ts)$/.test(file) && !file.startsWith(brandDir));

// A colour literal is a hex colour (not an HTML entity such as &#9670; and not inside a word) or an rgb(, rgba(, hsl(, hsla( call.
const colourLiteral = /(?<![&\w#])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![0-9a-zA-Z_-])|\b(?:rgba?|hsla?)\(/g;

// Each entry says why a literal is allowed. Keep this list short.
const allowed = {
  'web/src/components/Inspector.svelte': { literals: ['#000'], why: 'the colour of a mask-image gradient only matters as opaque versus transparent; it is never painted' }
};

test('no colour literal outside web/src/brand, except the allowlist', () => {
  const offenders = [];
  for (const file of files) {
    const text = norm(fs.readFileSync(file, 'utf8'));
    const permitted = allowed[rel(file)]?.literals ?? [];
    text.split('\n').forEach((line, index) => {
      for (const match of line.matchAll(colourLiteral)) {
        if (!permitted.includes(match[0])) offenders.push(rel(file) + ':' + (index + 1) + ' ' + match[0]);
      }
    });
  }
  assert.deepEqual(offenders, [], 'colours belong in web/src/brand/tokens.css. Found:\n' + offenders.join('\n'));
});

test('the allowlist is still needed and carries a reason', () => {
  for (const [file, entry] of Object.entries(allowed)) {
    assert.ok(entry.why.length > 10, file + ' has a reason');
    const text = norm(fs.readFileSync(path.join(root, '..', file), 'utf8'));
    for (const literal of entry.literals) assert.ok(text.includes(literal), file + ' still uses ' + literal + '; remove it from the allowlist');
  }
});

test('the scanner catches the shapes it is meant to catch', () => {
  const hits = (text) => [...text.matchAll(colourLiteral)].map((match) => match[0]);
  assert.deepEqual(hits('color: #123456;'), ['#123456']);
  assert.deepEqual(hits('color: #fff; background: #FFFA;'), ['#fff', '#FFFA']);
  assert.deepEqual(hits('color: #12345678'), ['#12345678']);
  assert.deepEqual(hits('a: rgb(1 2 3); b: rgba(1,2,3,.5); c: hsl(0 0% 0%)'), ['rgb(', 'rgba(', 'hsl(']);
  assert.deepEqual(hits('&#9670; {#each rows as row} #app { } a#bad'), []);
});

// A var() that names a fallback (var(--x, inherit)) is a deliberate optional hook and may be undefined; one without a fallback must exist.
test('every var(--name) without a fallback used in web/src is defined in tokens.css or in the same file', () => {
  const globalNames = new Set([...tokensCss.matchAll(/(--[a-zA-Z0-9-]+)\s*:/g)].map((match) => match[1]));
  const missing = [];
  for (const file of files) {
    const text = norm(fs.readFileSync(file, 'utf8'));
    const localNames = new Set([...text.matchAll(/(--[a-zA-Z0-9-]+)\s*:/g)].map((match) => match[1]));
    for (const match of text.matchAll(/var\(\s*(--[a-zA-Z0-9-]+)\s*\)/g)) {
      const name = match[1];
      if (!globalNames.has(name) && !localNames.has(name)) missing.push(rel(file) + ' ' + name);
    }
  }
  assert.deepEqual([...new Set(missing)], [], 'define these in web/src/brand/tokens.css or in the file that uses them');
});

test('the light fallback palette matches the light theme in tokens.css', () => {
  const start = tokensCss.indexOf(':root[data-theme="light"] {');
  assert.ok(start >= 0, 'light block exists');
  const block = tokensCss.slice(tokensCss.indexOf('{', start) + 1, tokensCss.indexOf('\n}', start));
  const light = Object.fromEntries([...block.matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)].map((match) => [match[1], match[2].toLowerCase()]));
  const fallbackSource = norm(fs.readFileSync(path.join(src, 'brand', 'fallback.ts'), 'utf8'));
  const pick = (key) => fallbackSource.match(new RegExp(String.raw`\b${key}:\s*'(#[0-9a-fA-F]{6,8})'`))?.[1].toLowerCase();
  const pairs = { text: '--text', accent: '--accent', warn: '--warn', bad: '--bad', pending: '--pending', text2: '--text-2', axisLine: '--line', grid: '--line-soft', empty: '--chart-empty', surface: '--surface' };
  for (const [key, token] of Object.entries(pairs)) assert.equal(pick(key), light[token], key + ' follows ' + token);
  assert.equal(pick('accentSoft').slice(0, 7), light['--accent'], 'accentSoft is the accent with an alpha');
  const list = (key) => [...fallbackSource.match(new RegExp(String.raw`\b${key}:\s*\[([^\]]*)\]`))[1].matchAll(/'(#[0-9a-fA-F]{6})'/g)].map((match) => match[1].toLowerCase());
  assert.deepEqual(list('heat'), [1, 2, 3, 4].map((n) => light['--heat-' + n]));
  assert.deepEqual(list('series'), [1, 2, 3, 4, 5, 6, 7, 8].map((n) => light['--series-' + n]));
});

test('app.css imports the tokens first and holds no font-face or theme block', () => {
  const app = norm(fs.readFileSync(path.join(src, 'app.css'), 'utf8'));
  assert.ok(app.startsWith('@import "./brand/tokens.css";'));
  assert.doesNotMatch(app, /@font-face/);
  assert.doesNotMatch(app, /\[data-theme=/);
});
