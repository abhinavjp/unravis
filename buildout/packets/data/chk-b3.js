// usage (from buildout/wireframes): node ../packets/data/chk-b3.js   prints PASS or FAIL <reason>
var fs = require('fs');
var s = fs.readFileSync('parts/02-css-ev2.html', 'utf8'), bad = [];
if (s.replace(/^\s+/, '').indexOf('<style>') !== 0) bad.push('must start with <style>');
if (!/<\/style>\s*$/.test(s)) bad.push('must end with </style>');
if ((s.match(/<style>/g) || []).length !== 1 || (s.match(/<\/style>/g) || []).length !== 1) bad.push('exactly one style block');
var css = s.replace(/<\/?style>/g, '');
if ((css.match(/\{/g) || []).length !== (css.match(/\}/g) || []).length) bad.push('unbalanced braces');
['.ev-dep', '.ev-dep-title', '.ev-dep-why', '.ev-dep-effect', '.ev-dep-fix', '.ev-dep-fixlabel', '.ev-cmd', '.ev-copy', '.ev-dep-note', '.ev-dep-pick', '.ev-impact', '.ev-tt', '.ev-fold', '.ev-qa'].forEach(function (k) {
  if (css.indexOf(k) < 0) bad.push('missing ' + k);
});
if (css.indexOf('[data-tip]') < 0 || css.indexOf('attr(data-tip)') < 0) bad.push('tooltip via attr(data-tip)');
if (css.indexOf(':focus-visible') < 0) bad.push('focus-visible');
if (/(^|[\s,}])\.(B|C)[\s.{,:]/.test(css)) bad.push('no .B/.C rules');
if (/#[0-9a-fA-F]{3,8}\b/.test(css)) bad.push('use var(--token) colours only');
['--warn', '--accent', '--line', '--surface-2', '--text-2'].forEach(function (t) { if (css.indexOf('var(' + t) < 0) bad.push('uses ' + t); });
if (s.length > 6000) bad.push('too long');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
