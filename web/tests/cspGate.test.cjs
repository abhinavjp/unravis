// BL-323: the strict-CSP gate (scripts/check-csp.cjs) holds on the real source, and it catches each fault it claims to catch.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { scan } = require('../scripts/check-csp.cjs');

const root = path.resolve(__dirname, '..');

/** Writes a small web folder (src/ and index.html) and scans it. */
function scanFiles(files, allow = []) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'csp-gate-'));
  try {
    for (const [name, text] of Object.entries(files)) {
      const file = path.join(dir, name);
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fs.writeFileSync(file, text);
    }
    return scan(dir, allow);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}
const rulesOf = (r) => r.violations.map((v) => v.rule).sort();

test('the real source is clean: no inline style, no {@html}, no eval, no HTML tooltip, no data: picture', () => {
  const r = scan(root);
  assert.deepEqual(r.violations.map((v) => `${v.file}:${v.line} [${v.rule}] ${v.source}`), []);
  assert.deepEqual(r.stale, [], 'every line of scripts/csp-allowlist.json still matches something');
  assert.ok(r.files > 300, 'the scan reads the whole tree (' + r.files + ' files)');
  assert.ok(r.dynamic > 0, 'it counts the dynamic style bindings (CSSOM writes, allowed)');
});

test('a static style attribute in a template is caught; dynamic ones are CSSOM writes and pass', () => {
  const bad = scanFiles({ 'src/A.svelte': '<div style="color: red">x</div>\n<span class="a" style="width:1px;">y</span>' });
  assert.deepEqual(rulesOf(bad), ['static-style', 'static-style']);
  assert.deepEqual(bad.violations.map((v) => v.line), [1, 2]);
  const ok = scanFiles({
    'src/B.svelte': [
      '<script>let x = $state(1);</script>',
      '<div style="left: {x}%">a</div>',
      '<div style={`width:${x}px`}>b</div>',
      '<div style:left={x + "px"} style:--k={x}>c</div>',
      '<div style="color:red" style:left={x + "px"}>d</div>',
      '<button onclick={() => x > 1 ? 1 : 2} style="top:{x}px">e</button>'
    ].join('\n')
  });
  assert.deepEqual(ok.violations, []);
  assert.equal(ok.dynamic, 7, "bindings counted: one each in a, b and e, two in c, a static one with a directive and the directive in d");
});

test('{@html}, innerHTML-like sinks, srcdoc and eval-like calls are caught', () => {
  const r = scanFiles({
    'src/C.svelte': '<script>function f(el: HTMLElement) { el.innerHTML = "x"; el.insertAdjacentHTML("beforeend", "y"); }</script>\n<div>{@html text}</div>\n<iframe srcdoc={t}></iframe>',
    'src/d.ts': 'export const a = eval("1"); export const b = new Function("return 1"); setTimeout("go()", 5); document.write("x");',
    'src/e.ts': 'el.setAttribute("style", "a:b"); document.createElement("style"); document.createElement("script");'
  });
  assert.deepEqual(rulesOf(r), ['eval', 'eval', 'eval', 'html-sink', 'html-sink', 'html-sink', 'html-sink', 'html-tag', 'script-sink', 'style-sink', 'style-sink'].sort());
});

test('comments are not code: a rule named in a comment does not fail', () => {
  const r = scanFiles({ 'src/f.ts': '// no innerHTML here, no eval(x)\n/* new Function("a") */\nexport const ok = 1;', 'src/G.svelte': '<!-- {@html x} style="a:b" -->\n<p>ok</p>' });
  assert.deepEqual(r.violations, []);
});

test('data: and blob: sources for pictures, fonts and stylesheets are caught; a Blob variable is not', () => {
  const r = scanFiles({
    'src/h.ts': 'export const a = "data:image/svg+xml,%3Csvg"; export const b = `url(data:font/woff2;base64,AA)`;',
    'src/I.svelte': '<img src="data:image/png;base64,AAAA" alt="">',
    'src/j.ts': 'function save(blob: Blob) { const url = URL.createObjectURL(blob); return { blob: blob, url }; }'
  });
  assert.deepEqual(rulesOf(r), ['data-uri', 'data-uri', 'data-uri']);
});

test('index.html: an inline style, an inline script, a handler and a data: icon are caught', () => {
  const r = scanFiles({ 'src/x.ts': 'export {};', 'index.html': '<html><head><style>a{}</style><link rel="icon" href="data:image/svg+xml,x"></head><body style="m:0" onload="go()"><script>1</script><script src="/a.js"></script></body></html>' });
  assert.deepEqual(rulesOf(r), ['data-uri', 'script-sink', 'script-sink', 'static-style', 'static-style']);
});

test('an ECharts tooltip must be rich text; "show: false" and richTip() pass', () => {
  const bad = scanFiles({ 'src/o.ts': 'export const o = { tooltip: { trigger: "axis", formatter: (i) => i.marker + "<br/>" } };' });
  assert.deepEqual(rulesOf(bad), ['chart-html', 'chart-html', 'chart-tooltip']);
  const ok = scanFiles({
    'src/p.ts': [
      'export const a = { tooltip: { show: false } };',
      'export const b = { tooltip: { ...richTip(p, {}), trigger: "axis", formatter: () => "a\\nb" } };',
      'export const c = { tooltip: { trigger: "item", renderMode: "richText", formatter: () => "a" } };'
    ].join('\n')
  });
  assert.deepEqual(ok.violations, []);
  assert.deepEqual(rulesOf(scanFiles({ 'src/q.ts': 'import { SVGRenderer } from "echarts/renderers";' })), ['svg-renderer']);
});

test('S2: every spelling of a markup sink is caught, not only `.innerHTML`', () => {
  const sinks = {
    'a.ts': 'el["innerHTML"] = x;',
    'b.ts': 'Object.assign(el, { innerHTML: x });',
    'c.ts': 'const { outerHTML } = el;',
    'd.ts': 'el.setHTMLUnsafe(x);',
    'e.ts': 'range.createContextualFragment(x);',
    'f.ts': 'new DOMParser().parseFromString(x, "text/html");',
    'g.ts': 'frame.srcdoc = html;',
    'h.ts': 'el.insertAdjacentHTML("afterbegin", x);'
  };
  for (const [name, code] of Object.entries(sinks)) {
    assert.deepEqual([...new Set(rulesOf(scanFiles({ ['src/' + name]: code })))], ['html-sink'], code);
  }
});

test('S2: Function(...) without new, setAttribute with a template or a variable name, createElement(`style`)', () => {
  const cases = {
    'a.ts': ['eval', 'const f = Function("return 1");'],
    'b.ts': ['eval', 'const f = globalThis.Function(`return 1`);'],
    'c.ts': ['style-sink', 'el.setAttribute(`style`, "a:b");'],
    'd.ts': ['style-sink', 'el.setAttribute(name, "a:b");'],
    'e.ts': ['style-sink', 'document.createElement(`style`);'],
    'f.ts': ['script-sink', 'document.createElement(`script`);']
  };
  for (const [name, [rule, code]] of Object.entries(cases)) assert.deepEqual(rulesOf(scanFiles({ ['src/' + name]: code })), [rule], code);
  assert.deepEqual(scanFiles({ 'src/ok.ts': 'el.setAttribute("aria-label", x); el.setAttribute("role", "img");' }).violations, [], 'a literal attribute name is fine');
});

test('S2: a data: or blob: source in a .css file or a <style> block is caught', () => {
  const r = scanFiles({
    'src/brand/t.css': '.a { background: url("data:image/svg+xml,%3Csvg") }\n.b { background: url(data:application/octet-stream;base64,AA) }\n@font-face { src: url(\'../fonts/x.woff2\') }',
    'src/S.svelte': '<p>x</p>\n<style>\n  .c { background-image: url(data:image/png;base64,AAA); }\n</style>',
    'src/ok.css': '.a { background: url("../img/a.svg") } /* url(data:image/png;base64,AA) in a comment */'
  });
  assert.deepEqual(r.violations.map((v) => v.file + ':' + v.line + ' ' + v.rule).sort(), ['src/S.svelte:3 data-uri', 'src/brand/t.css:1 data-uri', 'src/brand/t.css:2 data-uri']);
  assert.equal(scanFiles({ 'src/x.ts': 'export {};', 'src/c.css': '.a{color:red}' }).violations.length, 0);
});

test('S2: a tooltip that the gate cannot read, or any renderMode other than richText, is caught', () => {
  const rules = (code) => rulesOf(scanFiles({ 'src/t.ts': code }));
  assert.deepEqual(rules('const t = { trigger: "axis" }; export const o = { tooltip: t };'), ['chart-tooltip']);
  assert.deepEqual(rules('const tooltip = {}; export const o = { tooltip, grid: {} };'), ['chart-tooltip']);
  assert.deepEqual(rules('export const o = { tooltip: { ...richTip(p, {}), renderMode: "html", formatter: () => "a" } };'), ['chart-tooltip']);
  assert.deepEqual(rules("export const o = { tooltip: { renderMode: 'html', formatter: () => 'a' } };"), ['chart-tooltip', 'chart-tooltip']);
  assert.deepEqual(rules('export const o = { tooltip: { show: false }, series: [{ tooltip: { show: false } }] };'), []);
  assert.deepEqual(rules('export const o = { tooltip: {\n  ...richTip(p, { confine: true }),\n  formatter: () => "a" } };'), []);
});

test('the allowlist lets a reviewed line through, and an entry that matches nothing fails the gate', () => {
  const files = { 'src/K.svelte': '<iframe sandbox="" srcdoc={html}></iframe>' };
  const entry = { file: 'src/K.svelte', rule: 'html-sink', match: 'srcdoc={html}', why: 'reviewed' };
  assert.deepEqual(scanFiles(files, [entry]).violations, []);
  const stale = scanFiles({ 'src/K.svelte': '<p>none</p>' }, [entry]);
  assert.equal(stale.stale.length, 1);
  assert.equal(scanFiles(files, [{ ...entry, match: 'something else' }]).violations.length, 1, 'a match on another line does not excuse this one');
});

test('the CLI exits 0 on the real tree and 1 on a planted fault', () => {
  const { spawnSync } = require('node:child_process');
  const script = path.join(root, 'scripts', 'check-csp.cjs');
  assert.equal(spawnSync(process.execPath, [script], { encoding: 'utf8' }).status, 0);
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'csp-gate-cli-'));
  try {
    fs.mkdirSync(path.join(dir, 'src'));
    fs.writeFileSync(path.join(dir, 'src', 'Z.svelte'), '<div style="a:b"></div>');
    const r = spawnSync(process.execPath, [script, '--root', dir], { encoding: 'utf8' });
    assert.equal(r.status, 1);
    assert.match(r.stderr, /src\/Z\.svelte:1 {2}\[static-style\]/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('the main and strict Lite pages keep asset URLs same-origin and the page icon is a file', () => {
  for (const [htmlFile, configFile] of [['index.html', 'vite.config.ts'], ['lite/index.html', 'vite.lite.config.ts']]) {
    const vite = fs.readFileSync(path.join(root, configFile), 'utf8');
    assert.match(vite, /assetsInlineLimit:\s*0/);
    const html = fs.readFileSync(path.join(root, htmlFile), 'utf8');
    assert.doesNotMatch(html, /data:/, htmlFile + ' must not request an inline asset under strict img-src');
  }
  assert.ok(fs.existsSync(path.join(root, 'src', 'brand', 'favicon.svg')));
  const lite = fs.readFileSync(path.join(root, 'lite', 'index.html'), 'utf8');
  assert.match(lite, /rel="icon"[^>]*href="\.\.\/src\/brand\/favicon\.svg"/);
});

test('the desktop policy the gate protects (tauri.conf.json) stays strict, and the browser-run spec reads it from there', () => {
  const conf = JSON.parse(fs.readFileSync(path.join(root, '..', 'desktop', 'src-tauri', 'tauri.conf.json'), 'utf8'));
  const csp = conf.app.security.csp;
  assert.doesNotMatch(csp, /unsafe-inline|unsafe-eval|data:|blob:/);
  assert.match(csp, /style-src 'self'(;|$)/);
  const spec = fs.readFileSync(path.join(root, '..', 'tools', 'uiaudit', 'csp', 'harness.ts'), 'utf8');
  assert.match(spec, /tauri\.conf\.json/);
  assert.match(spec, /securitypolicyviolation/);
});

test('rich-text tooltips are drawn on the canvas, so specs read them from the chart: charts.ts leaves the chart on its container and the e2e helper reads it there (BL-323)', () => {
  const charts = fs.readFileSync(path.join(root, 'src', 'lib', 'charts.ts'), 'utf8');
  assert.match(charts, /\.__chart\s*=\s*chart/);
  const helper = fs.readFileSync(path.join(root, '..', 'tools', 'e2e', 'support', 'chart-tip.ts'), 'utf8');
  assert.match(helper, /__chart/);
  assert.match(helper, /getRoots\(\)/);
  for (const spec of ['explorer.spec.ts', 'ui-layout.spec.ts']) {
    const text = fs.readFileSync(path.join(root, '..', 'tools', 'e2e', 'specs', spec), 'utf8');
    assert.match(text, /support\/chart-tip/, spec + ' reads the canvas tooltip through the helper');
    assert.doesNotMatch(text, /getByText\(\/Response-time target/, spec + ' does not look for the old HTML tooltip');
  }
});
