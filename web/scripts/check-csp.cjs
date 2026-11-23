'use strict';
// BL-323: the strict-CSP gate for the web UI.
//
//   node scripts/check-csp.cjs [--root <web dir>] [--list]
//
// The desktop window runs the UI under  default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; ...  (desktop/src-tauri/tauri.conf.json):
// no inline script, no inline style, no eval, no data: pictures. This script reads web/src and web/index.html and fails when the source could break
// that policy. The policy itself is proved in a real browser by tools/uiaudit/csp (Playwright, fails on any `securitypolicyviolation`); this gate keeps
// the cheap, certain causes out of the tree on every pull request (web unit tests run it: tests/cspGate.test.cjs).
//
// What `style-src` blocks and what it does not (CSP3): a `style="..."` attribute that the HTML parser reads from markup is blocked; a style written through
// the CSSOM (`el.style.x = v`, `el.style.cssText = v`, `el.style.setProperty`) is not. Svelte 5 compiles `style:left={v}` and every `style="...{v}..."` /
// `style={v}` into `set_style`, which writes `el.style.cssText`: allowed. A `style="color:red"` with no expression and no `style:` directive stays in the
// cloned HTML template: blocked. So the rules are:
//
//   html-tag       `{@html ...}` in a template (it parses a string as markup).
//   static-style   a static `style="..."` attribute in a template (no `{...}` in it and no `style:` directive on the same element), or any `style=` in an .html file.
//   html-sink      innerHTML, outerHTML, insertAdjacentHTML, document.write, srcdoc= in code or templates (string to markup).
//   eval           eval(...), new Function(...), setTimeout/setInterval with a string.
//   style-sink     setAttribute('style', ...), createElement('style'), insertAdjacentHTML of a <style>.
//   script-sink    createElement('script'), an inline <script> in an .html file.
//   data-uri       a data: or blob: address as a picture, stylesheet or font source in code or markup (img-src and font-src are 'self').
//   chart-tooltip  an ECharts `tooltip: {...}` that is not `renderMode: 'richText'` (the HTML tooltip writes `style="..."` markup). `show: false` is fine.
//   chart-html     HTML in chart text: `<br/>`, `<b>`, `extraCssText`, `.marker` in the chart files.
//   svg-renderer   the ECharts SVG renderer (the canvas renderer is the only one registered).
//
// Dynamic `style:prop={v}` / `style={v}` are allowed (CSSOM) and only counted (`--list` prints them).
// A reviewed exception is a line in scripts/csp-allowlist.json: {"file", "rule", "match", "why"}. `match` is a substring of the offending source line.
// An entry that matches nothing is itself an error, so the list cannot rot.
//
// Exit 0 clean, 1 violations (or a stale allowlist entry), 2 could not run. Read only.

const fs = require('node:fs');
const path = require('node:path');

const SOURCE_EXT = new Set(['.svelte', '.ts', '.js', '.cjs', '.mjs']);

function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (SOURCE_EXT.has(path.extname(entry.name))) out.push(full);
  }
  return out;
}

/** Line number (1-based) of an offset. */
function lineAt(text, offset) {
  let n = 1;
  for (let i = 0; i < offset && i < text.length; i++) if (text.charCodeAt(i) === 10) n++;
  return n;
}

/** Replaces comments by spaces of the same length (newlines kept), so offsets and line numbers stay right. Strings are kept: the rules read them. */
function blankComments(code, svelte) {
  let out = '';
  let i = 0;
  let quote = '';
  while (i < code.length) {
    const c = code[i];
    const n = code[i + 1];
    if (quote) {
      out += c;
      if (c === '\\') { out += code[i + 1] ?? ''; i += 2; continue; }
      if (c === quote) quote = '';
      i++;
      continue;
    }
    if (c === '/' && n === '/' && code[i - 1] !== ':') {
      while (i < code.length && code[i] !== '\n') { out += ' '; i++; }
      continue;
    }
    if (c === '/' && n === '*') {
      while (i < code.length && !(code[i] === '*' && code[i + 1] === '/')) { out += code[i] === '\n' ? '\n' : ' '; i++; }
      out += '  ';
      i += 2;
      continue;
    }
    if (svelte && c === '<' && code.startsWith('<!--', i)) {
      while (i < code.length && !code.startsWith('-->', i)) { out += code[i] === '\n' ? '\n' : ' '; i++; }
      out += '   ';
      i += 3;
      continue;
    }
    if (c === '"' || c === "'" || c === '`') quote = c;
    out += c;
    i++;
  }
  return out;
}

/** Splits a .svelte file into the template (script and style blocks blanked) and the code (the script blocks). Offsets are kept. */
function splitSvelte(text) {
  let template = text;
  let code = ' '.repeat(0);
  const blank = (s) => s.replace(/[^\n]/g, ' ');
  let codeOnly = text.replace(/[^\n]/g, ' ');
  template = text.replace(/<(script|style)\b[^>]*>[\s\S]*?<\/\1>/gi, (m, tag, offset) => {
    if (tag.toLowerCase() === 'script') {
      const open = m.indexOf('>') + 1;
      const close = m.lastIndexOf('</');
      codeOnly = codeOnly.slice(0, offset + open) + text.slice(offset + open, offset + close) + codeOnly.slice(offset + close);
    }
    return blank(m);
  });
  return { template, code: codeOnly };
}

/** Reads the start tags of a template: { name, attrs: [{name, value|null, kind, offset}], offset }. Knows quotes and `{...}` so a `>` inside an expression does not end a tag. */
function* startTags(template) {
  let i = 0;
  while (i < template.length) {
    const at = template.indexOf('<', i);
    if (at < 0) return;
    if (!/[A-Za-z]/.test(template[at + 1] ?? '')) { i = at + 1; continue; }
    let j = at + 1;
    let name = '';
    while (j < template.length && /[A-Za-z0-9:._-]/.test(template[j])) name += template[j++];
    const attrs = [];
    let ended = false;
    while (j < template.length && !ended) {
      while (j < template.length && /\s/.test(template[j])) j++;
      const c = template[j];
      if (c === '>') { ended = true; j++; break; }
      if (c === '/' && template[j + 1] === '>') { ended = true; j += 2; break; }
      if (c === '{') {
        const start = j;
        j = skipBraces(template, j);
        attrs.push({ name: template.slice(start, j), value: null, kind: 'spread', offset: start });
        continue;
      }
      const start = j;
      let attr = '';
      while (j < template.length && !/[\s=>\/{]/.test(template[j])) attr += template[j++];
      if (!attr) { j++; continue; }
      let value = null;
      let kind = 'bare';
      if (template[j] === '=') {
        j++;
        const q = template[j];
        if (q === '"' || q === "'") {
          const close = findClose(template, j + 1, q);
          value = template.slice(j + 1, close);
          j = close + 1;
          kind = 'quoted';
        } else if (q === '{') {
          const s = j;
          j = skipBraces(template, j);
          value = template.slice(s, j);
          kind = 'expr';
        } else {
          const s = j;
          while (j < template.length && !/[\s>]/.test(template[j])) j++;
          value = template.slice(s, j);
          kind = 'unquoted';
        }
      }
      attrs.push({ name: attr, value, kind, offset: start });
    }
    yield { name, attrs, offset: at };
    i = Math.max(j, at + 1);
  }
}

/** Index of the closing quote; a `{...}` inside a quoted value may hold the quote character, so braces are skipped. */
function findClose(text, from, q) {
  let depth = 0;
  for (let k = from; k < text.length; k++) {
    const c = text[k];
    if (c === '{') depth++;
    else if (c === '}') depth = Math.max(0, depth - 1);
    else if (c === q && depth === 0) return k;
  }
  return text.length;
}

/** Index after the `}` that closes the `{` at `from`; strings and template literals inside are skipped. */
function skipBraces(text, from) {
  let depth = 0;
  for (let k = from; k < text.length; k++) {
    const c = text[k];
    if (c === '"' || c === "'" || c === '`') {
      const q = c;
      k++;
      while (k < text.length && text[k] !== q) { if (text[k] === '\\') k++; k++; }
      continue;
    }
    if (c === '{') depth++;
    else if (c === '}') { depth--; if (depth === 0) return k + 1; }
  }
  return text.length;
}

/** The text of the `{...}` object literal that starts at `open` (the index of `{`). */
function balanced(text, open) {
  return text.slice(open, skipBraces(text, open));
}

const CODE_RULES = [
  { rule: 'html-sink', re: /\.(?:innerHTML|outerHTML)\b|\binsertAdjacentHTML\s*\(|\bdocument\.write(?:ln)?\s*\(/g, why: 'writes a string as markup' },
  { rule: 'eval', re: /\beval\s*\(|\bnew\s+Function\s*\(|\b(?:setTimeout|setInterval)\s*\(\s*(['"`])/g, why: 'evaluates a string as code' },
  { rule: 'style-sink', re: /\.setAttribute\s*\(\s*['"]style['"]|\bcreateElement(?:NS)?\s*\([^)]*['"]style['"]/g, why: 'makes an inline style or a style element' },
  { rule: 'script-sink', re: /\bcreateElement(?:NS)?\s*\([^)]*['"]script['"]/g, why: 'makes a script element' },
  { rule: 'data-uri', re: /(?:['"`]\s*|url\(\s*['"]?)(?:data:(?:image|font|text\/css|application\/font)|blob:)/g, why: 'a data: or blob: source (img-src and font-src are self only)' }
];

function scanFile(file, rel) {
  const found = [];
  const text = fs.readFileSync(file, 'utf8');
  const ext = path.extname(file);
  const svelte = ext === '.svelte';
  const add = (rule, offset, why, source) => {
    const line = lineAt(text, offset);
    found.push({ file: rel, line, rule, why, source: text.split('\n')[line - 1].trim().slice(0, 200) });
  };
  let template = '';
  let code = text;
  if (svelte) ({ template, code } = splitSvelte(text));
  const bareCode = blankComments(code, false);
  for (const { rule, re, why } of CODE_RULES) {
    re.lastIndex = 0;
    let m;
    while ((m = re.exec(bareCode))) add(rule, m.index, why);
  }
  if (!svelte) {
    // An ECharts option written in a .ts file.
    chartRules(bareCode, text, add);
    return { found, dynamic: 0 };
  }
  const markup = blankComments(template, true);
  let dynamic = 0;
  for (const m of markup.matchAll(/\{@html\b/g)) add('html-tag', m.index, '{@html} parses a string as markup');
  for (const tag of startTags(markup)) {
    const hasDirective = tag.attrs.some((a) => a.name.startsWith('style:'));
    for (const a of tag.attrs) {
      if (a.name === 'style:' || a.name.startsWith('style:')) { dynamic++; continue; }
      if (a.name === 'srcdoc') add('html-sink', a.offset, 'srcdoc puts a string in an iframe as markup (it inherits the policy)');
      if (a.name !== 'style') continue;
      const braced = a.value != null && a.value.includes('{');
      if (braced || a.kind === 'expr' || hasDirective) dynamic++;
      else add('static-style', a.offset, 'a static style attribute stays in the HTML template and is blocked by style-src');
    }
    if (/^(?:img|source|link)$/i.test(tag.name)) {
      for (const a of tag.attrs) if (/^(?:src|href|srcset)$/.test(a.name) && a.value && /^\s*(?:data|blob):/i.test(a.value)) add('data-uri', a.offset, 'a data: or blob: address (img-src is self only)');
    }
  }
  chartRules(bareCode, text, add);
  return { found, dynamic };
}

/** ECharts rules, for the code of any file that builds a chart option. */
function chartRules(code, text, add) {
  if (/\bSVGRenderer\b/.test(code)) add('svg-renderer', code.search(/\bSVGRenderer\b/), 'only the canvas renderer is registered');
  const chartFile = /\becharts\b|tooltip\s*:|\bsetOption\b/.test(code);
  if (!chartFile) return;
  for (const m of code.matchAll(/\btooltip\s*:\s*\{/g)) {
    const open = m.index + m[0].length - 1;
    const body = balanced(code, open);
    if (/^\{\s*show\s*:\s*false\s*,?\s*\}$/.test(body)) continue;
    if (!/renderMode\s*:\s*['"]richText['"]/.test(body) && !/\.\.\.\s*richTip\(/.test(body)) {
      add('chart-tooltip', m.index, "an ECharts tooltip without renderMode: 'richText' (use richTip() from lib/chartTooltip.ts)");
    }
  }
  for (const m of code.matchAll(/<br\s*\/?>|<\/?b>|\bextraCssText\b|\bitem\.marker\b|\.marker\s*\+/g)) {
    // `<br/>` and `<b>` only count in a file that builds ECharts text.
    if (/\becharts\b|\bsetOption\b|\btooltip\s*:/.test(code)) add('chart-html', m.index, 'HTML or CSS text in chart text (richText tooltips are plain text with newlines)');
  }
}

function scanHtml(file, rel) {
  const found = [];
  const text = fs.readFileSync(file, 'utf8');
  const add = (rule, offset, why) => { const line = lineAt(text, offset); found.push({ file: rel, line, rule, why, source: text.split('\n')[line - 1].trim().slice(0, 200) }); };
  const body = text.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '));
  for (const m of body.matchAll(/<style\b/gi)) add('static-style', m.index, 'an inline <style> element');
  for (const tag of startTags(body)) {
    for (const a of tag.attrs) {
      if (a.name === 'style') add('static-style', a.offset, 'a style attribute in the page');
      if (/^on[a-z]+$/i.test(a.name)) add('script-sink', a.offset, 'an inline event handler');
      if (a.value && /^\s*(?:data|blob):/i.test(a.value) && /^(?:link|img|source)$/i.test(tag.name)) add('data-uri', a.offset, 'a data: address (img-src is self only)');
      if (a.value && /^\s*javascript:/i.test(a.value)) add('script-sink', a.offset, 'a javascript: address');
    }
    if (/^script$/i.test(tag.name) && !tag.attrs.some((a) => a.name === 'src')) add('script-sink', tag.offset, 'an inline script');
  }
  return found;
}

function loadAllowlist(root) {
  const file = path.join(root, 'scripts', 'csp-allowlist.json');
  if (!fs.existsSync(file)) return [];
  const list = JSON.parse(fs.readFileSync(file, 'utf8')).allow ?? [];
  for (const e of list) {
    if (!e.file || !e.rule || !e.match || !e.why) throw new Error('csp-allowlist.json: every entry needs file, rule, match and why: ' + JSON.stringify(e));
  }
  return list;
}

/** Scans `root` (the web folder). Returns { violations, stale, dynamic, files }. */
function scan(root, allow = loadAllowlist(root)) {
  const all = [];
  let dynamic = 0;
  let files = 0;
  const src = path.join(root, 'src');
  for (const file of walk(src)) {
    const rel = path.relative(root, file).split(path.sep).join('/');
    const r = scanFile(file, rel);
    files++;
    dynamic += r.dynamic;
    all.push(...r.found);
  }
  const index = path.join(root, 'index.html');
  if (fs.existsSync(index)) { all.push(...scanHtml(index, 'index.html')); files++; }
  const used = new Set();
  const violations = all.filter((v) => {
    const k = allow.findIndex((e) => e.file === v.file && e.rule === v.rule && v.source.includes(e.match));
    if (k >= 0) { used.add(k); return false; }
    return true;
  });
  const stale = allow.filter((_, k) => !used.has(k));
  return { violations, stale, dynamic, files };
}

function main() {
  const args = process.argv.slice(2);
  const at = args.indexOf('--root');
  const root = path.resolve(at >= 0 ? args[at + 1] : path.join(__dirname, '..'));
  let r;
  try {
    r = scan(root);
  } catch (e) {
    console.error('check-csp could not run: ' + e.message);
    process.exit(2);
  }
  for (const v of r.violations) console.error(`${v.file}:${v.line}  [${v.rule}] ${v.why}\n    ${v.source}`);
  for (const e of r.stale) console.error(`scripts/csp-allowlist.json: the entry for ${e.file} [${e.rule}] "${e.match}" matches nothing any more. Remove it.`);
  if (args.includes('--list')) console.log(`${r.files} files, ${r.dynamic} dynamic style bindings (written through the CSSOM, allowed).`);
  if (r.violations.length || r.stale.length) {
    console.error(`\ncheck-csp: ${r.violations.length} violation(s), ${r.stale.length} stale allowlist entr${r.stale.length === 1 ? 'y' : 'ies'}. Policy: docs/desktop/spec/02-security.md (BL-323). Fix the source; an exception goes in scripts/csp-allowlist.json with a reason.`);
    process.exit(1);
  }
  console.log(`check-csp: clean (${r.files} files, ${r.dynamic} dynamic style bindings, all CSSOM).`);
}

module.exports = { scan, scanFile, scanHtml, startTags, loadAllowlist };
if (require.main === module) main();
