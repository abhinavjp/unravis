// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix8.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8').split('\r\n').join('\n'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }

// --- ES5 scan + syntax of the edited files ---
var EDITED = ['parts/41-tools-a.js', 'parts/40-manual-home.js'];
var SRC = {};
EDITED.forEach(function (p) {
  SRC[p] = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(SRC[p])) bad.push(p + ' not ES5');
  try { new Function(SRC[p]); } catch (e) { bad.push(p + ' syntax ' + e.message); }
});
var s41 = SRC['parts/41-tools-a.js'], s40 = SRC['parts/40-manual-home.js'];

// --- item 1: the Slowest APIs Change cell uses signed(), no "(+" left ---
eq('item1 cell uses signed', cnt(s41, "fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'"), 1);
eq('item1 old cell gone', cnt(s41, "fmtPct(r[4]) + ' (+' + fmtCount(r[5]) + ' ms)</td>'"), 0);

// --- item 2: no HTML entity inside any inert(...) literal of the edited files ---
EDITED.forEach(function (p) {
  rd(p).split('\n').forEach(function (line) {
    if (line.indexOf('inert(') >= 0 && /&[a-zA-Z]+;/.test(line)) bad.push(p + ' entity on inert line: ' + line.trim());
  });
});
eq('item2 no &rsquo; in 41', cnt(s41, '&rsquo;'), 0);
eq('item2 escaped inert literal', cnt(s41, "inert(\\'Opens the case\\'s Add data flow\\')"), 1);
eq('item2 old inert literal gone', cnt(s41, "inert(\\'Opens the case&rsquo;s Add data flow\\')"), 0);
eq('item2 43 has no inert', cnt(rd('parts/43-tools-c.js'), 'inert('), 0);

// --- item 3: code-point-safe note store, maxlength removed ---
eq('item3 old note slice gone', cnt(s40, 'text:String(W.noteDraft).trim().slice(0, MAX_ITEM_TEXT)'), 0);
eq('item3 code-point note store', cnt(s40, "text:chars(String(W.noteDraft).trim()).slice(0, MAX_ITEM_TEXT).join('')"), 1);
eq('item3 maxlength gone', cnt(s40, 'maxlength="400"'), 0);
eq('item3 value wiring intact', cnt(s40, "value=\"' + esc(W.noteDraft) + '\""), 1);
eq('item3 chars defined once', cnt(s40, 'function chars('), 1);

// --- item 4: chk-fix4 count stays green ---
var f4 = rd('../packets/data/chk-fix4.js');
eq('item4 chk-fix4 expects 5', cnt(f4, "eq('chars call count', cnt(mh, 'chars('), 5);"), 1);
eq('item4 chk-fix4 old 4 gone', cnt(f4, "eq('chars call count', cnt(mh, 'chars('), 4);"), 0);

// --- item 5: chk-s1A's bare harness needs the signed() stub the app always has ---
var s1a = rd('../packets/data/chk-s1A.js');
eq('item5 chk-s1A signed stub', cnt(s1a, 'c.signed = function (n) {'), 1);

// --- wiring that keeps chk-fix5 and chk-s1A green (untouched by this packet) ---
eq('wire openCallsFor button', cnt(s41, "openCallsFor(\\'"), 1);
eq('wire openCallsFor defined once', cnt(s41, 'function openCallsFor('), 1);
eq('wire escaped slowest-calls link absent', cnt(s41, "openTool(\\'slowest-calls\\')"), 0);

// --- vm: signed helper and the rendered Slowest APIs table ---
var c = { renders: 0 };
c.render = function () { c.renders++; };
c.document = { getElementById: function () { return null; } };
c.window = c;
vm.createContext(c);
['parts/10-data.js', 'parts/30-steps-0-4.js', 'parts/41-tools-a.js'].forEach(function (p) {
  try { vm.runInContext(rd(p), c); } catch (e) { console.log('FAIL load ' + p + ' ' + e.message); process.exit(0); }
});
function run(s) { return vm.runInContext(s, c); }
if (typeof c.signed !== 'function') bad.push('signed helper missing');
else { eq('signed -2', c.signed(-2), '-2'); eq('signed 2', c.signed(2), '+2'); }
run("W = { ranSteps: true, minCalls: 0, match: '', showAllApis: false, have: {} };");
run("ROUTES = [['A.Neg', 100, 500, 700, -1, -2, 'good', '-1 %'], ['A.Pos', 100, 900, 700, 1, 2, 'bad', '+29 %']];");
var sa = c.TOOL_BODIES['slowest-apis']();
has('slowest-apis -2 ms', sa, '(-2 ms)');
has('slowest-apis +2 ms', sa, '(+2 ms)');
eq('slowest-apis one -2', cnt(sa, '(-2 ms)'), 1);
eq('slowest-apis one +2', cnt(sa, '(+2 ms)'), 1);
eq('slowest-apis no (+-', cnt(sa, '(+-'), 0);

// --- item 3 runtime: a 300-emoji note keeps 300 code points, no lone surrogate ---
var m = { renders: 0 };
m.render = function () { m.renders++; };
m.document = { getElementById: function () { return null; } };
m.window = m;
vm.createContext(m);
try { vm.runInContext(rd('parts/10-data.js'), m); vm.runInContext(rd('parts/40-manual-home.js'), m); } catch (e) { console.log('FAIL load 40-manual-home ' + e.message); process.exit(0); }
function mrun(s) { return vm.runInContext(s, m); }
var LONE = "(function(t){for(var i=0;i<t.length;i++){var u=t.charCodeAt(i);if(u>=0xD800&&u<=0xDBFF){if(!(i+1<t.length&&t.charCodeAt(i+1)>=0xDC00&&t.charCodeAt(i+1)<=0xDFFF))return true;i++;}else if(u>=0xDC00&&u<=0xDFFF)return true;}return false;})(W.checklist[0].text)";
mrun("W = { closed: false, noteDraft: '', checklist: [], noted: {}, tool: 'response-time' };");
mrun("var E300 = ''; for (var i = 0; i < 300; i++) E300 += '\\uD83D\\uDE00'; W.noteDraft = E300;");
eq('300-emoji units', mrun('W.noteDraft.length'), 600);
eq('noteTooLong 300 emoji', mrun('noteTooLong(W.noteDraft)'), false);
mrun('addToolNote();');
eq('note stored', mrun('W.checklist.length'), 1);
eq('note code points', mrun('chars(W.checklist[0].text).length'), 300);
eq('note utf16 units', mrun('W.checklist[0].text.length'), 600);
eq('note text is the emoji run', mrun('W.checklist[0].text === E300'), true);
eq('note no lone surrogate', mrun(LONE), false);
mrun("W.checklist = []; W.noteDraft = E300 + '\\uD83D\\uDE00';");
eq('noteTooLong 301 emoji', mrun('noteTooLong(W.noteDraft)'), true);
mrun('addToolNote();');
eq('over-long note not stored', mrun('W.checklist.length'), 0);
mrun("W.noteDraft = '';");
if (m.noteForm().indexOf('maxlength="400"') >= 0) bad.push('noteForm still prints maxlength="400"');

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
