// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s2b.js   prints PASS or FAIL <reason>
var fs = require('fs'), bad = [];
var s = fs.readFileSync('parts/30-steps-0-4.js', 'utf8');
['frameReleaseLine(r)', 'frameReleaseHint()', 'frameBanner() + frameDst()', 'frameEstimateLine()', 'frameKeepTouching();', "dt('Guidance'"].forEach(function (k) {
  var n = s.split(k).length - 1;
  if (n !== 1) bad.push(k + ' occurs ' + n + ' times');
});
if (s.indexOf('The two windows overlap') >= 0) bad.push('overlap problem still there');
if (/=>|`|\blet\b|\bconst\b/.test(s)) bad.push('not ES5');
try { new Function(s); } catch (e) { bad.push('syntax ' + e.message); }
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
