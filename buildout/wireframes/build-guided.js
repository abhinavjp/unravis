#!/usr/bin/env node
'use strict';
// Concatenate parts/ in filename order into guided-analysis-layout.html.
// JS parts are wrapped in <script>; HTML parts are copied verbatim.
// No dependencies.

var fs = require('fs');
var path = require('path');

var here = __dirname;
var partsDir = path.join(here, 'parts');
var outFile = path.join(here, 'guided-analysis-layout.html');

var names = fs.readdirSync(partsDir).filter(function(n){
  return !n.startsWith('.') && (n.endsWith('.js') || n.endsWith('.html'));
}).sort();

if (!names.length) {
  console.error('No parts in ' + partsDir);
  process.exit(1);
}

var chunks = names.map(function(name){
  var raw = fs.readFileSync(path.join(partsDir, name), 'utf8');
  if (name.endsWith('.js')) {
    return '<script>\n' + raw.replace(/<\/script/gi, '<\\/script') + '\n</script>\n';
  }
  return raw;
});

var html = chunks.join('\n');
fs.writeFileSync(outFile, html, 'utf8');
var bytes = Buffer.byteLength(html);
console.log('Wrote ' + path.relative(process.cwd(), outFile) + ' from ' + names.length + ' parts (' + bytes + ' bytes)');
if (bytes > 480 * 1024) {
  console.error('Output exceeds 480 KB');
  process.exit(1);
}
