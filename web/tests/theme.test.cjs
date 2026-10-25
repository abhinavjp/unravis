const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const css = read('src/app.css');

// ---- token blocks ----
function block(opener) {
  const start = css.indexOf(opener);
  assert.ok(start >= 0, opener + ' exists');
  const open = css.indexOf('{', start);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}' && --depth === 0) return { start, end: i + 1, text: css.slice(open + 1, i) };
  }
  throw new Error('unterminated block ' + opener);
}
const darkBlock = block(':root {');
const lightMediaBlock = block('@media (prefers-color-scheme: light) {');
const lightForcedBlock = block(':root[data-theme="light"] {');
const lightMediaInner = block(':root:not([data-theme="dark"]) {');
const tokens = (text) => Object.fromEntries([...text.matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)].map(([, name, value]) => [name, value.toLowerCase()]));
const dark = tokens(darkBlock.text);
const lightMedia = tokens(lightMediaInner.text);
const lightForced = tokens(lightForcedBlock.text);

// ---- colour maths ----
const channel = (c) => { c /= 255; return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
const rgb = (hex) => { const n = parseInt(hex.slice(1), 16); return [n >> 16, (n >> 8) & 255, n & 255]; };
const luminance = (hex) => { const [r, g, b] = rgb(hex); return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b); };
const ratio = (a, b) => { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
const lab = (hex) => {
  const [r, g, b] = rgb(hex).map(channel);
  const x = (0.4124 * r + 0.3576 * g + 0.1805 * b) / 0.95047, y = 0.2126 * r + 0.7152 * g + 0.0722 * b, z = (0.0193 * r + 0.1192 * g + 0.9505 * b) / 1.08883;
  const f = (t) => t > 0.008856 ? Math.cbrt(t) : 7.787 * t + 16 / 116;
  return [116 * f(y) - 16, 500 * (f(x) - f(y)), 200 * (f(y) - f(z))];
};
const distance = (a, b) => { const p = lab(a), q = lab(b); return Math.hypot(p[0] - q[0], p[1] - q[1], p[2] - q[2]); };

const themes = { dark, light: lightForced };
const grounds = ['--bg', '--surface', '--surface-2'];

test('the three theme blocks exist and define every colour token the contrast checks need', () => {
  for (const [name, set] of Object.entries({ dark, 'light (media)': lightMedia, 'light (forced)': lightForced })) {
    for (const token of [...grounds, '--text', '--text-2', '--control-line', '--accent', '--accent-ink', '--focus', '--warn', '--bad', '--pending', '--line', '--line-soft', '--chart-empty']) {
      assert.match(set[token] ?? '', /^#[0-9a-f]{6}$/, name + ' defines ' + token);
    }
  }
});

test('both light blocks are identical to each other', () => {
  assert.deepEqual(lightMedia, lightForced);
  assert.equal(lightMediaInner.text.replace(/\s+/g, ' ').replace(/color-scheme: light;/, '').trim(), lightForcedBlock.text.replace(/\s+/g, ' ').replace(/color-scheme: light;/, '').trim(), 'every declaration matches, not only the hex ones');
});

test('exactly three control-line colours remain, one per theme block', () => {
  assert.equal((css.match(/--control-line:\s*#[0-9a-f]{6}/gi) ?? []).length, 3);
  assert.equal(dark['--control-line'], '#6b8478');
});

test('the dark palette is the Terminal palette from the design addendum', () => {
  const expected = { '--bg': '#050807', '--surface': '#0a0f0d', '--surface-2': '#101714', '--line': '#1a2621', '--line-soft': '#131c18', '--control-line': '#6b8478', '--text': '#eef3f1', '--text-2': '#93a39b', '--accent': '#3fd6b0', '--accent-ink': '#03130e', '--focus': '#7ff0d0', '--warn': '#e3b341', '--bad': '#ff7b72', '--pending': '#8fa39a', '--chart-empty': '#0a0f0d' };
  for (const [name, value] of Object.entries(expected)) assert.equal(dark[name], value, name);
  assert.doesNotMatch(css, /--text-3\s*:/, '--text-3 is not a token');
});

test('the light theme keeps warm-paper neutrals and takes the teal accent', () => {
  assert.equal(lightForced['--accent'], '#0b6b57');
  assert.equal(lightForced['--focus'], '#0b6b57');
  assert.equal(lightForced['--surface'], '#ffffff');
  assert.equal(lightForced['--bg'], '#f5f5f3');
});

for (const [theme, set] of Object.entries(themes)) {
  test(theme + ': text and secondary text clear 4.5:1 on the page, surface and raised surface', () => {
    for (const ground of grounds) {
      for (const text of ['--text', '--text-2']) {
        const value = ratio(set[text], set[ground]);
        assert.ok(value >= 4.5, text + ' on ' + ground + ' is ' + value.toFixed(2));
      }
    }
  });
  test(theme + ': control lines, the accent and the focus ring clear 3:1 on every ground', () => {
    for (const ground of grounds) {
      for (const token of ['--control-line', '--accent', '--focus']) {
        const value = ratio(set[token], set[ground]);
        assert.ok(value >= 3, token + ' on ' + ground + ' is ' + value.toFixed(2));
      }
    }
  });
  test(theme + ': ink on the accent fill clears 4.5:1', () => {
    const value = ratio(set['--accent-ink'], set['--accent']);
    assert.ok(value >= 4.5, 'accent-ink on accent is ' + value.toFixed(2));
  });
  test(theme + ': the tone colours used as text clear 4.5:1 on the page and surface', () => {
    for (const ground of ['--bg', '--surface']) {
      for (const token of ['--warn', '--bad', '--pending']) {
        const value = ratio(set[token], set[ground]);
        assert.ok(value >= 4.5, token + ' on ' + ground + ' is ' + value.toFixed(2));
      }
    }
  });
  test(theme + ': series 1 stays distinguishable from the accent, and so do the rest of the series', () => {
    const series = [1, 2, 3, 4, 5, 6, 7, 8].map((n) => set['--series-' + n]);
    assert.equal(series.filter(Boolean).length, 8);
    for (const colour of series) assert.ok(distance(colour, set['--accent']) >= 25, colour + ' is too close to the accent ' + set['--accent']);
  });
}

test('the radius and font tokens carry the Terminal shape and the Plex stacks', () => {
  assert.match(css, /--radius:\s*2px;/);
  assert.match(css, /--radius-sm:\s*2px;/);
  assert.match(css, /--font:\s*"IBM Plex Sans",/);
  assert.match(css, /--mono:\s*"IBM Plex Mono",/);
  assert.match(css, /--font:[^;]*system-ui[^;]*sans-serif;/, 'the sans stack ends in system fallbacks');
  assert.match(css, /--mono:[^;]*monospace;/, 'the mono stack ends in a generic monospace');
  assert.match(css, /font-size:\s*14px;\s*\n\s*line-height/, 'the root size is unchanged');
  assert.match(css, /body \{[^}]*font-size:\s*13px/, 'the dense feel comes from the body size');
});

test('no literal corner radius above 2px survives in the stylesheet (circles stay round)', () => {
  const literal = [...css.matchAll(/border-radius:\s*(\d+)px/g)].map((match) => Number(match[1]));
  assert.ok(literal.every((px) => px <= 2), 'literal radii: ' + literal.join(','));
});

test('colours are written as hex values only inside the three theme blocks', () => {
  const outside = css.slice(0, darkBlock.start) + css.slice(darkBlock.end, lightMediaBlock.start) + css.slice(lightForcedBlock.end);
  const withoutComments = outside.replace(/\/\*[\s\S]*?\*\//g, '');
  assert.doesNotMatch(withoutComments, /#[0-9a-fA-F]{3,8}\b(?![\w-])/, 'a hex colour sits outside the token blocks');
  assert.doesNotMatch(withoutComments, /\brgba?\(/, 'an rgb() colour sits outside the token blocks');
});

test('panel and group titles are mono with a decorative // prefix that is not read aloud', () => {
  assert.match(css, /\.section-heading h3::before[^{]*\{[^}]*content:\s*"\/\/ "\s*\/\s*""/);
  assert.match(css, /\.section-heading h3, \.plot-heading h3[^{]*\{[^}]*font-family:\s*var\(--mono\)[^}]*font-size:\s*12px[^}]*font-weight:\s*500/);
});

test('no glow, gradient, banner or glass is introduced by the theme', () => {
  const withoutComments = css.replace(/\/\*[\s\S]*?\*\//g, '');
  assert.doesNotMatch(withoutComments, /backdrop-filter|text-shadow|radial-gradient/);
  assert.doesNotMatch(withoutComments, /(^|[;{\s])background(-image)?:[^;}]*(linear|radial)-gradient/, 'no gradient fills');
});

// ---- fonts ----
const faces = [...css.matchAll(/@font-face\s*\{([^}]*)\}/g)].map((match) => match[1]);
const urls = faces.flatMap((face) => [...face.matchAll(/url\(\s*["']?([^"')]+)["']?\s*\)/g)].map((match) => match[1]));

test('five @font-face rules cover Plex Sans 400, 500, 600 and Plex Mono 400, 500, all swap-loaded and Latin-ranged', () => {
  assert.equal(faces.length, 5);
  const keys = faces.map((face) => /font-family:\s*"([^"]+)"/.exec(face)[1] + ' ' + /font-weight:\s*(\d+)/.exec(face)[1]).sort();
  assert.deepEqual(keys, ['IBM Plex Mono 400', 'IBM Plex Mono 500', 'IBM Plex Sans 400', 'IBM Plex Sans 500', 'IBM Plex Sans 600']);
  for (const face of faces) {
    assert.match(face, /font-display:\s*swap/);
    assert.match(face, /unicode-range:\s*U\+0000-00FF/);
    assert.match(face, /format\("woff2"\)/);
  }
});

test('every @font-face url is a relative path to a file that exists', () => {
  assert.equal(urls.length, 5);
  for (const url of urls) {
    assert.doesNotMatch(url, /^[a-z]+:|^\/\//i, url + ' is relative');
    assert.match(url, /\.woff2$/);
    assert.ok(fs.existsSync(path.join(root, 'src', url)), url + ' exists under src/');
  }
});

test('the stylesheet has no @import and no http reference', () => {
  assert.doesNotMatch(css, /@import/);
  assert.doesNotMatch(css, /https?:/i);
});

test('the bundled fonts total at most 220 KB, only woff2 plus the licence, and the licence text is present', () => {
  const dir = path.join(root, 'src', 'fonts');
  const files = fs.readdirSync(dir);
  const woff2 = files.filter((name) => name.endsWith('.woff2'));
  assert.equal(woff2.length, 5);
  assert.deepEqual(files.filter((name) => !name.endsWith('.woff2')), ['OFL.txt']);
  const total = woff2.reduce((sum, name) => sum + fs.statSync(path.join(dir, name)).size, 0);
  assert.ok(total <= 220 * 1024, 'woff2 total is ' + total + ' bytes');
  const licence = fs.readFileSync(path.join(dir, 'OFL.txt'), 'utf8');
  assert.match(licence, /SIL OPEN FONT LICENSE Version 1\.1/);
  assert.match(licence, /IBM Plex Sans/);
  assert.match(licence, /IBM Plex Mono/);
});

test('the font packages are not dependencies; the files are vendored', () => {
  const pkg = JSON.parse(read('package.json'));
  const names = Object.keys({ ...pkg.dependencies, ...pkg.devDependencies });
  assert.deepEqual(names.filter((name) => name.includes('fontsource') || name.includes('plex')), []);
});
