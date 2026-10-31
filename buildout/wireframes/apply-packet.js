// Applies SEARCH/REPLACE blocks from an agent reply (or a packet file) to files under buildout/wireframes.
// Usage: node apply-packet.js <reply.md> [--dry]
// Block format (heading line names the file, relative to this folder):
//   ### parts/01-css.html            (anything after the path on the heading line is ignored)
//   <<<<<<< SEARCH
//   old text
//   =======
//   new text
//   >>>>>>> REPLACE
// Rules: every SEARCH must occur exactly once; line endings are matched whatever the file uses (CRLF or LF)
// and the replacement is written with the file's own line ending. All blocks apply or none do.
var fs = require('fs'), path = require('path');
var args = process.argv.slice(2), dry = args.indexOf('--dry') >= 0;
var src = fs.readFileSync(args.filter(function (a) { return a[0] !== '-'; })[0], 'utf8').replace(/\r\n/g, '\n');
var re = /^###\s+(\S+)[^\n]*\n<<<<<<< SEARCH\n([\s\S]*?)\n=======\n([\s\S]*?)\n>>>>>>> REPLACE/gm;
var files = {}, m, n = 0, failed = 0;
while ((m = re.exec(src))) {
  n++;
  var rel = m[1], abs = path.join(__dirname, rel);
  if (!files[rel]) {
    var raw = fs.readFileSync(abs, 'utf8');
    files[rel] = { abs: abs, crlf: raw.indexOf('\r\n') >= 0, text: raw.replace(/\r\n/g, '\n') };
  }
  var f = files[rel], count = f.text.split(m[2]).length - 1;
  if (count !== 1) { console.log('FAIL block ' + n + ' (' + rel + '): SEARCH occurs ' + count + ' times'); failed++; continue; }
  f.text = f.text.replace(m[2], function () { return m[3]; });
  console.log('ok   block ' + n + ' (' + rel + ')');
}
if (!n) { console.log('FAIL no blocks found'); process.exit(1); }
if (failed) { console.log('NOTHING WRITTEN: ' + failed + ' block(s) failed'); process.exit(1); }
if (dry) { console.log('dry run: ' + n + ' blocks would apply'); process.exit(0); }
Object.keys(files).forEach(function (rel) {
  var f = files[rel];
  fs.writeFileSync(f.abs, f.crlf ? f.text.replace(/\n/g, '\r\n') : f.text);
});
console.log('applied ' + n + ' blocks to ' + Object.keys(files).length + ' file(s)');
