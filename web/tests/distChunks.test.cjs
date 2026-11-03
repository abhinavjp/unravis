const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

// A cycle between static chunk imports makes evaluation throw in the browser; the lazy host then
// shows a blank panel. This reads the built bundle, so run it after `npm run build`.
const dir = path.join(__dirname, '..', 'dist', 'assets');

// Minified output has no space before `from`: import{y as e}from"./charts-x.js"
const staticImports = (src) =>
  [...src.matchAll(/(?:^|[;}\s])(?:import|export)\s*(?:[^'"()]*?from\s*)?["']\.\/([^"']+\.js)["']/g)].map((m) => m[1]);

test('matcher sees minified chunk imports', () => {
  assert.deepEqual(staticImports('import{y as e}from"./charts-8IDv7Ffn.js";var t=1'), ['charts-8IDv7Ffn.js']);
  assert.deepEqual(staticImports('import"./a.js";export{x as y}from"./b.js"'), ['a.js', 'b.js']);
});

test('built chunks have no static import cycles', { skip: !fs.existsSync(dir) }, () => {
  const graph = new Map();
  for (const file of fs.readdirSync(dir).filter((f) => f.endsWith('.js'))) {
    const src = fs.readFileSync(path.join(dir, file), 'utf8');
    graph.set(file, [...new Set(staticImports(src))]);
  }
  assert.ok([...graph.values()].some((deps) => deps.length > 0), 'no chunk imports found: the matcher is stale');
  const state = new Map();
  const stack = [];
  const visit = (node) => {
    if (state.get(node) === 2) return;
    if (state.get(node) === 1) assert.fail('import cycle: ' + [...stack.slice(stack.indexOf(node)), node].join(' -> '));
    state.set(node, 1);
    stack.push(node);
    for (const dep of graph.get(node) ?? []) visit(dep);
    stack.pop();
    state.set(node, 2);
  };
  for (const node of graph.keys()) visit(node);
});
