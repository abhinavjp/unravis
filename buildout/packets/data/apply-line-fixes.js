var fs = require('fs');
var path = require('path');

var baseDir = process.argv[2] || path.join(__dirname, '..', '..');

function fail(file, from) {
  console.log('FAIL ' + file + ' ' + from);
  process.exit(1);
}

var fixesPath = path.join(__dirname, 'line-fixes.json');
var fixes = JSON.parse(fs.readFileSync(fixesPath, 'utf8'));

var cache = {};

fixes.forEach(function (fx) {
  var full = path.join(baseDir, fx.file);
  var entry = cache[full];
  if (!entry) {
    var text;
    try {
      text = fs.readFileSync(full, 'utf8');
    } catch (e) {
      fail(fx.file, fx.from);
    }
    entry = { full: full, text: text };
    cache[full] = entry;
  }

  if (fx.all) {
    if (entry.text.indexOf(fx.from) === -1) {
      fail(fx.file, fx.from);
    }
    entry.text = entry.text.split(fx.from).join(fx.to);
  } else {
    var start = 0;
    if (Object.prototype.hasOwnProperty.call(fx, 'after')) {
      start = entry.text.indexOf(fx.after);
      if (start === -1) {
        fail(fx.file, fx.from);
      }
    }
    var pos = entry.text.indexOf(fx.from, start);
    if (pos === -1) {
      fail(fx.file, fx.from);
    }
    entry.text = entry.text.slice(0, pos) + fx.to + entry.text.slice(pos + fx.from.length);
  }
});

Object.keys(cache).forEach(function (full) {
  fs.writeFileSync(full, cache[full].text);
});

console.log('OK ' + fixes.length + ' edits');
process.exit(0);
