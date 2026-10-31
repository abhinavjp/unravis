var fs = require('fs');
var path = require('path');
var vm = require('vm');

var FILES = [
  '14-help-sql1.js',
  '14-help-sql2.js',
  '14-help-mongo1.js',
  '14-help-mongo2.js',
  '14-help-host1.js',
  '14-help-host2.js'
];
var FIELDS = ['look', 'what', 'normal', 'issue', 'check', 'chart', 'terms'];

var dir = process.argv[2] || path.join(__dirname, '..', '..', 'wireframes', 'parts');

function fail(reason) {
  console.log('FAIL ' + reason);
  process.exit(1);
}

var fixesPath = path.join(__dirname, 'help-fixes.json');
var fixes = JSON.parse(fs.readFileSync(fixesPath, 'utf8'));

var byId = {};
var order = [];
fixes.forEach(function (fx) {
  if (FIELDS.indexOf(fx.field) === -1) {
    fail('unknown field "' + fx.field + '" for id ' + fx.id);
  }
  if (!byId[fx.id]) {
    byId[fx.id] = [];
    order.push(fx.id);
  }
  byId[fx.id].push(fx);
});

var filesData = {};
FILES.forEach(function (name) {
  var full = path.join(dir, name);
  var text;
  try {
    text = fs.readFileSync(full, 'utf8');
  } catch (e) {
    fail('cannot read ' + name);
  }
  var eol = text.indexOf('\r\n') !== -1 ? '\r\n' : '\n';
  var hadTrailing = text.length > 0 && text.slice(-eol.length) === eol;
  var body = hadTrailing ? text.slice(0, -eol.length) : text;
  filesData[name] = {
    full: full,
    eol: eol,
    hadTrailing: hadTrailing,
    lines: body.split(eol)
  };
});

var changed = 0;

order.forEach(function (id) {
  var found = false;
  for (var fi = 0; fi < FILES.length; fi++) {
    var name = FILES[fi];
    var lines = filesData[name].lines;
    var prefix = 'EVHELP["' + id + '"] = ';
    for (var li = 0; li < lines.length; li++) {
      var line = lines[li];
      if (line.indexOf(prefix) === 0) {
        found = true;
        var objText = line.slice(prefix.length);
        if (objText.charAt(objText.length - 1) === ';') {
          objText = objText.slice(0, -1);
        }
        var obj = vm.runInNewContext('(' + objText + ')');
        byId[id].forEach(function (fx) {
          obj[fx.field] = fx.value;
        });
        var terms = obj.terms || [];
        var termsStr = terms
          .map(function (t) {
            return '{ t: ' + JSON.stringify(t.t) + ', d: ' + JSON.stringify(t.d) + ' }';
          })
          .join(', ');
        lines[li] =
          'EVHELP["' + id + '"] = { look: ' + JSON.stringify(obj.look) +
          ', what: ' + JSON.stringify(obj.what) +
          ', normal: ' + JSON.stringify(obj.normal) +
          ', issue: ' + JSON.stringify(obj.issue) +
          ', check: ' + JSON.stringify(obj.check) +
          ', chart: ' + JSON.stringify(obj.chart) +
          ', terms: [' + termsStr + '] };';
        changed++;
        break;
      }
    }
    if (found) break;
  }
  if (!found) {
    fail('id not found: ' + id);
  }
});

FILES.forEach(function (name) {
  var fd = filesData[name];
  var out = fd.lines.join(fd.eol);
  if (fd.hadTrailing) out += fd.eol;
  fs.writeFileSync(fd.full, out);
});

console.log('OK ' + changed + ' cards');
process.exit(0);
